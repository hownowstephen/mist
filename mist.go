// Package mist renders a strict subset of Liquid in a single pass, bailing
// with ErrUnsupported on anything outside the subset so the caller can fall
// back to a full engine. See SPEC.md for the exact subset.
package mist

import (
	"errors"
	"fmt"
	"maps"
	"time"
	"unsafe"
)

var (
	// ErrUnsupported means the template is outside the subset; render it with the full engine instead.
	ErrUnsupported = errors.New("mist: unsupported")
	// ErrUndefined is a strict-mode undefined variable. The full engine fails too, though a
	// syntax error later in the template may take precedence there.
	ErrUndefined = errors.New("mist: undefined variable")
	// ErrBuiltin returned by a FilterFunc applies the built-in filter of the same name
	// to the same input and arguments, as if none were registered.
	ErrBuiltin = errors.New("mist: use the built-in filter")
	// ErrUnchanged returned by a FilterFunc leaves the filter's input as it was. Unlike
	// returning Filter.Input, it keeps an undefined input undefined rather than null.
	ErrUnchanged = errors.New("mist: leave the input unchanged")
	// ErrInternal is a panic in mist or in a registered tag, filter or dialect hook,
	// recovered so the caller can fall back. It wraps ErrUnsupported. It means a bug, so
	// report it: Error.Stack says where it happened.
	ErrInternal = fmt.Errorf("%w: internal error", ErrUnsupported)
)

// Error is the error Render, Append and Check return. errors.Is matches its Kind.
type Error struct {
	// Kind is ErrUnsupported, ErrUndefined, ErrInternal, or the error a registered tag,
	// filter or dialect hook returned.
	Kind error
	// Code names the reason in a form that stays the same across releases, for metrics
	// and grouping: "undefined", "internal", "tag:NAME" or "filter:NAME" for a tag or
	// filter mist doesn't support, "hook:tag:NAME", "hook:filter:NAME" or
	// "hook:dialect:output" for a hook's error, and otherwise a kebab-case reason such as
	// "nesting-too-deep" (testdata/codes.txt lists them all). Msg is for people and may change.
	Code string
	Pos  int // byte offset into the template
	// Line and Col are Pos as liquidjs counts them: lines split on \n, columns in UTF-16
	// code units, both from 1.
	Line, Col int
	Msg       string
	// Path, for ErrUndefined, is what liquidjs's error names: the keys up to and including
	// the first undefined one, bracket indexes as their values' JavaScript strings, so its
	// message is "undefined variable: " + strings.Join(Path, ".") + ", line:Line, col:Col".
	// It's nil where mist can't say.
	Path  []string
	Stack []byte // ErrInternal: the stack where the panic happened
}

func (e *Error) Error() string { return fmt.Sprintf("%v at offset %d: %s", e.Kind, e.Pos, e.Msg) }
func (e *Error) Unwrap() error { return e.Kind }

// Engine renders templates with custom tags and filters. The zero value has none,
// and the package-level functions use it. An Engine is safe for concurrent use as long
// as nothing modifies it or its maps while it renders.
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
	// PassUnknownFilters passes the input of a filter that is neither built in nor in
	// Filters through unchanged, after evaluating its arguments, as liquidjs does with
	// strictFilters off (its default). Leave it false, so such templates bail, unless
	// Filters already covers every filter the full engine defines.
	PassUnknownFilters bool
	// Limits bound each render; zero fields take the defaults.
	Limits Limits
}

// Limits bound one render. Going over one bails with ErrUnsupported, so the full engine,
// with its own limits, decides.
type Limits struct {
	// Memory is a budget in liquidjs's memoryLimit units. Each filter and range spends at
	// least what liquidjs charges it (the lengths of a filter's input and arguments, join's
	// and slice's sizes, and a range's items), so with Memory at or below the full engine's
	// memoryLimit, mist never renders a template that engine rejects. Default 10,000,000.
	Memory int
	// Output is the most bytes one render writes, captures included. Default 4 MiB.
	Output int
	// Iterations is the most for-loop iterations in one render. Default 1,000,000.
	Iterations int
}

