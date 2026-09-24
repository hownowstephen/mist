package mist

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// corpus.json holds templates harvested from Shopify/liquid-spec and Shopify/liquid's
// tests with liquidjs's results (scripts/oracle.mjs). Wherever mist doesn't bail it
// must agree with liquidjs exactly. MIST_CORPUS points the test at another oracle file.
type corpusCase struct {
	Src    string         `json:"src"`
	Name   string         `json:"name"`
	Tpl    string         `json:"tpl"`
	Data   map[string]any `json:"data"`
	Lax    oracle         `json:"lax"`
	Strict *oracle        `json:"strict"`
}

type oracle struct {
	Out *string `json:"out"`
	Err string  `json:"err"`
}

var bailNoise = regexp.MustCompile(`"[^"]*"|\d+`)

func TestCorpus(t *testing.T) {
	path := "testdata/corpus.json"
	if p := os.Getenv("MIST_CORPUS"); p != "" {
		path = p
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cs []corpusCase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	var accepted, bailed int
	reasons := map[string]int{}
	for _, c := range cs {
		for _, strict := range []bool{false, true} {
			want := c.Lax
			if strict && c.Strict != nil {
				want = *c.Strict
			}
			out, err := Render(nil, c.Tpl, c.Data, strict)
			var e *Error
			if errors.As(err, &e) && e.Kind == ErrUnsupported {
				bailed++
				reasons[bailNoise.ReplaceAllString(e.Msg, "…")]++
				continue
			}
			if strings.Contains(want.Err, "limit") {
				continue // liquidjs resource limit during corpus generation
			}
			accepted++
			switch {
			case err == nil && want.Out != nil && string(out) == *want.Out:
			case errors.Is(err, ErrUndefined) && strings.Contains(want.Err, "undefined variable"):
			default:
				wantS := want.Err
				if want.Out != nil {
					wantS = *want.Out
				}
				t.Errorf("%s %q strict=%v\n  tpl:  %q\n  data: %v\n  mist: %q, %v\n  liquidjs: %q", c.Src, c.Name, strict, c.Tpl, c.Data, out, err, wantS)
			}
		}
	}
	t.Logf("%d renders agreed with liquidjs, %d bailed (%.0f%% accepted)", accepted, bailed, 100*float64(accepted)/float64(accepted+bailed))
	type kv struct {
		k string
		v int
	}
	var top []kv
	for k, v := range reasons {
		top = append(top, kv{k, v})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
	for i := 0; i < len(top) && i < 15; i++ {
		t.Logf("bail %5d  %s", top[i].v, top[i].k)
	}
}
