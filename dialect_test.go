package mist

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var errToy = errors.New("toy")

type call struct {
	op   string
	a, b any
}

// toyDialect prints values with type prefixes and compares by their printed form,
// deliberately unlike liquidjs, so the tests prove the hooks are used.
func toyDialect(calls *[]call) *Dialect {
	return &Dialect{
		Output: func(dst []byte, v any) ([]byte, error) {
			switch x := v.(type) {
			case bool:
				if x {
					return append(dst, "yes"...), nil
				}
				return append(dst, "no"...), nil
			case int64:
				return fmt.Appendf(dst, "int:%d", x), nil
			case float64:
				return fmt.Appendf(dst, "float:%g", x), nil
			case json.Number:
				return fmt.Appendf(dst, "num:%s", x), nil
			case []any:
				return fmt.Appendf(dst, "list:%d", len(x)), nil
			case map[string]any:
				return nil, ErrUnsupported
			case Keyword:
				return fmt.Appendf(dst, "kw:%s", x), nil
			}
			return nil, errToy
		},
		Compare: func(op string, a, b any) (bool, error) {
			*calls = append(*calls, call{op, a, b})
			switch op {
			case "==":
				return fmt.Sprint(a) == fmt.Sprint(b), nil
			case "!=":
				return fmt.Sprint(a) != fmt.Sprint(b), nil
			}
			return false, ErrUnsupported
		},
	}
}

func TestDialectOutput(t *testing.T) {
	e := Engine{Dialect: toyDialect(new([]call))}
	vars := map[string]any{"t": true, "f": false, "n": 2.5, "j": json.Number("2.0"), "xs": []any{1, 2}, "s": "str", "m": map[string]any{}, "odd": int32(1)}
	out, err := e.Render("{{ t }}{{ f }}{{ 3 }}{{ n }}{{ j }}{{ xs }}{{ empty }}|{{ s }}{{ missing }}{{ 'lit' }}", vars, false)
	if want := "yesnoint:3float:2.5num:2.0list:2kw:empty|strlit"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
	if _, err := e.Render("{{ m }}", vars, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("map: got %v; want ErrUnsupported", err)
	}
	if _, err := e.Render("{{ odd }}", vars, false); !errors.Is(err, errToy) {
		t.Errorf("odd type: got %v; want the dialect's error", err)
	}
	// echo and raw skip liquidjs's outputEscape, which the Output hook stands in for.
	for _, tpl := range []string{"{% echo t %}", "{% echo empty %}", "{{ t | raw }}"} {
		if _, err := e.Render(tpl, vars, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: got %v; want ErrUnsupported", tpl, err)
		}
	}
}

func TestDialectCompare(t *testing.T) {
	var calls []call
	e := Engine{Dialect: toyDialect(&calls)}
	vars := map[string]any{"n": 2.5, "s": "", "z": nil}
	for tpl, want := range map[string]string{
		"{% if n == '2.5' %}Y{% else %}N{% endif %}":            "Y", // liquidjs: different types, never equal
		"{% if n != 2 and s == blank %}Y{% else %}N{% endif %}": "N",
		"{% if n %}Y{% endif %}":                                "Y", // truthiness isn't a comparison
	} {
		if out, err := e.Render(tpl, vars, false); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}

	calls = nil
	if _, err := e.Render("{% if missing == z %}{% endif %}{% if 3 == s %}{% endif %}{% if s == blank %}{% endif %}{% if z == nil %}{% endif %}{% if s != empty %}{% endif %}", vars, true); err != nil {
		t.Fatal(err)
	}
	want := []call{{"==", nil, nil}, {"==", int64(3), ""}, {"==", "", Blank}, {"==", nil, nil}, {"!=", "", Empty}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("hook arguments: got %#v; want %#v", calls, want)
	}
	for _, tpl := range []string{"{% if 3 < 4 %}{% endif %}", "{% if s contains 'a' %}{% endif %}"} {
		if _, err := e.Render(tpl, vars, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: got %v; want ErrUnsupported", tpl, err)
		}
	}
	// Ordering against a keyword goes to the hook too.
	calls = nil
	if _, err := e.Render("{% if s < blank %}{% endif %}", vars, false); !errors.Is(err, ErrUnsupported) || !reflect.DeepEqual(calls, []call{{"<", "", Blank}}) {
		t.Errorf("s < blank: got %v, %#v; want ErrUnsupported from the hook", err, calls)
	}
}