func (l Limits) orDefaults() Limits {
	if l.Memory <= 0 {
		l.Memory = 10_000_000
	}
	if l.Output <= 0 {
		l.Output = 4 << 20
	}
	if l.Iterations <= 0 {
		l.Iterations = 1_000_000
	}
	return l
}

// TagFunc appends a custom tag's output to dst. Returning an error that wraps
// ErrUnsupported hands the template to the full engine; any other error stops rendering.
type TagFunc func(dst []byte, t Tag) ([]byte, error)

// Tag is one use of a custom tag, valid only while its TagFunc runs.
type Tag struct {
	Name   string
	Args   string         // raw text after the name, trimmed
	Source string         // the tag as written, {% to %}; a {% liquid %} line has no delimiters
	Vars   map[string]any // the data passed to Render, without assigns or loop variables
	Strict bool
	Depth  int // how many Tag.Render calls the tag runs inside: 0 in the template itself

	frames []frame
	scope  *tagScope
}

// tagScope is the render state a Tag reads and Tag.Render carries on, copied back after the call.
type tagScope struct {
	config
	shared
	outUsed int // output so far, against limits.Output
	nested  int
}

// Lookup resolves a variable path such as `product.url` or `items[0]` in the tag's
// scope: loop variables, then assigns, then Vars. ok is false if it's undefined or
// not a valid path; undefined items of an array or object it returns are Undefined.
func (t Tag) Lookup(path string) (v any, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			if _, bailed := e.(bailout); !bailed {
				panic(e)
			}
			v, ok = nil, false
		}
	}()
	r := renderer{tpl: path, config: config{vars: t.Vars}, shared: shared{assigns: t.scope.assigns, env: t.scope.env}}
	for _, f := range t.frames {
		r.push(f)
	}
	r.setSrc(0, len(path))
	r.ws()
	v = r.path(true, true)
	r.end()
	switch v.(type) {
	case Undefined:
		return nil, false
	case nilLitT:
		return nil, true // an assigned nil literal
	}
	return v, true
}

// maxNested bounds how deep Tag.Render nests.
const maxNested = 100

