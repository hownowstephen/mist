//go:build !race

package mist

import "testing"

// Rendering must not allocate; that's the point of mist. The race detector adds allocations.
func TestRenderDoesNotAllocate(t *testing.T) {
	vars := map[string]any{
		"customer": map[string]any{"first_name": "Ada", "plan": "pro", "since": "2019", "id": 42.0, "unsubscribed": false},
		"event":    map[string]any{"items": []any{map[string]any{"name": "Widget", "qty": 2.0}, map[string]any{"name": "Gadget", "qty": 1.0}}},
	}
	buf := make([]byte, 0, 1024)
	allocs := testing.AllocsPerRun(100, func() {
		var err error
		if buf, err = Render(buf[:0], benchTpl, vars, true); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Render allocated %v times per run; want 0", allocs)
	}
}
