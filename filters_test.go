package mist

import (
	"encoding/json"
	"errors"
	"math"
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
		if !jsUpperDiffers(c) && string(unicode.ToUpper(c)) != want(js.Upper, c) {
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
		"capitalize": func(Filter) (any, error) { return "overridden", nil },
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

func TestStringFiltersBailOnInvalidUTF8(t *testing.T) {
	vars := map[string]any{"s": "a\xffb"}
	for _, tpl := range []string{"{{ s | truncate: 1 }}", "{{ s | split: '' }}", "{{ 'x' | strip: s }}", "{{ s | url_encode }}", "{{ s | size }}", "{{ s.size }}", "{{ s | base64_encode }}", "{{ 'x' | hmac_sha256: s }}"} {
		if _, err := Render(tpl, vars, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: got %v; want ErrUnsupported", tpl, err)
		}
	}
}

// liquidjs's strip_html never returns on these, so they can't be parity cases.
func TestStripHTMLBailsWhereLiquidJSHangs(t *testing.T) {
	for _, s := range []string{"ab<c", "a<", "a<!-- x", "<b>x</b>y<z", "<script>a</script>b<c"} {
		if _, err := Render(`{{ s | strip_html }}`, map[string]any{"s": s}, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%q: got %v; want ErrUnsupported", s, err)
		}
	}
}

// Outputs as liquidjs gives them with strictFilters off, its default.
func TestPassUnknownFilters(t *testing.T) {
	e := Engine{PassUnknownFilters: true}
	vars := map[string]any{"x": "a", "xs": []any{1.0, 2.0}}
	for tpl, want := range map[string]string{
		`{{ x | nope }}`:                             "a",
		`{{ x | nope: 1, "b" }}`:                     "a",
		`{{ x | nope | upcase }}`:                    "A",
		`{{ undef | default: "d" | nope }}`:          "d",
		`{% if x | nope %}y{% endif %}`:              "y",
		`{{ xs | nope | size }}`:                     "2",
		`{% assign y = x | nope %}{{ y }}`:           "a",
		`{% if false %}{{ x | nope: 1 }}{% endif %}`: "",
	} {
		if out, err := e.Render(tpl, vars, true); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}
	for _, tpl := range []string{`{{ x | nope: undef }}`, `{{ undef | nope }}`, `{{ undef | nope | default: "d" }}`} {
		if _, err := e.Render(tpl, vars, true); !errors.Is(err, ErrUndefined) {
			t.Errorf("%s: got %v; want ErrUndefined (liquidjs evaluates the arguments)", tpl, err)
		}
	}
	// liquidjs defines these, so passing them through would be wrong.
	for _, tpl := range []string{`{{ xs | uniq }}`, `{{ xs | compact }}`, `{{ x | default: 1, 2 }}`} {
		if _, err := e.Render(tpl, vars, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: got %v; want ErrUnsupported", tpl, err)
		}
	}
	if err := e.Check(`{{ x | nope }}`); err != nil {
		t.Errorf("Check: %v", err)
	}
	if _, err := Render(`{{ x | nope }}`, vars, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("without the option: got %v; want ErrUnsupported", err)
	}
}

func TestErrBuiltinAppliesTheBuiltInFilter(t *testing.T) {
	builtin := func(f Filter) (any, error) {
		if f.Input == nil {
			return "wrapped", nil
		}
		return nil, ErrBuiltin
	}
	e := Engine{Filters: map[string]FilterFunc{"append": builtin, "where_exp": builtin, "default": builtin, "no_such_filter": builtin}}
	vars := map[string]any{"s": "a", "xs": []any{1.0, 2.0, 3.0}, "f": false}
	for tpl, want := range map[string]string{
		"{{ s | append: 'b' }}|{{ missing | append: 'b' }}":               "ab|wrapped",
		"{{ xs | where_exp: 'x', 'x > 1' | join: ',' }}":                  "2,3",
		"{{ f | default: 'd' }}{{ f | default: 'd', allow_false: true }}": "dfalse",
	} {
		if out, err := e.Render(tpl, vars, false); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}
	if _, err := e.Render("{{ s | no_such_filter }}", vars, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ErrBuiltin without a built-in: got %v; want ErrUnsupported", err)
	}
	if err := e.Check("{{ s | append }}"); err != nil {
		t.Errorf("Check of a registered filter: got %v; want nil", err)
	}
}

func TestRegisteredFiltersAndNaN(t *testing.T) {
	e := Engine{Filters: map[string]FilterFunc{
		"nan": func(Filter) (any, error) { return math.NaN(), nil },
		"inf": func(Filter) (any, error) { return math.Inf(-1), nil },
		"id":  func(f Filter) (any, error) { return f.Input, nil },
	}}
	out, err := e.Render(`{{ 1 | nan }} {{ 1 | inf }} {{ 1 | nan | json }} {{ 1 | nan | upcase }}{% assign n = 1 | nan %}{% if n <= 1 or n >= 1 or n == n %} ordered{% endif %}`, nil, false)
	if want := "NaN -Infinity null NAN"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
	for _, tpl := range []string{"{{ nil | id }}", "{{ 1 | id: nil }}"} {
		if _, err := e.Render(tpl, nil, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: got %v; want ErrUnsupported", tpl, err)
		}
	}
}