// Render appends src rendered as Liquid in the tag's scope, as liquidjs renders a
// template with the caller's Context: src sees the tag's loop variables, assigns and
// Vars, and its assigns, counters and cycles persist after it. It shares the render's
// limits. Blocks src opens must close in it, and its break and continue can't reach
// the tag's loops. A TagFunc can return its error as is: an *Error stops the outer
// render with the same Kind and Code, at the tag. On error out is nil.
func (t Tag) Render(dst []byte, src string) (out []byte, err error) {
	p := t.scope
	if p.nested == maxNested {
		return nil, &Error{Kind: ErrUnsupported, Code: "nested-render-depth", Msg: fmt.Sprintf("Tag.Render nested %d deep", maxNested)}
	}
	c := &renderer{config: p.config, shared: p.shared, tpl: src, out: dst, outBase: len(dst) - p.outUsed, nested: p.nested + 1}
	for _, f := range t.frames {
		c.push(f)
	}
	c.floor = c.depth
	defer func() { p.shared = c.shared }()
	defer c.recoverBail(&err)
	c.run()
	return c.out, nil
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

// Render is the package-level Render with e's configuration.
func (e Engine) Render(tpl string, vars map[string]any, strict bool) (string, error) {
	if start, _ := nextDelim(tpl, 0); start == len(tpl) && len(tpl) <= e.Limits.orDefaults().Output {
		return tpl, nil
	}
	out, err := e.Append(make([]byte, 0, len(tpl)+len(tpl)/2), tpl, vars, strict)
	return unsafe.String(unsafe.SliceData(out), len(out)), err // out is ours alone
}

// Append is the package-level Append with e's configuration.
func (e Engine) Append(dst []byte, tpl string, vars map[string]any, strict bool) (out []byte, err error) {
	out, _, err = e.append(dst, tpl, vars, strict)
	return out, err
}

// append is Append that also returns the increment and decrement counters, which
// liquidjs writes into the data a chain's later steps share.
func (e Engine) append(dst []byte, tpl string, vars map[string]any, strict bool) (out []byte, env map[string]any, err error) {
	out, env, err = e.run(dst, tpl, vars, strict)
	if err != nil {
		dropUnsurePath(e, tpl, err)
	}
	return out, env, err
}

// dropUnsurePath clears an ErrUndefined's Path when tpl doesn't pass Check: liquidjs parses
// the whole template first, and may report a later error instead.
func dropUnsurePath(e Engine, tpl string, err error) {
	var me *Error
	if errors.As(err, &me) && me.Path != nil && e.Check(tpl) != nil {
		me.Path = nil
	}
}

func (e Engine) run(dst []byte, tpl string, vars map[string]any, strict bool) (out []byte, env map[string]any, err error) {
	r := renderer{config: e.config(vars, strict), tpl: tpl, out: dst, outBase: len(dst)}
	defer func() { env = r.env }()
	defer r.releaseAssigns()
	defer r.recoverBail(&err)
	r.run()
	return r.out, nil, nil
}

// Check is the package-level Check with e's configuration.
func (e Engine) Check(tpl string) (err error) {
	r := renderer{config: e.config(nil, false), tpl: tpl, check: true}
	defer r.recoverBail(&err)
	r.run()
	return nil
}

func (e Engine) config(vars map[string]any, strict bool) config {
	return config{vars: vars, strict: strict, tags: e.Tags, filterFns: e.Filters, passUnknown: e.PassUnknownFilters, dialect: e.Dialect, now: e.Now, limits: e.Limits.orDefaults()}
}

// Step is one template in a RenderChain.
type Step struct {
	Body   string
	Key    []string // where the output is bound for later steps
	Strict bool
	Vars   map[string]any // replaces the chain vars for this step only
}

// Result is one step's output in a RenderChain.
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

// RenderChain is the package-level RenderChain with e's configuration.
func (e Engine) RenderChain(steps []Step, vars map[string]any) (res []Result, n int, hydrated map[string]any) {
	res = make([]Result, 0, len(steps))
	owned := false
	var cloned map[string]bool // nested maps bind has cloned, by path
	var buf []byte
	for i, st := range steps {
		v := st.Vars
		if v == nil {
			v = vars
		}
		out, env, err := e.append(buf[:0], st.Body, v, st.Strict)
		if errors.Is(err, ErrUnsupported) {
			return res, i, vars
		}
		if len(env) > 0 && st.Vars == nil {
			if !owned {
				vars, owned = maps.Clone(vars), true
				if vars == nil {
					vars = map[string]any{}
				}
			}
			maps.Copy(vars, env) // counters persist in the shared data, even when the step fails
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
		if bindPath && cloned == nil {
			cloned = map[string]bool{}
		}
		if bindPath && !bind(vars, st.Key, body, cloned, "") {
			return res[:i], i, vars
		}
		if bindContent {
			vars["content"] = body
		}
	}
	return res, len(steps), vars
}

// bind stores val at key in m, which must be owned. A nested map is cloned the first
// time a chain writes into it; cloned holds the paths already cloned.
func bind(m map[string]any, key []string, val any, cloned map[string]bool, path string) bool {
	if len(key) == 1 {
		m[key[0]] = val
		return true
	}
	child, isMap := m[key[0]].(map[string]any)
	if !isMap && m[key[0]] != nil {
		return false // JS would set a property on a scalar; not worth mirroring
	}
	path += "\x00" + key[0]
	if !cloned[path] {
		child = maps.Clone(child)
		if child == nil {
			child = map[string]any{}
		}
		m[key[0]] = child
		cloned[path] = true
	}
	return bind(child, key[1:], val, cloned, path)
}
