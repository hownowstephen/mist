package mist

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const maxDepth = 16

// ponytail: a flat budget per render; make it an Engine option if callers need more.
const maxIterations = 1_000_000

type frameKind uint8

const (
	kIf frameKind = iota
	kUnless
	kFor
	kCapture
	kCase
)

// frame is kept small because the renderer zeroes all maxDepth of them on every render;
// fields are shared between kinds where they can be.
type frame struct {
	kind       frameKind
	lit        uint8 // case: the subject's lit
	parentLive bool
	active     bool // current branch (or loop body) executes
	taken      bool // a branch of this if/unless already executed, or a when of this case matched
	sawElse    bool
	rtrim      bool  // for: the for tag's -%}, reapplied on each iteration
	rev        bool  // for: iterate coll from the end (reversed)
	idx        int32 // for: the current item
	collStart  int32 // for: the collection as written in the template, for forloop.name
	collEnd    int32
	body       int    // for: offset just past the for tag; capture: where its output starts
	name       string // for: the loop variable; capture: the target; case: the subject's s
	coll       []any
	x          any     // case: the subject's x
	n          float64 // case: the subject's n
}

func (f *frame) subject() val { return val{x: f.x, s: f.name, n: f.n, lit: f.lit} }

// looping reports whether f is a for loop still iterating, not in its else part, which
// has no loop variable or forloop and where break and continue reach the enclosing loop.
func (f *frame) looping() bool { return f.kind == kFor && !f.sawElse }

