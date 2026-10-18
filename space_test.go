package mist

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// TestCharClasses checks mist's character classes against JavaScript and liquidjs
// (testdata/jschar.json, from scripts/jschar.mjs): for every code point, each class
// matches or bails, and each whitespace character renders as liquidjs renders it wherever
// mist tests for whitespace, failing where liquidjs throws.
func TestCharClasses(t *testing.T) {
	b, err := os.ReadFile("testdata/jschar.json")
	if err != nil {
		t.Fatal(err)
	}
	var js struct {
		Classes map[string][][2]rune
		Cases   []struct {
			Tpl  string
			Data map[string]any
			Out  *string
		}
	}
	if err := json.Unmarshal(b, &js); err != nil {
		t.Fatal(err)
	}
	in := func(class string, c rune) bool {
		rs, ok := js.Classes[class]
		if !ok {
			t.Fatalf("no class %q", class)
		}
		i := sort.Search(len(rs), func(i int) bool { return rs[i][1] >= c })
		return i < len(rs) && rs[i][0] <= c
	}
	preds := map[string]func(rune) bool{"space": jsSpace, "trim": isTrimBlank, "cjk": isCJK}
	var bad []string
	for c := rune(0); c <= unicode.MaxRune; c++ {
		if c >= 0xD800 && c <= 0xDFFF {
			continue
		}
		for name, f := range preds {
			if f(c) != in(name, c) {
				bad = append(bad, fmt.Sprintf("%s %U", name, c))
			}
		}
		s := "a" + string(c) + "a"
		for _, mode := range []string{"default", "pretty", "raw"} {
			want := "a-a"
			if in("slugify "+mode, c) {
				want = s
			}
			if got, ok := unlessBail(func() string { return slugify(s, []val{strVal(mode), {x: true}}, 0) }); ok && got != want {
				bad = append(bad, fmt.Sprintf("slugify %s %U", mode, c))
			}
		}
	}
	for _, c := range js.Cases {
		out, err := Render(c.Tpl, c.Data, false)
		failInternal(t, err)
		if c.Out == nil && err == nil || c.Out != nil && (err != nil || out != *c.Out) {
			want := "threw"
			if c.Out != nil {
				want = strconv.Quote(*c.Out)
			}
			bad = append(bad, fmt.Sprintf("%q %v: got %q, %v; liquidjs %s", c.Tpl, c.Data, out, err, want))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d differ from JavaScript or liquidjs:\n%s", len(bad), strings.Join(bad[:min(20, len(bad))], "\n"))
	}
}

// unlessBail runs f, reporting false if it bails.
func unlessBail(f func() string) (s string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isBail := r.(bailout); !isBail {
				panic(r)
			}
		}
	}()
	return f(), true
}