func TestDialectReject(t *testing.T) {
	for c, tpls := range map[Constructs][]string{
		TrimMarkers:       {"a {{- x }}", "{{ x -}} b", "{%- if true %}{% endif %}", "{% comment %}x{%- endcomment %}"},
		NegativeLiterals:  {"{{ -3 }}", "{{ xs[-1] }}", "{% if false %}{{ -1 }}{% endif %}"},
		UnspacedOperators: {"{% if x==2 %}{% endif %}", "{% if x== 2 %}{% endif %}", "{% if false %}{% if a<b %}{% endif %}{% endif %}"},
		RawBlocks:         {"{% raw %}{{ x }}{% endraw %}"},
		BlankKeyword:      {"{% if x == blank %}{% endif %}"},
		EmptyKeyword:      {"{% if x == empty %}{% endif %}"},
	} {
		strict := Engine{Dialect: &Dialect{Reject: c}}
		for _, tpl := range tpls {
			if err := strict.Check(tpl); !errors.Is(err, ErrUnsupported) {
				t.Errorf("%s with Reject %b: Check got %v; want ErrUnsupported", tpl, c, err)
			}
			if _, err := strict.Render(tpl, map[string]any{"xs": []any{1}}, false); !errors.Is(err, ErrUnsupported) {
				t.Errorf("%s with Reject %b: Render got %v; want ErrUnsupported", tpl, c, err)
			}
			if err := Check(tpl); err != nil {
				t.Errorf("%s without a dialect: %v", tpl, err)
			}
		}
	}
	if err := (Engine{Dialect: &Dialect{Reject: UnspacedOperators}}).Check("{% if x ==2 %}{% endif %}"); err != nil {
		t.Errorf("whitespace before the operator is enough: %v", err)
	}
}

func TestDialectNoDefaultLeniency(t *testing.T) {
	e := Engine{Dialect: &Dialect{NoDefaultLeniency: true}}
	if _, err := e.Render("{{ x | default: 'd' }}", nil, true); !errors.Is(err, ErrUndefined) {
		t.Errorf("strict leading default: got %v; want ErrUndefined", err)
	}
	if out, err := e.Render("{{ x | default: 'd' }}", nil, false); err != nil || out != "d" {
		t.Errorf("lax: got %q, %v; want d", out, err)
	}
}

func TestDialectSeesFractionalNumbers(t *testing.T) {
	e := Engine{Dialect: toyDialect(new([]call))}
	out, err := e.Render("{{ 1.5 }} {{ -2.5 | abs }} {{ 2 | times: 1 }} {{ 7 | divided_by: 2 }}", nil, false)
	if want := "float:1.5 float:2.5 int:2 float:3.5"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
}

// NumberText keeps a literal's text, so a dialect can tell 2.0 from 2.
func TestDialectNumberText(t *testing.T) {
	var seen []any
	e := Engine{
		Dialect: &Dialect{NumberText: true, Output: func(dst []byte, v any) ([]byte, error) {
			seen = append(seen, v)
			return fmt.Appendf(dst, "%v", v), nil
		}},
		Filters: map[string]FilterFunc{"arg": func(f Filter) (any, error) { seen = append(seen, f.Args[0]); return f.Input, nil }},
	}
	out, err := e.Render("{{ 2.0 }}|{{ -3 }}|{{ 'x' | arg: 1.50 }}|{% assign n = 2.0 %}{{ n }}|{{ 1 | plus: 1 }}", nil, false)
	if want := "2.0|-3|x|2.0|2"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
	if want := []any{json.Number("2.0"), json.Number("-3"), json.Number("1.50"), json.Number("2.0"), int64(2)}; !reflect.DeepEqual(seen, want) {
		t.Errorf("hooks saw %#v; want %#v", seen, want)
	}
}

// NoBuiltinFilters leaves only registered filters.
func TestDialectNoBuiltinFilters(t *testing.T) {
	e := Engine{
		Dialect: &Dialect{NoBuiltinFilters: true},
		Filters: map[string]FilterFunc{
			"mine": func(Filter) (any, error) { return "m", nil },
			"back": func(Filter) (any, error) { return nil, ErrBuiltin },
		},
	}
	if out, err := e.Render("{{ x | mine }}", nil, false); err != nil || out != "m" {
		t.Errorf("registered: got %q, %v", out, err)
	}
	for _, tpl := range []string{"{{ x | upcase }}", "{{ x | default: 1 }}", "{{ x | back }}", "{% if x %}{{ y | size }}{% endif %}"} {
		var me *Error
		_, err := e.Render(tpl, nil, false)
		if tpl[len(tpl)-1] == '}' && strings.Contains(tpl, "if x") {
			err = e.Check(tpl) // dead at render time, so only Check sees it
		}
		if !errors.As(err, &me) || !strings.HasPrefix(me.Code, "filter:") {
			t.Errorf("%s: got %v; want a filter: bail", tpl, err)
		}
	}
}

// StrictAssign raises an undefined assign value under strict.
func TestDialectStrictAssign(t *testing.T) {
	tpl := "{% assign x = missing %}[{{ x }}]"
	if out, err := (Engine{}).Render(tpl, nil, true); err != nil || out != "[]" {
		t.Errorf("liquidjs: got %q, %v; want [] (lenient)", out, err)
	}
	e := Engine{Dialect: &Dialect{StrictAssign: true}}
	if _, err := e.Render(tpl, nil, true); !errors.Is(err, ErrUndefined) {
		t.Errorf("StrictAssign: got %v; want ErrUndefined", err)
	}
	if out, err := e.Render(tpl, nil, false); err != nil || out != "[]" {
		t.Errorf("StrictAssign, lax: got %q, %v; want []", out, err)
	}
}
