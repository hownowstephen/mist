package mist

import (
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// corpus.json holds templates harvested from Shopify/liquid-spec and Shopify/liquid's
// tests with liquidjs's results (scripts/oracle.mjs). Wherever mist doesn't bail it
// must agree with liquidjs exactly, except that ErrUndefined only requires liquidjs to
// fail too (it may report a later syntax error instead). MIST_CORPUS points the test at another oracle file.
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

var updateAccepted = flag.Bool("update-accepted", false, "rewrite testdata/corpus.accepted")

// corpusEngine uses oracle.mjs's fixed Date.now, and passes unknown filters through
// as oracle.mjs's liquidjs does by default.
var corpusEngine = Engine{Now: func() time.Time { return time.UnixMilli(1700000000123) }, PassUnknownFilters: true}

func TestCorpus(t *testing.T) {
	path := "testdata/corpus.json"
	if p := os.Getenv("MIST_CORPUS"); p != "" {
		path = p
	}
	baseline := path == "testdata/corpus.json"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cs []corpusCase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	var accepted, mismatched, bailed int
	var keys []string
	reasons := map[string]int{}
	for _, c := range cs {
		for i, strict := range []bool{false, true} {
			want := c.Lax
			if strict && c.Strict != nil {
				want = *c.Strict
			}
			out, err := corpusEngine.Render(c.Tpl, c.Data, strict)
			failInternal(t, err)
			var e *Error
			if errors.As(err, &e) && errors.Is(err, ErrUnsupported) {
				bailed++
				reasons[e.Code]++
				continue
			}
			keys = append(keys, c.Src+"\t"+c.Name+"\t"+[]string{"lax", "strict"}[i])
			if strings.Contains(want.Err, "limit") {
				continue // liquidjs resource limit during corpus generation
			}
			accepted++
			switch {
			case err == nil && want.Out != nil && out == *want.Out:
			case errors.Is(err, ErrUndefined) && want.Err != "":
			default:
				mismatched++
				wantS := want.Err
				if want.Out != nil {
					wantS = *want.Out
				}
				t.Errorf("%s %q strict=%v\n  tpl:  %q\n  data: %v\n  mist: %q, %v\n  liquidjs: %q", c.Src, c.Name, strict, c.Tpl, c.Data, out, err, wantS)
			}
		}
	}
	if baseline {
		checkAccepted(t, keys)
	}
	t.Logf("%d renders agreed with liquidjs, %d didn't, %d bailed (%.0f%% accepted)", accepted-mismatched, mismatched, bailed, 100*float64(accepted)/float64(accepted+bailed))
	type kv struct {
		k string
		v int
	}
	var top []kv
	for k, v := range reasons {
		top = append(top, kv{k, v})
	}
	slices.SortFunc(top, func(a, b kv) int { return cmp.Compare(b.v, a.v) })
	for _, r := range top[:min(15, len(top))] {
		t.Logf("bail %5d  %s", r.v, r.k)
	}
}

// testdata/corpus.accepted lists the corpus renders mist doesn't bail on, so a change that
// makes mist bail on more of them fails. After a deliberate change, or regenerating the
// corpus, rewrite it with go test -run TestCorpus -update-accepted.
func checkAccepted(t *testing.T, keys []string) {
	if *updateAccepted {
		if err := os.WriteFile("testdata/corpus.accepted", []byte(encodeAccepted(keys)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile("testdata/corpus.accepted")
	if err != nil {
		t.Fatal(err)
	}
	want := decodeAccepted(string(b))
	count := map[string]int{}
	for _, k := range keys {
		count[k]++
	}
	var lost []string
	for _, k := range want {
		if count[k]--; count[k] < 0 {
			lost = append(lost, k)
		}
	}
	gained := 0
	for k, n := range count {
		if n > 0 {
			gained += n
			t.Logf("newly accepted: %s", k)
		}
	}
	if gained > 0 {
		t.Logf("%d renders newly accepted; record them with go test -run TestCorpus -update-accepted", gained)
	}
	if len(lost) > 0 {
		t.Errorf("%d renders in testdata/corpus.accepted now bail; if that's deliberate, run go test -run TestCorpus -update-accepted:\n%s", len(lost), strings.Join(lost[:min(20, len(lost))], "\n"))
	}
}

// encodeAccepted groups "src\tname\tmode" keys under a line per src: "\tname" where both modes
// are accepted, "\tname\tmode" where one is. Names repeat once per duplicate case.
func encodeAccepted(keys []string) string {
	slices.Sort(keys)
	var b strings.Builder
	src := ""
	for i := 0; i < len(keys); {
		s, rest, _ := strings.Cut(keys[i], "\t")
		name := rest[:strings.LastIndexByte(rest, '\t')]
		prefix := s + "\t" + name + "\t"
		j, lax := i, 0
		for ; j < len(keys) && (keys[j] == prefix+"lax" || keys[j] == prefix+"strict"); j++ {
			if keys[j] == prefix+"lax" {
				lax++
			}
		}
		if s != src {
			src = s
			b.WriteString(s + "\n")
		}
		both := min(lax, j-i-lax)
		for range both {
			b.WriteString("\t" + name + "\n")
		}
		for range lax - both {
			b.WriteString("\t" + name + "\tlax\n")
		}
		for range j - i - lax - both {
			b.WriteString("\t" + name + "\tstrict\n")
		}
		i = j
	}
	return b.String()
}

func decodeAccepted(text string) []string {
	var keys []string
	src := ""
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		name, ok := strings.CutPrefix(line, "\t")
		switch {
		case !ok:
			src = line
		case strings.HasSuffix(name, "\tlax"), strings.HasSuffix(name, "\tstrict"):
			keys = append(keys, src+"\t"+name)
		default:
			keys = append(keys, src+"\t"+name+"\tlax", src+"\t"+name+"\tstrict")
		}
	}
	return keys
}
