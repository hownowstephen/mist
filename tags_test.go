package mist

import (
	"errors"
	"fmt"
	"slices"
	"strings"
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

func TestTagLookupAssignedNil(t *testing.T) {
	e := Engine{Tags: map[string]TagFunc{"show": func(dst []byte, t Tag) ([]byte, error) {
		a, aok := t.Lookup("a")
		b, bok := t.Lookup("b")
		return fmt.Appendf(dst, "%v %v %v %v", a, aok, b, bok), nil
	}}}
	if out, err := e.Render("{% assign a = nil %}{% assign b = m %}{% show %}", nil, false); err != nil || out != "<nil> true <nil> false" {
		t.Fatalf("got %q, %v", out, err)
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

// nest renders the variable its argument names, as the render service's render_liquid does.
var nest = Engine{Tags: map[string]TagFunc{"nest": func(dst []byte, t Tag) ([]byte, error) {
	src, _ := t.Lookup(t.Args)
	s, _ := src.(string)
	return t.Render(dst, s)
}}}

// Expected outputs are liquidjs's, with nest rendering through the caller's Context.
func TestTagRender(t *testing.T) {
	for _, c := range []struct {
		tpl  string
		vars map[string]any
		want string
	}{
		{"{% assign x = 'a' %}{% for i in (1..2) %}{% nest t %}{% endfor %}|{{ y }}|{% increment c %}", map[string]any{"t": "{{ i }}{{ x }}{{ forloop.index }}{% assign y = i %}{% increment c %}"}, "1a102a21|2|2"},
		{"{% capture z %}<{% nest u %}>{% endcapture %}[{{ z }}]{{ w }}", map[string]any{"u": "{% assign w = 'W' %}in"}, "[<in>]W"},
	} {
		if out, err := nest.Render(c.tpl, c.vars, false); err != nil || out != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.tpl, out, err, c.want)
		}
	}
	for _, c := range []struct {
		tpl    string
		vars   map[string]any
		strict bool
		kind   error
		code   string
	}{
		{"{% nest v %}", map[string]any{"v": "{{ missing }}"}, true, ErrUndefined, "undefined"},
		{"{% nest v %}", map[string]any{"v": "{% if true %}"}, false, ErrUnsupported, "unclosed-block"},
		{"{% for i in (1..3) %}{% nest v %}{% endfor %}", map[string]any{"v": "{% endfor %}"}, false, ErrUnsupported, "unexpected-block-tag"},
		{"{% for i in (1..3) %}{% nest v %}{% endfor %}", map[string]any{"v": "{% break %}"}, false, ErrUnsupported, "break-outside-for"}, // liquidjs breaks the caller's loop
		{"{% nest v %}", map[string]any{"v": "{% nest v %}"}, false, ErrUnsupported, "nested-render-depth"},
		{"{% nest v %}", map[string]any{"v": "{% for i in (1..10) %}{% endfor %}{% for i in (1..10) %}{% endfor %}"}, false, ErrUnsupported, "iteration-limit"},
	} {
		e := nest
		e.Limits.Iterations = 15 // shared with the nested render
		var me *Error
		_, err := e.Render(c.tpl, c.vars, c.strict)
		if !errors.Is(err, c.kind) || !errors.As(err, &me) || me.Code != c.code || me.Pos != strings.Index(c.tpl, "{% nest")+len("{%") {
			t.Errorf("%s with %v: got %v; want %v %s at the tag", c.tpl, c.vars, err, c.kind, c.code)
		}
	}
}

// Depth lets a tag cap how deep its own Tag.Render calls nest.
func TestTagDepth(t *testing.T) {
	var depths []int
	e := Engine{Tags: map[string]TagFunc{"nest": func(dst []byte, t Tag) ([]byte, error) {
		depths = append(depths, t.Depth)
		src, _ := t.Lookup(t.Args)
		s, _ := src.(string)
		return t.Render(dst, s)
	}}}
	if _, err := e.Render("{% nest a %}", map[string]any{"a": "{% nest b %}", "b": "x"}, false); err != nil || !slices.Equal(depths, []int{0, 1}) {
		t.Errorf("got %v, %v; want depths [0 1]", depths, err)
	}
}
