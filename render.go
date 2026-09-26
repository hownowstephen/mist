package mist

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const maxDepth = 16

type frameKind uint8

const (
	kIf frameKind = iota
	kUnless
	kFor
	kCapture
)

type frame struct {
	kind       frameKind
	parentLive bool
	active     bool // current branch (or loop body) executes
	taken      bool // a branch of this if/unless already executed
	sawElse    bool
	rtrim      bool // for: the for tag's -%}, reapplied on each iteration
	body       int  // for: offset just past the for tag; capture: where its output starts
	idx        int
	name       string
	coll       []any
}

type renderer struct {
	tpl       string
	out       []byte
	vars      map[string]any
	assigns   map[string]any
	strict    bool
	check     bool         // parse every branch, evaluate nothing
	undef     pendingUndef // strict undefined, raised once the current tag parses cleanly
	tags      map[string]TagFunc
	filterFns map[string]FilterFunc
	dialect   *Dialect
	now       func() time.Time
	stack     [maxDepth]frame
	depth     int

	// expression cursor: src is tpl[base:base+len(src)]
	src  string
	base int
	p    int
}

type bailout struct{ err error }

func recoverBail(err *error) {
	if e := recover(); e != nil {
		b, ok := e.(bailout)
		if !ok {
			panic(e)
		}
		*err = b.err
	}
}

func bail(pos int, format string, args ...any) {
	panic(bailout{&Error{Kind: ErrUnsupported, Pos: pos, Msg: fmt.Sprintf(format, args...)}})
}

func (r *renderer) live() bool {
	if r.check {
		return false
	}
	if r.depth == 0 {
		return true
	}
	f := &r.stack[r.depth-1]
	return f.parentLive && f.active
}

func (r *renderer) top() *frame {
	if r.depth == 0 {
		return nil
	}
	return &r.stack[r.depth-1]
}

func (r *renderer) push(f frame) {
	if r.depth == maxDepth {
		bail(r.base, "nesting deeper than %d", maxDepth)
	}
	r.stack[r.depth] = f
	r.depth++
}

func (r *renderer) run() {
	s := r.tpl
	pos, trimNext := 0, false
	for pos < len(s) {
		start, isTag := nextDelim(s, pos)
		text := s[pos:start]
		if trimNext {
			text = trimLeftBlank(text)
		}
		if start == len(s) {
			r.emit(text)
			break
		}
		b, e, next, lt, rt := delimBounds(s, start, isTag)
		if (lt || rt) && r.rejects(TrimMarkers) {
			bail(start, "trim markers are rejected by the dialect")
		}
		if lt {
			text = trimRightBlank(text)
		}
		r.emit(text)
		if isTag {
			pos, trimNext = r.tag(b, e, next, lt, rt)
		} else {
			r.output(b, e)
			pos, trimNext = next, rt
		}
	}
	if r.depth > 0 {
		bail(len(s), "unclosed block")
	}
}

func (r *renderer) emit(text string) {
	if r.live() {
		r.out = append(r.out, text...)
	}
}

// nextDelim finds the next "{{" or "{%" at or after pos, or len(s).
func nextDelim(s string, pos int) (int, bool) {
	for {
		j := strings.IndexByte(s[pos:], '{')
		if j < 0 {
			return len(s), false
		}
		j += pos
		if j+1 < len(s) {
			switch s[j+1] {
			case '{':
				return j, false
			case '%':
				return j, true
			}
		}
		pos = j + 1
	}
}

// delimBounds returns the body [b,e) of the tag or output at start, excluding
// trim markers, and the offset just past its closer.
func delimBounds(s string, start int, isTag bool) (b, e, next int, lt, rt bool) {
	b = start + 2
	if b < len(s) && s[b] == '-' {
		lt = true
		b++
	}
	if isTag {
		// liquidjs ends tags at the first %}, quotes or not
		k := strings.Index(s[b:], "%}")
		if k < 0 {
			bail(start, "unclosed tag")
		}
		e = b + k
	} else {
		e = closeOutput(s, b, start)
	}
	next = e + 2
	if e > b && s[e-1] == '-' {
		rt = true
		e--
	}
	return b, e, next, lt, rt
}

