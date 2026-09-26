// Package mist renders a strict subset of Liquid in a single pass, bailing
// with ErrUnsupported on anything outside the subset so the caller can fall
// back to a full engine. See SPEC.md for the exact subset.
package mist

import (
	"errors"
	"fmt"
	"maps"
	"time"
)

var (
	// ErrUnsupported means the template is outside the subset; render it with the full engine instead.
	ErrUnsupported = errors.New("mist: unsupported")
	// ErrUndefined is a strict-mode undefined variable. The full engine fails too, though a
	// syntax error later in the template may take precedence there.
	ErrUndefined = errors.New("mist: undefined variable")
)

type Error struct {
	Kind error // ErrUnsupported or ErrUndefined
	Pos  int   // byte offset into the template
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%v at offset %d: %s", e.Kind, e.Pos, e.Msg) }
func (e *Error) Unwrap() error { return e.Kind }

// Engine renders templates with custom tags and filters. The zero value has none,
// and the package-level functions use it.
type Engine struct {
	// Tags maps inline tag names, as in {% name args %}, to their implementations.
	// Built-in tag names can't be overridden.
	Tags map[string]TagFunc
	// Filters maps filter names, as in {{ x | name: arg }}, to their implementations.
	// They override built-in filters of the same name, as registerFilter does in liquidjs.
	Filters map[string]FilterFunc
	// Dialect, if set, changes how values print and compare and which constructs
	// bail. nil means liquidjs, the SPEC parity target.
	Dialect *Dialect
	// Now is the clock for the date filter's 'now' and 'today'. nil means time.Now.
	Now func() time.Time
}

// TagFunc appends a custom tag's output to dst. Returning an error that wraps
// ErrUnsupported hands the template to the full engine; any other error stops rendering.
type TagFunc func(dst []byte, t Tag) ([]byte, error)

// Tag is one use of a custom tag.
type Tag struct {
	Name   string
	Args   string         // raw text after the name, trimmed
	Vars   map[string]any // the data passed to Render, without assigns or loop variables
	Strict bool

	assigns map[string]any
	frames  []frame
}

// Lookup resolves a variable path such as `product.url` or `items[0]` in the tag's
// scope: loop variables, then assigns, then Vars. ok is false if it's undefined or
// not a valid path.
func (t Tag) Lookup(path string) (v any, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			if _, bailed := e.(bailout); !bailed {
				panic(e)
			}
			v, ok = nil, false
		}
	}()
	r := renderer{tpl: path, vars: t.Vars, assigns: t.assigns}
	r.depth = copy(r.stack[:], t.frames)
	r.setSrc(0, len(path))
	r.ws()
	v = r.path(true, true)
	r.end()
	if _, undefined := v.(undefinedT); undefined {
		return nil, false
	}
	return v, true
}

// Render renders tpl with vars. strict makes undefined variables in output an error.
func Render(tpl string, vars map[string]any, strict bool) (string, error) {
	return Engine{}.Render(tpl, vars, strict)
}

// Append is Render appending to dst, so callers can reuse a buffer and render
// without allocating. On error out is nil.
func Append(dst []byte, tpl string, vars map[string]any, strict bool) ([]byte, error) {
	return Engine{}.Append(dst, tpl, vars, strict)
}

// Check reports whether tpl is inside the subset, parsing every branch without data.
// Templates that pass can still bail at render time on data-dependent rules (see SPEC.md).
func Check(tpl string) error { return Engine{}.Check(tpl) }

// Render is the package-level Render with e's custom tags.
func (e Engine) Render(tpl string, vars map[string]any, strict bool) (string, error) {
	if start, _ := nextDelim(tpl, 0); start == len(tpl) {
		return tpl, nil
	}
	out, err := e.Append(make([]byte, 0, len(tpl)+len(tpl)/2), tpl, vars, strict)
	return string(out), err
}

// Append is the package-level Append with e's custom tags.
func (e Engine) Append(dst []byte, tpl string, vars map[string]any, strict bool) (out []byte, err error) {
	defer recoverBail(&err)
	r := renderer{tpl: tpl, out: dst, vars: vars, strict: strict, tags: e.Tags, filterFns: e.Filters, dialect: e.Dialect, now: e.Now}
	r.run()
	return r.out, nil
}

// Check is the package-level Check, also accepting e's custom tags.
func (e Engine) Check(tpl string) (err error) {
	defer recoverBail(&err)
	r := renderer{tpl: tpl, check: true, tags: e.Tags, filterFns: e.Filters, dialect: e.Dialect}
	r.run()
	return nil
}

// Step is one template in a RenderChain.
type Step struct {
	Body   string
	Key    []string // where the output is bound for later steps
	Strict bool
	Vars   map[string]any // replaces the chain vars for this step only
}

type Result struct {
	Out string
	Err error // ErrUndefined or a custom tag's error; unsupported steps are never returned
}

// RenderChain renders steps in order, binding each output into the vars seen by
// later steps (see SPEC.md § Chains). It stops at the first step mist
// can't render and returns n, its index; the caller renders steps[n:] with the full
// engine using the returned vars. The caller's maps are never mutated.
func RenderChain(steps []Step, vars map[string]any) (res []Result, n int, hydrated map[string]any) {
	return Engine{}.RenderChain(steps, vars)
}

// RenderChain is the package-level RenderChain with e's custom tags.
func (e Engine) RenderChain(steps []Step, vars map[string]any) (res []Result, n int, hydrated map[string]any) {
	res = make([]Result, 0, len(steps))
	owned := false
	var buf []byte
	for i, st := range steps {
		v := st.Vars
		if v == nil {
			v = vars
		}
		out, err := e.Append(buf[:0], st.Body, v, st.Strict)
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
