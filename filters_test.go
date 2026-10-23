package mist

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
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

// TestDateTable checks date against liquidjs (testdata/jsdate.json, from scripts/jsdate.mjs):
// every directive at each instant, and each edge-case input string, matches or bails. An
// instant displayed within years 1000–9999 bails only on directives that print with the
// locale (%c, %x, %X) or the process's zone name (%Z).
func TestLocation(t *testing.T) {
	l, ok := Location("America/New_York")
	if !ok || l.String() != "America/New_York" {
		t.Fatalf("Location(America/New_York) = %v, %v", l, ok)
	}
	if again, _ := Location("America/New_York"); again != l {
		t.Error("Location doesn't cache")
	}
	for _, name := range []string{"", "Local", "Factory", "posixrules", "Nowhere/Special", "america/new_york"} {
		if _, ok := Location(name); ok {
			t.Errorf("Location(%q) loads", name)
		}
	}
}

func TestDateTable(t *testing.T) {
	b, err := os.ReadFile("testdata/jsdate.json")
	if err != nil {
		t.Fatal(err)
	}
	var js struct {
		Instants    []float64
		Formats     map[string][]*string
		InputFormat string
		Inputs      map[string]*string
	}
	if err := json.Unmarshal(b, &js); err != nil {
		t.Fatal(err)
	}
	var bad []string
	check := func(d any, f string, want *string, mayBail func(code string) bool) {
		out, err := Render("{{ d | date: f }}", map[string]any{"d": d, "f": f}, false)
		failInternal(t, err)
		var e *Error
		switch {
		case errors.As(err, &e) && errors.Is(err, ErrUnsupported):
			if !mayBail(e.Code) {
				bad = append(bad, fmt.Sprintf("%v | date: %q bailed: %v", d, f, err))
			}
		case want == nil || err != nil || out != *want:
			js := "threw"
			if want != nil {
				js = strconv.Quote(*want)
			}
			bad = append(bad, fmt.Sprintf("%v | date: %q gave %q, %v; liquidjs %s", d, f, out, err, js))
		}
	}
	for f, outs := range js.Formats {
		for i, d := range js.Instants {
			y := time.UnixMilli(int64(math.Round(d * 1000))).UTC().Year()
			check(d, f, outs[i], func(code string) bool {
				switch code {
				case "date-year-range":
					return y < 1000 || y > 9999
				case "date-locale-format":
					return strings.ContainsAny(f[len(f)-1:], "cxX")
				case "date-zone-name":
					return strings.HasSuffix(f, "Z")
				}
				return false
			})
		}
	}
	for s, want := range js.Inputs {
		check(s, js.InputFormat, want, func(string) bool { return true })
	}
	if len(bad) > 0 {
		slices.Sort(bad)
		t.Fatalf("%d dates differ from liquidjs:\n%s", len(bad), strings.Join(bad[:min(20, len(bad))], "\n"))
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

func TestFilterUndefined(t *testing.T) {
	kind := func(f Filter) (any, error) {
		var s []byte
		for i, v := range append([]any{f.Input}, f.Args...) {
			switch undef := i == 0 && f.InputUndefined() || i > 0 && f.ArgUndefined(i-1); {
			case v == nil && undef:
				s = append(s, 'u')
			case v == nil:
				s = append(s, 'n')
			case undef:
				s = append(s, '!') // undefined reported for a value
			default:
				s = append(s, 'v')
			}
		}
		return string(s), nil
	}
	e := Engine{Filters: map[string]FilterFunc{"kind": kind}}
	vars := map[string]any{"nul": nil, "s": "x", "hs": []any{map[string]any{"k": 1}}}
	for tpl, want := range map[string]string{
		"{{ missing | kind }}":                         "u",
		"{{ nul | kind }}":                             "n",
		"{{ s | kind }}":                               "v",
		"{{ hs | find_exp: 'h', 'h.k == 9' | kind }}":  "u",
		"{{ hs | find_exp: 'h', 'h.k == 1' | kind }}":  "v",
		"{{ s | kind: missing, nul, s }}":              "vunv",
		"{% assign a = missing %}{{ a | kind: a }}":    "uu",
		"{% assign a = nul %}{{ a | kind: a.b, 'z' }}": "nnv", // a property of null is null in liquidjs
	} {
		out, err := e.Render(tpl, vars, false)
		if err != nil || out != want {
			t.Errorf("%s = %q, %v; want %q", tpl, out, err, want)
		}
	}
	if (Filter{}).ArgUndefined(0) || (Filter{Args: []any{nil}}).ArgUndefined(-1) {
		t.Error("ArgUndefined out of range")
	}
}

func TestFilterNow(t *testing.T) {
	at := time.UnixMilli(1700000000123)
	clock := func(f Filter) (any, error) { return f.Now().UnixMilli(), nil }
	e := Engine{Filters: map[string]FilterFunc{"clock": clock}, Now: func() time.Time { return at }}
	if out, err := e.Render("{{ x | clock }}", nil, false); err != nil || out != "1700000000123" {
		t.Errorf("Filter.Now with Engine.Now = %q, %v", out, err)
	}
	e.Now = nil
	before := time.Now().UnixMilli()
	out, err := e.Render("{{ x | clock }}", nil, false)
	if n, _ := strconv.ParseInt(out, 10, 64); err != nil || n < before {
		t.Errorf("Filter.Now without Engine.Now = %q, %v", out, err)
	}
}

func TestRegisteredFiltersAndNaN(t *testing.T) {
	e := Engine{Filters: map[string]FilterFunc{
		"nan":  func(Filter) (any, error) { return math.NaN(), nil },
		"inf":  func(Filter) (any, error) { return math.Inf(-1), nil },
		"pinf": func(Filter) (any, error) { return math.Inf(1), nil },
		"nans": func(Filter) (any, error) { return []any{math.NaN()}, nil },
		"id":   func(f Filter) (any, error) { return f.Input, nil },
	}}
	out, err := e.Render(`{{ 1 | nan }} {{ 1 | inf }} {{ 1 | pinf }} {{ 1 | nan | json }} {{ 1 | nan | upcase }}{% assign n = 1 | nan %}{% if n <= 1 or n >= 1 or n == n %} ordered{% endif %}`, nil, false)
	if want := "NaN -Infinity Infinity null NAN"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
	for _, tpl := range []string{"{{ nil | id }}", "{{ 1 | id: nil }}", "{{ 1 | nans | uniq }}"} {
		if _, err := e.Render(tpl, nil, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: got %v; want ErrUnsupported", tpl, err)
		}
	}
}

func TestErrUnchangedKeepsUndefined(t *testing.T) {
	e := Engine{Filters: map[string]FilterFunc{
		"keep":  func(Filter) (any, error) { return nil, ErrUnchanged },
		"input": func(f Filter) (any, error) { return f.Input, nil },
	}}
	out, err := e.Render(`{% case missing | keep %}{% when other %}undefined{% endcase %}{% case missing | input %}{% when other %} null{% endcase %}{{ 'a' | keep }}`, nil, false)
	if want := "undefineda"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
}

// Undefined items reach hooks as the exported Undefined, never an unexported type.
func TestUndefinedInsideValues(t *testing.T) {
	var seen []string
	note := func(v any) { seen = append(seen, fmt.Sprintf("%#v", v)) }
	e := Engine{
		Filters: map[string]FilterFunc{"peek": func(f Filter) (any, error) { note(f.Input); return "", nil }},
		Tags: map[string]TagFunc{"peek": func(dst []byte, t Tag) ([]byte, error) {
			v, _ := t.Lookup("m")
			note(v)
			return dst, nil
		}},
		Dialect: &Dialect{Output: func(dst []byte, v any) ([]byte, error) { note(v); return dst, nil }},
	}
	data := map[string]any{"xs": []any{map[string]any{"k": 1.0}, map[string]any{}}}
	if _, err := e.Render(`{{ xs | map: 'k' | peek }}{% assign m = xs | map: 'k' %}{% peek %}{{ m }}{{ xs | group_by: 'k' | map: 'name' | peek }}`, data, false); err != nil {
		t.Fatal(err)
	}
	want := `[]interface {}{1, mist.Undefined{}}`
	for _, s := range seen {
		if s != want {
			t.Errorf("got %s; want %s", s, want)
		}
	}
	if len(seen) != 4 {
		t.Errorf("got %d hook calls", len(seen))
	}
}

// toyDecimal compares like decimal.js's Decimal in liquidjs: never == anything else,
// ordered against one-digit integers.
type toyDecimal struct{ s string }

func (d *toyDecimal) Compare(op string, other any) (bool, error) {
	switch op {
	case "==":
		return other == d, nil
	case "!=":
		return other != d, nil
	}
	o, ok := other.(int64)
	if !ok {
		return false, ErrUnsupported
	}
	switch os := fmt.Sprint(o); op {
	case "<":
		return d.s < os, nil
	case ">":
		return d.s > os, nil
	}
	return false, errToy
}

func (d *toyDecimal) String() string { return d.s }

// toyObject is a Comparer with no String.
type toyObject struct{}

func (toyObject) Compare(string, any) (bool, error) { return false, nil }

// A Comparer is a JavaScript object: truthy, never default's empty case, and a string
// as its String() wherever liquidjs calls String() on it.
func TestComparerAsObject(t *testing.T) {
	e := Engine{Filters: map[string]FilterFunc{
		"dec": func(f Filter) (any, error) { return &toyDecimal{fmt.Sprint(f.Input)}, nil },
		"obj": func(Filter) (any, error) { return toyObject{}, nil },
	}}
	tpl := "{% assign d = 2 | dec %}{{ d | prepend: '$' }}|{{ d | append: ' pts' | upcase }}|{{ 'n' | append: d }}|{% if d %}t{% endif %}|{{ d | default: 'x' | append: '' }}|{% assign o = 1 | obj %}{% if o %}t{% endif %}"
	if out, err := e.Render(tpl, nil, false); err != nil || out != "$2|2 PTS|n2|t|2|t" {
		t.Errorf("got %q, %v", out, err)
	}
	// Indexing keys by String(); case/when and an array's contains use the value's ==.
	tpl = "{% assign d = 1 | dec %}{{ xs[d] }}|{{ m[d] }}|{% case d %}{% when 1 %}one{% when d %}self{% else %}else{% endcase %}|{% assign ds = d | split: ',' %}{% if xs contains d %}c{% endif %}{% if d contains '1' %}x{% endif %}{% if '1' contains d %}s{% endif %}"
	if out, err := e.Render(tpl, map[string]any{"xs": []any{"a", "b"}, "m": map[string]any{"1": "x"}}, false); err != nil || out != "b|x|self|s" {
		t.Errorf("got %q, %v", out, err)
	}
	var me *Error
	if _, err := e.Render("{% assign o = 1 | obj %}{{ o | append: '' }}", nil, false); !errors.As(err, &me) || me.Code != "value-type" {
		t.Errorf("a Comparer without String: got %v; want value-type", err)
	}
}

func TestComparer(t *testing.T) {
	e := Engine{Filters: map[string]FilterFunc{"dec": func(f Filter) (any, error) {
		return &toyDecimal{fmt.Sprint(f.Input)}, nil
	}}}
	tpl := "{% assign d = 2 | dec %}{% assign o = 2 | dec %}" +
		"{% if d == 2 %}a{% endif %}{% if d == d %}b{% endif %}{% if d == o %}c{% endif %}{% if d != 2 %}d{% endif %}" +
		"{% if d > 1 %}e{% endif %}{% if 1 < d %}f{% endif %}{% if 3 > d %}g{% endif %}{% if d < 1 %}h{% endif %}"
	if out, err := e.Render(tpl, nil, false); err != nil || out != "bdefg" {
		t.Errorf("got %q, %v; want %q", out, err, "bdefg")
	}
	for tpl, want := range map[string]string{
		"{% assign d = 2 | dec %}{% if d > 's' %}{% endif %}": "hook:value:compare",
		"{% assign d = 2 | dec %}{% if d >= 1 %}{% endif %}":  "hook:value:compare",
		"{% assign d = 2 | dec %}{{ d | size }}":              "value-type",
	} {
		var me *Error
		if _, err := e.Render(tpl, nil, false); !errors.As(err, &me) || me.Code != want {
			t.Errorf("%s: got %v; want code %s", tpl, err, want)
		}
	}
}

// Remembered array sizes charge exactly what walking the arrays does.
func TestArraySizeCache(t *testing.T) {
	items := slices.Repeat([]any{"x"}, 300) // 600 units
	arrs := make([]any, 10)
	for i := range arrs {
		arrs[i] = slices.Clone(items)
	}
	vars := map[string]any{"items": items, "prefix": items[:299], "arrs": arrs}
	for tpl, want := range map[string]int{
		"{% for x in items %}{{ items | size }}{% endfor %}":                                         300 * 600,
		"{{ items | size }}{{ prefix | size }}{{ items | size }}":                                    600 + 598 + 600,
		"{% for a in arrs %}{{ a | size }}{% endfor %}{% for a in arrs %}{{ a | size }}{% endfor %}": 20 * 600,
	} {
		for _, mem := range []int{want, want - 1} {
			e := Engine{Limits: Limits{Memory: mem}}
			_, err := e.Render(tpl, vars, true)
			var me *Error
			if bailed := errors.As(err, &me) && me.Code == "memory-limit"; bailed != (mem < want) || !bailed && err != nil {
				t.Errorf("%s with Memory %d: %v", tpl, mem, err)
			}
		}
	}
}

// BenchmarkFilterInLoop charges a filter on the array it loops over every iteration.
func BenchmarkFilterInLoop(b *testing.B) {
	e := Engine{Limits: Limits{Memory: math.MaxInt32}}
	for _, n := range []int{1000, 10000} {
		vars := map[string]any{"items": slices.Repeat([]any{"x"}, n)}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			buf := make([]byte, 0, 8*n)
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if buf, err = e.Append(buf[:0], "{% for x in items %}{{ items | size }}{% endfor %}", vars, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
