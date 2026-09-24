package mist

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	out, err := e.Render("{{ t }}{{ f }}{{ 3 }}{{ n }}{{ j }}{{ xs }}|{{ s }}{{ missing }}{{ 'lit' }}", vars, false)
	if want := "yesnoint:3float:2.5num:2.0list:2|strlit"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
	if _, err := e.Render("{{ m }}", vars, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("map: got %v; want ErrUnsupported", err)
	}
	if _, err := e.Render("{{ odd }}", vars, false); !errors.Is(err, errToy) {
		t.Errorf("odd type: got %v; want the dialect's error", err)
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
	if _, err := e.Render("{% if missing == z %}{% endif %}{% if 3 == s %}{% endif %}{% if s == blank %}{% endif %}{% if z == nil %}{% endif %}", vars, true); err != nil {
		t.Fatal(err)
	}
	want := []call{{"==", nil, nil}, {"==", int64(3), ""}, {"==", "", Blank}, {"==", nil, nil}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("hook arguments: got %#v; want %#v", calls, want)
	}
	if _, err := e.Render("{% if 3 < 4 %}{% endif %}", vars, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unsupported comparison: got %v; want ErrUnsupported", err)
	}
}

func TestDialectReject(t *testing.T) {
	for c, tpls := range map[Constructs][]string{
		TrimMarkers:       {"a {{- x }}", "{{ x -}} b", "{%- if true %}{% endif %}", "{% comment %}x{%- endcomment %}"},
		NegativeLiterals:  {"{{ -3 }}", "{{ xs[-1] }}", "{% if false %}{{ -1 }}{% endif %}"},
		UnspacedOperators: {"{% if x==2 %}{% endif %}", "{% if x== 2 %}{% endif %}", "{% if false %}{% if a<b %}{% endif %}{% endif %}"},
		RawBlocks:         {"{% raw %}{{ x }}{% endraw %}"},
		BlankKeyword:      {"{% if x == blank %}{% endif %}"},
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
