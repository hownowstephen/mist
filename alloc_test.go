//go:build !race

package mist

import "testing"

// Append must not allocate; that's the point of mist. The race detector adds allocations.
func TestAppendDoesNotAllocate(t *testing.T) {
	tpl, vars, _ := benchFixture(t, "basic")
	buf := make([]byte, 0, 1024)
	allocs := testing.AllocsPerRun(100, func() {
		var err error
		if buf, err = Append(buf[:0], tpl, vars, true); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Append allocated %v times per run; want 0", allocs)
	}
}

func TestEngineWithUnusedTagsDoesNotAllocate(t *testing.T) {
	e := Engine{Tags: map[string]TagFunc{"greet": func(dst []byte, _ Tag) ([]byte, error) { return dst, nil }}}
	tpl, vars, _ := benchFixture(t, "basic")
	buf := make([]byte, 0, 1024)
	if allocs := testing.AllocsPerRun(100, func() {
		var err error
		if buf, err = e.Append(buf[:0], tpl, vars, true); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Fatalf("Engine.Append allocated %v times per run; want 0", allocs)
	}
}

func TestDefaultDoesNotAllocate(t *testing.T) {
	vars := map[string]any{"customer": map[string]any{"first_name": "Ada"}}
	tpl := `Hi {{ customer.first_name | default: "there" }}, {{ customer.nickname | default: customer.first_name }}!`
	buf := make([]byte, 0, 256)
	if allocs := testing.AllocsPerRun(100, func() {
		var err error
		if buf, err = Append(buf[:0], tpl, vars, true); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Fatalf("default allocated %v times per run; want 0", allocs)
	}
}

func TestRenderWithoutLiquidDoesNotAllocate(t *testing.T) {
	tpl := "Your order { has } been filled."
	if allocs := testing.AllocsPerRun(100, func() {
		if out, err := Render(tpl, nil, true); err != nil || out != tpl {
			t.Fatalf("Render = %q, %v", out, err)
		}
	}); allocs != 0 {
		t.Fatalf("Render allocated %v times per run; want 0", allocs)
	}
}

// A custom tag inside a loop costs no allocation per call.
func TestTagCallsInLoopsDoNotAllocate(t *testing.T) {
	e := Engine{Tags: map[string]TagFunc{"t": func(dst []byte, _ Tag) ([]byte, error) { return dst, nil }}}
	buf := make([]byte, 0, 256)
	render := func(tpl string) float64 {
		return testing.AllocsPerRun(100, func() {
			var err error
			if buf, err = e.Append(buf[:0], tpl, map[string]any{"xs": make([]any, 50)}, false); err != nil {
				t.Fatal(err)
			}
		})
	}
	if one, many := render(`{% for x in xs limit: 1 %}{% t %}{% endfor %}`), render(`{% for x in xs %}{% t %}{% endfor %}`); many > one {
		t.Fatalf("50 tag calls allocated %v times, 1 call %v; want no more", many, one)
	}
}