type renderer struct {
	tpl          string
	out          []byte
	vars         map[string]any
	assigns      map[string]any
	strict       bool
	check        bool         // parse every branch, evaluate nothing
	undef        pendingUndef // strict undefined, raised once the current tag parses cleanly
	tags         map[string]TagFunc
	filterFns    map[string]FilterFunc
	passUnknown  bool // Engine.PassUnknownFilters
	dialect      *Dialect
	now          func() time.Time
	stack        [maxDepth]frame
	depth        int
	iters        int            // loop iterations so far, against maxIterations
	halt         int            // 1 + the stack index of the for a break or continue is unwinding to, or 0
	haltBreak    bool           // the halt is a break, not a continue
	cycles       map[string]int // cycle positions by liquidjs's fingerprint
	binds        []binding      // expression filter items, innermost last
	lenientUndef bool           // a lenient path met an undefined variable under strict
	env          map[string]any // increment and decrement counters, which liquidjs keeps in the data scope

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
	if r.check || r.halt != 0 {
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
	if len(s) > math.MaxInt32 {
		bail(0, "template over 2 GiB") // frames keep int32 offsets
	}
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
			if i = quotedEnd(s, i); i < 0 {
				bail(start, "unterminated string")
			}
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
	if r.peek() == '#' {
		r.inlineComment(b)
		return next, rt
	}
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
		eval := f.parentLive && !f.taken && !r.check && r.halt == 0
		f.active = r.cond(eval)
		f.taken = f.taken || f.active
	case "else":
		r.end()
		f := r.top()
		if f == nil || f.kind == kCapture || f.sawElse {
			bail(b, "unexpected else")
		}
		if f.kind == kFor {
			// The else ends the loop body: iterate again, or move to the else part, which
			// renders only when the collection was empty before limit, offset and reversed.
			if again := r.endIteration(b); again {
				return f.body, f.rtrim
			}
			f.sawElse, f.active = true, f.taken
			break
		}
		f.sawElse, f.active, f.taken = true, !f.taken, true
	case "case":
		if r.dialect != nil && r.dialect.Compare != nil {
			bail(b, "case with a dialect Compare hook")
		}
		v := r.expr(live, true) // lenientIf, as for conditions
		if k := v.keyword(); k != "" {
			bail(b, "%s is only supported with == and !=", k)
		}
		v = r.filters(v, live)
		r.end()
		if v.lit == 0 && v.x == nilLit {
			v = val{} // liquidjs unwraps the nil Drop to null, which no longer matches undefined
		}
		// Until the first when, the body is parsed but never rendered.
		r.push(frame{kind: kCase, parentLive: live, x: v.x, name: v.s, n: v.n, lit: v.lit})
	case "when":
		f := r.top()
		if f == nil || f.kind != kCase || f.sawElse {
			bail(b, "unexpected when") // liquidjs ignores a when after else
		}
		// Every matching when renders, but each stops evaluating at its first matching value.
		eval, match := f.parentLive && !r.check && r.halt == 0, false
		for {
			at := r.pos()
			v := r.expr(eval && !match, true)
			match = match || eval && eq(f.subject(), v, at)
			r.ws()
			if r.p == len(r.src) {
				break
			}
			if r.peek() == ',' {
				r.p++
			} else if w := r.ident(); w != "or" {
				bail(r.pos(), "expected , or or between when values") // liquidjs skips to the next "or"
			}
		}
		r.end()
		f.active = match
		f.taken = f.taken || match
	case "endcase":
		r.end()
		if f := r.top(); f == nil || f.kind != kCase {
			bail(b, "unexpected endcase")
		}
		r.depth--
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
		if c := r.peek(); !isIdentStart(c) && c != '(' {
			bail(r.base+r.p, "for collection must be a variable path or a range")
		}
		cs := r.p
		coll := r.expr(live, false).x
		f := frame{kind: kFor, parentLive: live, body: next, rtrim: rt, name: v, collStart: int32(r.base + cs), collEnd: int32(r.pos())}
		mods := r.forParams(live)
		r.end()
		if live {
			switch c := coll.(type) {
			case []any:
				f.coll = mods.apply(c)
				f.taken = len(c) == 0 // for its else part
			case nil, undefinedT:
				f.taken = true
			default:
				bail(b, "for over non-array %T", coll)
			}
		}
		f.rev = mods.rev
		f.active = len(f.coll) > 0
		r.push(f)
	case "break", "continue":
		r.end()
		i := r.depth - 1
		for i >= 0 && !r.stack[i].looping() {
			i--
		}
		if i < 0 {
			bail(b, "%s outside a for loop", name) // liquidjs stops rendering the template
		}
		if r.live() {
			// Everything up to the loop's endfor is dead; a capture still assigns what it has, as in liquidjs.
			r.halt, r.haltBreak = i+1, name == "break"
		}
	case "endfor":
		r.end()
		f := r.top()
		if f == nil || f.kind != kFor {
			bail(b, "unexpected endfor")
		}
		if !f.sawElse && r.endIteration(b) {
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
	case "cycle":
		r.cycle(live, b)
	case "echo":
		r.ws()
		if r.p == len(r.src) {
			break // liquidjs's echo of nothing
		}
		v := r.expr(live, false)
		if k := v.keyword(); k != "" {
			bail(b, "%s is only supported with == and !=", k)
		}
		v = r.filters(v, live)
		r.end()
		if live {
			if _, isStr := v.str(); !isStr && !isNil(v) && r.dialect != nil && r.dialect.Output != nil {
				bail(b, "echo of a non-string with a dialect Output hook") // liquidjs's echo skips outputEscape
			}
			r.write(v)
		}
	case "increment", "decrement":
		r.ws()
		v := r.ident()
		r.end()
		if v == "" {
			bail(b, "expected: %s <ident>", name)
		}
		if live {
			r.counter(v, name == "increment")
		}
	case "liquid":
		if lt || rt {
			bail(b, "trim markers on liquid") // they'd apply to the text around the tag, as usual, but keep it simple
		}
		r.liquid(r.base+r.p, e)
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

// cycle is liquidjs's cycle: [ group ":" ] value { "," value }, writing the next value
// unless it's falsy in JavaScript. liquidjs keys the position by the group and the
// values' tokens, which stringify as [object Object], so cycles with the same group
// and number of values share one.
func (r *renderer) cycle(live bool, at int) {
	r.ws()
	start := r.p
	r.cycleValue(false)
	r.ws()
	fp := "cycle:undefined:"
	if r.peek() == ':' {
		r.p = start
		g := r.cycleValue(live)
		r.ws()
		r.p++
		if live {
			fp = "cycle:" + jsString(g, at) + ":"
		}
		start = r.p
	}
	r.p = start
	n := 0
	for {
		r.cycleValue(false)
		n++
		r.ws()
		if r.p == len(r.src) {
			break
		}
		if r.peek() != ',' {
			bail(r.pos(), "expected , between cycle values")
		}
		r.p++
	}
	if !live {
		return
	}
	fp += strings.Repeat("[object Object],", n-1) + "[object Object]"
	if r.cycles == nil {
		r.cycles = map[string]int{}
	}
	idx := r.cycles[fp]
	r.cycles[fp] = (idx + 1) % n
	r.p = start
	var v val
	for i := range n {
		if i > 0 {
			r.ws()
			r.p++
		}
		if c := r.cycleValue(i == idx); i == idx {
			v = c
		}
	}
	r.end()
	if jsTruthy(v, at) {
		r.out = stringify(r.out, v, at)
	}
}

func (r *renderer) cycleValue(eval bool) val {
	at := r.pos()
	v := r.expr(eval, false)
	if v.keyword() != "" || v.lit == 0 && v.x == nilLit {
		bail(at, "cycle value %s", r.src[at-r.base:r.p])
	}
	return v
}

// forParams parses liquidjs's for modifiers. They apply as offset, then limit, then
// reversed, whatever order they're written in.
func (r *renderer) forParams(eval bool) (p forMods) {
	for {
		r.ws()
		if r.p == len(r.src) {
			return p
		}
		if r.peek() == ',' {
			r.p++
			r.ws()
		}
		at := r.pos()
		name := r.ident()
		r.ws()
		if name == "reversed" && !p.rev {
			if r.peek() == ':' {
				bail(at, "reversed with a value") // liquidjs reverses whatever the value
			}
			p.rev = true
			continue
		}
		if name != "offset" && name != "limit" || r.peek() != ':' || name == "offset" && p.hasOffset || name == "limit" && p.hasLimit {
			bail(at, "for parameters other than one each of limit, offset and reversed")
		}
		r.p++
		v := r.expr(eval, false)
		n := 0
		if eval {
			f, ok := v.check(at).num(at)
			if !ok || f != math.Trunc(f) || math.Abs(f) >= maxSafeInt {
				bail(at, "for %s that isn't an integer", name)
			}
			n = int(f)
		}
		if name == "offset" {
			p.offset, p.hasOffset = n, true
		} else {
			p.limit, p.hasLimit = n, true
		}
	}
}

type forMods struct {
	offset, limit       int
	hasOffset, hasLimit bool
	rev                 bool
}

// apply is liquidjs's arr.slice(offset), then .slice(0, limit); reversal happens as the loop iterates.
func (m forMods) apply(a []any) []any {
	if m.hasOffset {
		a = a[sliceIndex(m.offset, len(a)):]
	}
	if m.hasLimit {
		a = a[:sliceIndex(m.limit, len(a))]
	}
	return a
}

// sliceIndex resolves an Array.prototype.slice bound: negative counts from the end.
func sliceIndex(i, n int) int {
	if i < 0 {
		i += n
	}
	return max(0, min(i, n))
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
	t := Tag{Name: name, Args: args, Vars: r.vars, Strict: r.strict, assigns: r.assigns, env: r.env, frames: slices.Clone(r.stack[:r.depth])}
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

// endIteration ends one pass over a for loop's body at an else or endfor, reporting
// whether the loop runs again from its body.
func (r *renderer) endIteration(at int) bool {
	f := r.top()
	if r.halt == r.depth {
		r.halt = 0
		if r.haltBreak {
			return false
		}
	}
	if f.active && !r.check && int(f.idx)+1 < len(f.coll) {
		if r.iters++; r.iters > maxIterations {
			bail(at, "more than %d loop iterations", maxIterations) // ranges make loop counts template-controlled
		}
		f.idx++
		return true
	}
	return false
}

// inlineComment validates {% # … %}: liquidjs requires every line to start with #.
func (r *renderer) inlineComment(at int) {
	rest := r.src[r.p:]
	for i := strings.IndexByte(rest, '\n'); i >= 0; i = strings.IndexByte(rest, '\n') {
		rest = strings.TrimLeftFunc(rest[i+1:], func(c rune) bool { return c != '\n' && jsSpace(c) })
		if rest != "" && rest[0] != '#' && rest[0] != '\n' {
			bail(at, "an inline comment line that doesn't start with #") // liquidjs errors
		}
	}
}

// counter is liquidjs's increment (print, then add one) and decrement (subtract one,
// then print). Counters live in the data scope: they start from a numeric variable of
// the same name, and assigns and loop variables shadow them.
func (r *renderer) counter(name string, inc bool) {
	cur, ok := r.env[name]
	if !ok {
		cur = r.vars[name]
	}
	n, isNum := 0.0, false
	switch cur.(type) {
	case nil, bool, string, map[string]any, []any:
	default:
		n, isNum = num(cur, r.base)
	}
	if !isNum {
		n = 0
	}
	if !inc {
		n--
	}
	if r.env == nil {
		r.env = map[string]any{}
	}
	r.out = appendJSNumber(r.out, n)
	if inc {
		n++
	}
	r.env[name] = n
}

// liquid runs the {% liquid %} tag's lines, each a tag without delimiters, from start
// to end. Blocks opened in it must close in it, as liquidjs parses it on its own.
func (r *renderer) liquid(start, end int) {
	depth := r.depth
	for pos := start; pos < end; {
		eol := strings.IndexByte(r.tpl[pos:end], '\n')
		next := end
		if eol >= 0 {
			eol += pos
			next = eol + 1
		} else {
			eol = end
		}
		b := pos + len(r.tpl[pos:eol]) - len(strings.TrimLeftFunc(r.tpl[pos:eol], isTrimBlank))
		e := b + len(strings.TrimRightFunc(r.tpl[b:eol], isTrimBlank))
		if b == e {
			pos = next
			continue
		}
		r.setSrc(b, e)
		if r.peek() != '#' {
			switch name := r.ident(); name {
			case "comment", "raw", "liquid":
				bail(b, "%s inside a liquid tag", name)
			}
		}
		pos, _ = r.tag(b, e, next, false, false)
	}
	if r.depth != depth {
		bail(end, "a block left open in a liquid tag")
	}
}
