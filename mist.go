// Package mist renders a strict subset of Liquid in a single pass, bailing
// with ErrUnsupported on anything outside the subset so the caller can fall
// back to a full engine. See SPEC.md for the exact subset.
package mist

import (
	"errors"
	"fmt"
	"maps"
)

var (
	// ErrUnsupported means the template is outside the subset; render it with the full engine instead.
	ErrUnsupported = errors.New("mist: unsupported")
	// ErrUndefined is a strict-mode undefined variable. It is authoritative: the full engine fails the same way.
	ErrUndefined = errors.New("mist: undefined variable")
)

type Error struct {
	Kind error // ErrUnsupported or ErrUndefined
	Pos  int   // byte offset into the template
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%v at offset %d: %s", e.Kind, e.Pos, e.Msg) }
func (e *Error) Unwrap() error { return e.Kind }

// Render appends the rendered template to dst. On error out is nil.
func Render(dst []byte, tpl string, vars map[string]any, strict bool) (out []byte, err error) {
	defer recoverBail(&err)
	r := renderer{tpl: tpl, out: dst, vars: vars, strict: strict}
	r.run()
	if r.undef != nil {
		return nil, r.undef
	}
	return r.out, nil
}

// Check reports whether tpl is inside the subset, parsing every branch without data.
// Templates that pass can still bail at render time on data-dependent rules (see SPEC.md).
func Check(tpl string) (err error) {
	defer recoverBail(&err)
	r := renderer{tpl: tpl, check: true}
	r.run()
	return nil
}

// Step mirrors one entry of the render service's `render` array.
type Step struct {
	Body      string
	Key       []string // where the output is bound for later steps
	Strict    bool
	Premailer bool           // unsupported: inlined output may feed later steps
	Vars      map[string]any // replaces the chain vars for this step only
}

type Result struct {
	Out string
	Err error // ErrUndefined; unsupported steps are never returned
}

// RenderChain renders steps in order, binding each output into the vars seen by
// later steps exactly as the render service does. It stops at the first step mist
// can't render and returns n, its index; the caller renders steps[n:] with the full
// engine using the returned vars. The caller's maps are never mutated.
func RenderChain(steps []Step, vars map[string]any) (res []Result, n int, hydrated map[string]any) {
	res = make([]Result, 0, len(steps))
	owned := false
	var buf []byte
	for i, st := range steps {
		if st.Premailer {
			return res, i, vars
		}
		v := st.Vars
		if v == nil {
			v = vars
		}
		out, err := Render(buf[:0], st.Body, v, st.Strict)
		if errors.Is(err, ErrUnsupported) {
			return res, i, vars
		}
		buf = out
		body := st.Body // bound as content on error, so the layout still renders
		if err == nil {
			body = string(out)
			res = append(res, Result{Out: body})
		} else {
			res = append(res, Result{Err: err})
		}
		bindPath := err == nil && st.Vars == nil && len(st.Key) > 0
		bindContent := len(st.Key) > 0 && st.Key[0] == "content"
		if !bindPath && !bindContent {
			continue
		}
		if !owned {
			vars, owned = maps.Clone(vars), true
			if vars == nil {
				vars = map[string]any{}
			}
		}
		if bindPath && !bind(vars, st.Key, body) {
			return res[:i], i, vars
		}
		if bindContent {
			vars["content"] = body
		}
	}
	return res, len(steps), vars
}

// bind stores val at key in m, which must be owned. Nested maps are cloned.
// ponytail: clones each nested level per bind; keep an owned set if deep keys get hot.
func bind(m map[string]any, key []string, val any) bool {
	if len(key) == 1 {
		m[key[0]] = val
		return true
	}
	child, isMap := m[key[0]].(map[string]any)
	if !isMap && m[key[0]] != nil {
		return false // JS would set a property on a scalar; not worth mirroring
	}
	child = maps.Clone(child)
	if child == nil {
		child = map[string]any{}
	}
	m[key[0]] = child
	return bind(child, key[1:], val)
}
