package mist

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// TestCaseMapping checks every code point: capitalize either matches JavaScript's
// toUpperCase/toLowerCase (testdata/jscase.json, from scripts/jscase.mjs) or bails.
func TestCaseMapping(t *testing.T) {
	b, err := os.ReadFile("testdata/jscase.json")
	if err != nil {
		t.Fatal(err)
	}
	var js struct{ Upper, Lower map[string]string }
	if err := json.Unmarshal(b, &js); err != nil {
		t.Fatal(err)
	}
	want := func(m map[string]string, c rune) string {
		if s, ok := m[strconv.Itoa(int(c))]; ok {
			return s
		}
		return string(c)
	}
	var bad []string
	for c := rune(0); c <= unicode.MaxRune; c++ {
		if c >= 0xD800 && c <= 0xDFFF {
			continue
		}
		if c <= 0xFFFF && !jsUpperDiffers(c) && string(unicode.ToUpper(c)) != want(js.Upper, c) {
			bad = append(bad, "upper "+strconv.QuoteRune(c))
		}
		if !jsLowerDiffers(c) && string(unicode.ToLower(c)) != want(js.Lower, c) {
			bad = append(bad, "lower "+strconv.QuoteRune(c))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d code points map differently from JavaScript without bailing: %s", len(bad), strings.Join(bad[:min(20, len(bad))], ", "))
	}
}

func TestRegisteredFilters(t *testing.T) {
	var seen Filter
	e := Engine{Filters: map[string]FilterFunc{
		"shout": func(f Filter) (any, error) {
			seen = f
			s, _ := f.Input.(string)
			return strings.ToUpper(s) + "!", nil
		},
		"plus": func(f Filter) (any, error) {
			a, _ := f.Input.(float64)
			b, _ := f.Args[0].(float64)
			return a + b, nil
		},
		"capitalize": func(f Filter) (any, error) { return "overridden", nil },
		"fallback":   func(Filter) (any, error) { return nil, ErrUnsupported },
		"broken":     func(Filter) (any, error) { return nil, errBroken },
		"nilfn":      nil,
	}}
	vars := map[string]any{"name": "ada", "n": 2.0}
	for tpl, want := range map[string]string{
		"{{ name | shout }}":                   "ADA!",
		"{{ n | plus: 3 }}":                    "5",
		"{{ name | capitalize }}":              "overridden",
		"{{ nope | default: 'x' | shout }}":    "X!",
		"{% assign y = name | shout %}{{ y }}": "ADA!",
	} {
		if out, err := e.Render(tpl, vars, true); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}

	if _, err := e.Render("{{ nope | shout: 1, 'a' }}", vars, false); err != nil || seen.Input != nil || !seen.Strict == true && len(seen.Args) != 2 {
		t.Errorf("undefined input: got %+v, %v", seen, err)
	}
	if seen.Name != "shout" || len(seen.Args) != 2 || seen.Args[0] != 1.0 || seen.Args[1] != "a" || seen.Strict {
		t.Errorf("filter call: got %+v", seen)
	}
	for tpl, want := range map[string]error{
		"{{ name | fallback }}":         ErrUnsupported,
		"{{ name | nilfn }}":            ErrUnsupported,
		"{{ name | shout: x: 1 }}":      ErrUnsupported,
		"{{ name | shout: 1,2,3,4,5 }}": ErrUnsupported,
		"{{ name | broken }}":           errBroken,
	} {
		if _, err := e.Render(tpl, vars, false); !errors.Is(err, want) {
			t.Errorf("%s: got %v; want %v", tpl, err, want)
		}
	}
	if err := e.Check("{{ name | shout | nilfn }}"); err != nil {
		t.Errorf("Check with registered filters: %v", err)
	}
	if err := Check("{{ name | shout }}"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("package Check: got %v; want ErrUnsupported", err)
	}
}
