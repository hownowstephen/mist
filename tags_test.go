package mist

import (
	"errors"
	"fmt"
	"testing"
)

func tagEngine(calls *int) Engine {
	return Engine{Tags: map[string]TagFunc{
		"greet": func(dst []byte, t Tag) ([]byte, error) {
			*calls++
			return append(append(dst, "hi "...), t.Args...), nil
		},
		"lookup": func(dst []byte, t Tag) ([]byte, error) {
			v, ok := t.Lookup(t.Args)
			if !ok {
				return append(dst, "<undefined>"...), nil
			}
			s, _ := v.(string)
			return append(dst, s...), nil
		},
		"var": func(dst []byte, t Tag) ([]byte, error) {
			s, _ := t.Vars[t.Args].(string)
			return append(dst, s...), nil
		},
		"strict": func(dst []byte, t Tag) ([]byte, error) {
			if t.Strict {
				return append(dst, "strict"...), nil
			}
			return append(dst, "lax"...), nil
		},
		"fallback": func([]byte, Tag) ([]byte, error) { return nil, ErrUnsupported },
		"broken":   func([]byte, Tag) ([]byte, error) { return nil, errBroken },
		"if":       func(dst []byte, _ Tag) ([]byte, error) { return append(dst, "overridden"...), nil },
	}}
}

var errBroken = errors.New("broken")

func TestCustomTags(t *testing.T) {
	var calls int
	e := tagEngine(&calls)
	vars := map[string]any{"name": "data", "ps": []any{map[string]any{"url": "/a"}, map[string]any{"url": "/b"}}}
	for tpl, want := range map[string]string{
		"{% greet  bob  %}!":                                             "hi bob!",
		"a {%- greet x -%} b":                                            "ahi xb",
		"{% for p in ps %}[{% lookup p.url %}]{% endfor %}":              "[/a][/b]",
		"{% assign name = 'assigned' %}{% lookup name %}|{% var name %}": "assigned|data",
		"{% lookup nope %}|{% lookup ps[1].url %}|{% lookup a | b %}":    "<undefined>|/b|<undefined>",
		"{% if true %}yes{% endif %}":                                    "yes",
		"{% strict %}":                                                   "strict",
	} {
		if out, err := e.Render(tpl, vars, true); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}

	calls = 0
	if out, err := e.Render("{% if false %}{% greet x %}{% endif %}{% for x in none %}{% greet y %}{% endfor %}", map[string]any{"none": []any{}}, false); err != nil || out != "" || calls != 0 {
		t.Errorf("dead branches: got %q, %v, %d calls; want no calls", out, err, calls)
	}

	if _, err := e.Render("{% fallback %}", nil, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("fallback: got %v; want ErrUnsupported", err)
	}
	if _, err := e.Render("{% broken %}", nil, false); !errors.Is(err, errBroken) {
		t.Errorf("broken: got %v; want errBroken", err)
	}
}

func TestCustomTagsCheck(t *testing.T) {
	e := tagEngine(new(int))
	if err := e.Check("{% greet x %}{% if false %}{% lookup y %}{% endif %}"); err != nil {
		t.Errorf("engine Check: %v", err)
	}
	if err := Check("{% greet x %}"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("package Check: got %v; want ErrUnsupported for an unregistered tag", err)
	}
	if err := e.Check("{% unknown %}"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("engine Check: got %v; want ErrUnsupported for an unregistered tag", err)
	}
}

func TestNilTagFunc(t *testing.T) {
	e := Engine{Tags: map[string]TagFunc{"x": nil}}
	if err := e.Check("{% x %}"); err != nil {
		t.Errorf("Check: %v", err)
	}
	if _, err := e.Render("{% x %}", nil, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Render: got %v; want ErrUnsupported", err)
	}
}

func TestCustomTagsChain(t *testing.T) {
	e := tagEngine(new(int))
	res, n, _ := e.RenderChain([]Step{{Body: "{% greet a %}", Key: []string{"x"}}, {Body: "{{ x }}{% fallback %}"}}, nil)
	if n != 1 || len(res) != 1 || res[0].Out != "hi a" {
		t.Fatalf("got n=%d res=%+v; want the fallback step handed to the full engine", n, res)
	}
}

// liquidjs keeps counters in the data, so later chain steps that share it see them.
func TestCountersInChains(t *testing.T) {
	steps := []Step{
		{Body: "{% increment n %}{% increment n %}"},
		{Body: "{{ n }}", Key: []string{"out"}},
		{Body: "{% increment m %}", Vars: map[string]any{}},
		{Body: "[{{ m }}]"},
	}
	res, n, vars := RenderChain(steps, map[string]any{"n": 5.0})
	if n != len(steps) || res[0].Out != "56" || res[1].Out != "7" || res[2].Out != "0" || res[3].Out != "[]" {
		t.Fatalf("got %+v, %d", res, n)
	}
	if vars["n"] != 7.0 || vars["out"] != "7" {
		t.Errorf("hydrated vars %v", vars)
	}
}

func TestTagLookupSeesCounters(t *testing.T) {
	e := Engine{Tags: map[string]TagFunc{"show": func(dst []byte, t Tag) ([]byte, error) {
		v, _ := t.Lookup("n")
		return fmt.Appendf(dst, "%v", v), nil
	}}}
	if out, err := e.Render("{% increment n %}|{% show %}", nil, false); err != nil || out != "0|1" {
		t.Fatalf("got %q, %v", out, err)
	}
}