// closeOutput finds the "}}" closing an output, skipping quoted strings as liquidjs does.
func closeOutput(s string, i, start int) int {
	for i < len(s) {
		switch c := s[i]; c {
		case '"', '\'':
			k := strings.IndexByte(s[i+1:], c)
			if k < 0 || strings.ContainsRune(s[i+1:i+1+k], '\\') {
				bail(i, "unterminated or escaped string")
			}
			i += k + 2
		case '}':
			if i+1 < len(s) && s[i+1] == '}' {
				return i
			}
			i++
		default:
			i++
		}
	}
	bail(start, "unclosed output")
	return 0
}

func (r *renderer) output(b, e int) {
	r.setSrc(b, e)
	live := r.live()
	v := r.expr(live, false)
	if k := v.keyword(); k != "" {
		bail(b, "%s is only supported with == and !=", k)
	}
	if r.ws(); v.lit == 0 && v.x == nilLit && r.peek() != '|' {
		bail(b, "output of the nil literal") // see write
	}
	v = r.filters(v, live)
	r.end()
	if live {
		r.write(v)
	}
}

func (r *renderer) tag(b, e, next int, lt, rt bool) (int, bool) {
	r.setSrc(b, e)
	r.ws()
	name := r.ident()
	live := r.live()
	switch name {
	case "if", "unless":
		c := r.cond(live)
		if name == "unless" {
			c = !c
		}
		k := kIf
		if name == "unless" {
			k = kUnless
		}
		r.push(frame{kind: k, parentLive: live, active: c, taken: c})
	case "elsif":
		f := r.top()
		if f == nil || f.kind >= kFor || f.sawElse {
			bail(b, "unexpected elsif")
		}
		eval := f.parentLive && !f.taken && !r.check
		f.active = r.cond(eval)
		f.taken = f.taken || f.active
	case "else":
		r.end()
		f := r.top()
		if f == nil || f.kind >= kFor || f.sawElse {
			bail(b, "unexpected else")
		}
		f.sawElse, f.active, f.taken = true, !f.taken, true
	case "endif", "endunless":
		r.end()
		want := kIf
		if name == "endunless" {
			want = kUnless
		}
		if f := r.top(); f == nil || f.kind != want {
			bail(b, "unexpected %s", name)
		}
		r.depth--
	case "for":
		r.ws()
		v := r.ident()
		r.ws()
		if v == "" || r.ident() != "in" {
			bail(b, "expected: for <ident> in <path>")
		}
		r.ws()
		if !isIdentStart(r.peek()) {
			bail(r.base+r.p, "for collection must be a variable path")
		}
		coll := r.path(live, false)
		r.end()
		f := frame{kind: kFor, parentLive: live, body: next, rtrim: rt, name: v}
		if live {
			switch c := coll.(type) {
			case []any:
				f.coll = c
			case nil, undefinedT:
			default:
				bail(b, "for over non-array %T", coll)
			}
		}
		f.active = len(f.coll) > 0
		r.push(f)
	case "endfor":
		r.end()
		f := r.top()
		if f == nil || f.kind != kFor {
			bail(b, "unexpected endfor")
		}
		if f.active && !r.check && f.idx+1 < len(f.coll) {
			f.idx++
			return f.body, f.rtrim
		}
		r.depth--
	case "capture":
		r.ws()
		var v string
		if c := r.peek(); c == '"' || c == '\'' {
			v = r.str()
		} else if v = r.ident(); v == "" || reserved(v) {
			bail(b, "expected: capture <ident>")
		}
		r.end()
		r.push(frame{kind: kCapture, parentLive: live, active: true, body: len(r.out), name: v})
	case "endcapture":
		r.end()
		f := r.top()
		if f == nil || f.kind != kCapture {
			bail(b, "unexpected endcapture")
		}
		r.depth--
		if f.parentLive {
			r.setAssign(f.name, string(r.out[f.body:]))
			r.out = r.out[:f.body]
		}
	case "assign":
		r.ws()
		v := r.ident()
		r.ws()
		if v == "" || reserved(v) || r.peek() != '=' {
			bail(b, "expected: assign <ident> = <expr>")
		}
		r.p++
		val := r.expr(live, true)
		if k := val.keyword(); k != "" {
			bail(b, "%s is only supported with == and !=", k)
		}
		val = r.filters(val, live)
		r.end()
		if live {
			if isNil(val) {
				bail(b, "assigning nil") // liquidjs stores distinct null-ish values per source
			}
			r.setAssign(v, val.any())
		}
	case "comment":
		r.end()
		return r.skipComment(next)
	case "raw":
		r.end()
		if r.rejects(RawBlocks) {
			bail(b, "raw blocks are rejected by the dialect")
		}
		if lt || rt {
			bail(b, "trim markers on raw")
		}
		return r.raw(next, live), false
	default:
		fn, ok := r.tags[name]
		if !ok {
			bail(b, "unsupported tag %q", name)
		}
		if live {
			r.ws()
			r.callTag(fn, name, strings.TrimSpace(r.src[r.p:]), b)
		}
	}
	return next, rt
}

func (r *renderer) setAssign(name string, x any) {
	if r.assigns == nil {
		r.assigns = map[string]any{}
	}
	r.assigns[name] = x
}

func (r *renderer) callTag(fn TagFunc, name, args string, pos int) {
	if fn == nil {
		bail(pos, "tag %q is registered without a function", name) // e.g. for Check only
	}
	t := Tag{Name: name, Args: args, Vars: r.vars, Strict: r.strict, assigns: r.assigns, frames: slices.Clone(r.stack[:r.depth])}
	out, err := fn(r.out, t)
	if err != nil {
		panic(bailout{wrapErr("tag", name, pos, err)})
	}
	r.out = out
}

func wrapErr(kind, name string, pos int, err error) error {
	return fmt.Errorf("mist: %s %q at offset %d: %w", kind, name, pos, err)
}

// skipComment tokenizes (but ignores) everything up to endcomment, as liquidjs does.
func (r *renderer) skipComment(pos int) (int, bool) {
	s := r.tpl
	for {
		start, isTag := nextDelim(s, pos)
		if start == len(s) {
			bail(pos, "unclosed comment")
		}
		b, e, next, lt, rt := delimBounds(s, start, isTag)
		if (lt || rt) && r.rejects(TrimMarkers) {
			bail(start, "trim markers are rejected by the dialect")
		}
		pos = next
		if !isTag {
			continue
		}
		r.setSrc(b, e)
		r.ws()
		switch r.ident() {
		case "":
			bail(b, "tag without a name inside comment")
		case "endcomment":
			r.end()
			return next, rt
		case "comment", "raw":
			bail(b, "comment or raw inside comment")
		}
	}
}

func (r *renderer) raw(pos int, live bool) int {
	s := r.tpl
	for i := pos; ; {
		k := strings.Index(s[i:], "{%")
		if k < 0 {
			bail(pos, "unclosed raw")
		}
		k += i
		j := skipBlank(s, k+2)
		if strings.HasPrefix(s[j:], "endraw") {
			j = skipBlank(s, j+6)
			if !strings.HasPrefix(s[j:], "%}") || s[k+2] == '-' {
				bail(k, "malformed endraw")
			}
			if live {
				r.out = append(r.out, s[pos:k]...)
			}
			return j + 2
		}
		i = k + 2
	}
}

func skipBlank(s string, i int) int {
	for i < len(s) && isBlank(s[i]) {
		i++
	}
	return i
}

func isBlank(c byte) bool { return c == ' ' || (c >= '\t' && c <= '\r') }

// isTrimBlank matches liquidjs's BLANK character class, which whitespace control trims greedily.
func isTrimBlank(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r',
		0xA0, 0x1680, 0x180E, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
		return true
	}
	return c >= 0x2000 && c <= 0x200A
}

func trimLeftBlank(s string) string {
	for len(s) > 0 {
		c, n := utf8.DecodeRuneInString(s)
		if !isTrimBlank(c) {
			break
		}
		s = s[n:]
	}
	return s
}

func trimRightBlank(s string) string {
	for len(s) > 0 {
		c, n := utf8.DecodeLastRuneInString(s)
		if !isTrimBlank(c) {
			break
		}
		s = s[:len(s)-n]
	}
	return s
}
