package mist

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func (r *renderer) setSrc(b, e int) { r.src, r.base, r.p = r.tpl[b:e], b, 0 }

func (r *renderer) pos() int { return r.base + r.p }

func (r *renderer) peek() byte {
	if r.p < len(r.src) {
		return r.src[r.p]
	}
	return 0
}

// ws skips ASCII whitespace and U+00A0, both in liquidjs's BLANK class.
// ponytail: its other Unicode blanks bail; decoding them here would stop ws inlining.
func (r *renderer) ws() {
	s, i := r.src, r.p
	for i < len(s) {
		if c := s[i]; isBlank(c) {
			i++
		} else if c == 0xC2 && i+1 < len(s) && s[i+1] == 0xA0 {
			i += 2
		} else {
			break
		}
	}
	r.p = i
}

func (r *renderer) end() {
	r.ws()
	if r.p < len(r.src) {
		bail(r.pos(), "unexpected-token", "unexpected %q", r.src[r.p:])
	}
	if r.undef.set {
		panic(bailout{&Error{Kind: ErrUndefined, Code: "undefined", Pos: r.undef.pos, Msg: r.undef.path}})
	}
}

func isIdentStart(c byte) bool { return identByte[c] == 2 }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentChar(c byte) bool { return identByte[c] != 0 }

// identByte is 2 for bytes that start an identifier and 1 for the rest of liquidjs's
// ASCII word characters (digits, '-' and '?').
var identByte = func() (t [256]uint8) {
	for c := range 256 {
		switch {
		case c == '_', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			t[c] = 2
		case c >= '0' && c <= '9', c == '-', c == '?':
			t[c] = 1
		}
	}
	return t
}()

// ident = ( letter | "_" ) { letter | digit | "_" | "-" }
func (r *renderer) ident() string {
	i := r.p
	if i >= len(r.src) || !isIdentStart(r.src[i]) {
		return ""
	}
	for i < len(r.src) && isIdentChar(r.src[i]) {
		i++
	}
	s := r.src[r.p:i]
	r.p = i
	return s
}

// cond = cmp { "and" cmp } | cmp { "or" cmp }
// Returns false when not evaluating. Undefined variables are lenient (lenientIf).
func (r *renderer) cond(eval bool) bool {
	res, v, lone := r.chain(eval)
	if r.peek() != '|' {
		return res
	}
	if k := v.keyword(); lone && k != "" {
		bail(r.pos(), "keyword-outside-equality", "%s is only supported in conditions and output", k)
	}
	// liquidjs filters the whole condition's value: a lone operand's, else the boolean.
	if !lone {
		v = val{x: res}
	}
	at := r.pos()
	v = r.filters(v, eval)
	r.end()
	return eval && truthy(v, at)
}

// chain = cmp [ ( "and" | "or" ) chain ]. The recursion groups right to left, as
// liquidjs does: a and b or c is a and (b or c).
func (r *renderer) chain(eval bool) (res bool, first val, lone bool) {
	res, first, lone = r.cmp(eval)
	r.ws()
	if r.p == len(r.src) || r.peek() == '|' {
		return res, first, lone
	}
	at := r.pos()
	w := r.ident()
	if w != "and" && w != "or" {
		bail(at, "expected-and-or", "expected and/or, got %q", r.src[at-r.base:])
	}
	rest, _, _ := r.chain(eval)
	if w == "and" {
		return res && rest, first, false
	}
	return res || rest, first, false
}

// cmp = expr [ op expr ]
func (r *renderer) cmp(eval bool) (bool, val, bool) {
	start := r.pos()
	a := r.expr(eval, true)
	before := r.p
	r.ws()
	spaced := r.p > before
	op := r.op()
	if op != "" && !spaced && r.rejects(UnspacedOperators) {
		bail(r.pos(), "dialect-unspaced-operator", "operators without whitespace before them are rejected by the dialect")
	}
	if op == "" {
		return eval && truthy(a, r.pos()), a, true
	}
	at := r.pos()
	b := r.expr(eval, true)
	if k, other := a.keyword(), b; k != "" || b.keyword() != "" {
		if k == "" {
			k, other = b.keyword(), a
		}
		switch {
		case op == "contains":
			bail(start, "keyword-outside-equality", "%s is only supported with ==, !=, <, >, <= and >=", k)
		case op != "==" && op != "!=":
			if r.dialect == nil || r.dialect.Compare == nil {
				return false, a, false // liquidjs's EmptyDrop orders before and after nothing
			}
		case other.keyword() != "" || (other.lit == 0 && other.x == nilLit):
			bail(start, "keyword-against-keyword", "%s compared with blank, empty or nil", k) // liquidjs is asymmetric here
		}
	}
	if op == "contains" {
		if b.lit == 0 && b.x == nilLit {
			bail(at, "contains-nil", "contains nil") // liquidjs searches for "null"
		}
		if r.dialect != nil && r.dialect.Compare != nil {
			bail(at, "dialect-contains", "contains with a dialect Compare hook")
		}
		return eval && contains(a, b, at), a, false
	}
	if eval && r.dialect != nil && r.dialect.Compare != nil {
		return r.dialectCompare(op, a, b, at), a, false
	}
	return eval && compare(op, a, b, at), a, false
}

func (r *renderer) op() string {
	rest := r.src[r.p:]
	for _, op := range [...]string{"==", "!=", "<=", ">=", "<", ">", "contains"} {
		if strings.HasPrefix(rest, op) {
			if op == "contains" && len(rest) > len(op) && isIdentChar(rest[len(op)]) {
				bail(r.pos(), "contains-glued", "contains followed by an identifier character") // liquidjs splits containsx into contains x
			}
			r.p += len(op)
			return op
		}
	}
	return ""
}

// expr = path | string | int | "true" | "false" | "nil"
func (r *renderer) expr(eval, lenient bool) val {
	r.ws()
	at := r.pos()
	switch c := r.peek(); {
	case c == '"' || c == '\'':
		return val{s: r.str(), lit: litStr}
	case c == '-' || c == '+' || isDigit(c):
		return val{n: r.number(), lit: litNum}
	case c == '(':
		return val{x: r.rangeLit(eval)}
	case isIdentStart(c):
		save := r.p
		var v any
		switch r.ident() {
		case "true":
			v = true
		case "false":
			v = false
		case "nil", "null":
			v = nilLit
		case "blank":
			if r.rejects(BlankKeyword) {
				bail(at, "dialect-blank", "blank is rejected by the dialect")
			}
			v = blankLit
		case "empty":
			if r.rejects(EmptyKeyword) {
				bail(at, "dialect-empty", "empty is rejected by the dialect")
			}
			v = emptyLit
		default:
			r.p = save
			return val{x: r.path(eval, lenient)}
		}
		if c := r.peek(); c == '.' || c == '[' {
			bail(at, "literal-property", "property access on a literal")
		}
		return val{x: v}
	}
	bail(at, "expected-expression", "expected expression")
	return val{}
}

func (r *renderer) str() string {
	at := r.pos()
	end := quotedEnd(r.src, r.p)
	if end < 0 {
		bail(at, "unterminated-string", "unterminated string")
	}
	s := r.src[r.p+1 : end-1]
	r.p = end
	if strings.IndexByte(s, '\\') < 0 {
		return s
	}
	return unescape(s, at)
}

// quotedEnd returns the index just past the quote closing the string at s[i], skipping
// backslash escapes as liquidjs's readQuoted does, or -1.
func quotedEnd(s string, i int) int {
	k := strings.IndexByte(s[i+1:], s[i])
	if k < 0 {
		return -1
	}
	if strings.IndexByte(s[i+1:i+1+k], '\\') < 0 {
		return i + k + 2
	}
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case s[i]:
			return j + 1
		}
	}
	return -1
}

// unescape is liquidjs's parseStringLiteral: \b \f \n \r \t \v, \u with up to 4 hex
// digits, up to 3 octal digits, and any other escaped character as itself.
func unescape(s string, at int) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if k := strings.IndexByte("bfnrtv", s[i]); k >= 0 {
			b.WriteByte("\b\f\n\r\t\v"[k])
			continue
		}
		base, maxDigits, j := 8, 3, i
		switch {
		case s[i] == 'u':
			base, maxDigits, j = 16, 4, i+1
		case s[i] < '0' || s[i] > '7':
			_, n := utf8.DecodeRuneInString(s[i:])
			b.WriteString(s[i : i+n])
			i += n - 1
			continue
		}
		c, k := rune(0), j
		for ; k < len(s) && k < j+maxDigits; k++ {
			d := strings.IndexByte("0123456789abcdef", s[k]|0x20)
			if d < 0 || d >= base {
				break
			}
			c = c*rune(base) + rune(d)
		}
		if utf16.IsSurrogate(c) {
			bail(at, "string-escape-surrogate", "string escape of half a surrogate pair")
		}
		b.WriteRune(c)
		i = k - 1
	}
	return b.String()
}

// number = [ "-" | "+" ] digit { digit } [ "." { digit } ]
func (r *renderer) number() float64 {
	at, i := r.p, r.p
	if c := r.src[i]; c == '-' || c == '+' {
		if c == '-' && r.rejects(NegativeLiterals) {
			bail(r.base+at, "dialect-negative-literal", "negative literals are rejected by the dialect")
		}
		i++
	}
	j := i
	for j < len(r.src) && isDigit(r.src[j]) {
		j++
	}
	if j > i && j+1 < len(r.src) && r.src[j] == '.' && r.src[j+1] != '.' || j > i && j+1 == len(r.src) && r.src[j] == '.' {
		j++
		for j < len(r.src) && isDigit(r.src[j]) {
			j++
		}
	}
	rangeDots := strings.HasPrefix(r.src[j:], "..")
	if j == i || j < len(r.src) && !rangeDots && (r.src[j] == '.' || isIdentChar(r.src[j]) || r.src[j] >= utf8.RuneSelf && !r.blankAt(j)) {
		bail(r.base+at, "malformed-number", "malformed number literal")
	}
	f, err := strconv.ParseFloat(r.src[at:j], 64)
	if err != nil {
		bail(r.base+at, "malformed-number", "malformed number literal")
	}
	r.p = j
	return f
}

func (r *renderer) blankAt(i int) bool {
	return strings.HasPrefix(r.src[i:], "\u00a0")
}

// path = ident { "." ident | "[" int "]" | "[" string "]" }
func (r *renderer) path(eval, lenient bool) any {
	start := r.p
	name := r.ident()
	var v any
	switch {
	case name == "forloop":
		v = r.forloop(eval, start)
	case reserved(name):
		bail(r.base+start, "reserved-word-variable", "%q is not supported as a variable", name)
	case eval:
		v = r.root(name, start)
	}
	for {
		if eval && r.strict && v == (Undefined{}) {
			if lenient {
				v = nil // liquidjs catches the strict error and substitutes null
				r.lenientUndef = true
			} else if !r.undef.set {
				// Raised by end(): a trailing filter such as `| default` makes liquidjs lenient.
				r.undef = pendingUndef{true, r.base + start, r.src[start:r.p]}
			}
		}
		switch r.peek() {
		case '.':
			if strings.HasPrefix(r.src[r.p:], "..") {
				return v // a range's dots end the path, as in liquidjs
			}
			r.p++
			r.ws() // liquidjs skips blanks before a property name
			at := r.pos()
			src, i, j := r.src, r.p, r.p
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			r.p = j
			seg := src[i:j]
			if seg == "" || j < len(src) && src[j] >= utf8.RuneSelf && !r.blankAt(j) {
				bail(at, "expected-property-name", "expected property name")
			}
			if eval {
				v = key(v, val{s: seg, lit: litStr}, at)
			}
		case '[':
			r.p++
			r.ws()
			at := r.pos()
			k := r.expr(eval, false)
			if k.keyword() != "" {
				bail(at, "keyword-index", "%s as an index", k.keyword())
			}
			if _, undef := k.x.(Undefined); eval && r.strict && undef && k.lit == 0 {
				// liquidjs evaluates index keys strictly, even where the path itself is lenient.
				panic(bailout{&Error{Kind: ErrUndefined, Code: "undefined", Pos: at, Msg: r.src[at-r.base : r.p]}})
			}
			r.ws()
			if r.peek() != ']' {
				bail(r.pos(), "expected-bracket", "expected ]")
			}
			if eval {
				v = key(v, k, at)
			}
			r.p++
		default:
			if r.undef.set && r.undef.pos == r.base+start {
				r.undef.path = r.src[start:r.p] // name the whole path, not just the undefined prefix
			}
			return v
		}
	}
}

func (r *renderer) root(name string, at int) any {
	for i := len(r.binds) - 1; i >= 0; i-- {
		if r.binds[i].name == name {
			return r.binds[i].item
		}
	}
	for i := r.depth - 1; i >= 0; i-- {
		if f := r.frame(i); f.looping() && f.active && f.name == name {
			return f.item()
		}
	}
	if v, ok := r.assigns[name]; ok {
		return v
	}
	if v, ok := r.env[name]; ok {
		return v
	}
	if v, ok := r.vars[name]; ok {
		return v
	}
	if magic(name) {
		bail(r.base+at, "builtin-variable-name", "%q resolves to a liquidjs built-in", name)
	}
	return Undefined{}
}

// binding is an expression filter's item, which shadows every other variable of its name.
type binding struct {
	name string
	item any
}

// expValue evaluates exp as liquidjs's Value does for where_exp and the like (a condition's
// value, as in filtered conditions, then its filters) with name bound to item. Under strict,
// undefined variables throw unless the first filter is default.
func (r *renderer) expValue(eval bool, exp, name string, item any, at int) val {
	src, base, p, undef, hit := r.src, r.base, r.p, r.undef, r.lenientUndef
	r.src, r.base, r.p, r.undef, r.lenientUndef = exp, at, 0, pendingUndef{}, false
	r.binds = append(r.binds, binding{name, item})
	res, v, lone := r.chain(eval)
	if k := v.keyword(); lone && k != "" {
		bail(at, "keyword-outside-equality", "%s is only supported in conditions and output", k)
	}
	if !lone {
		v = val{x: res}
	}
	lenient := false
	if r.peek() == '|' {
		save := r.p
		r.p++
		r.ws()
		lenient = r.ident() == "default"
		r.p = save
		v = r.filters(v, eval)
	}
	r.end()
	if r.lenientUndef && !lenient {
		panic(bailout{&Error{Kind: ErrUndefined, Code: "undefined", Pos: at, Msg: exp}})
	}
	r.binds = r.binds[:len(r.binds)-1]
	r.src, r.base, r.p, r.undef, r.lenientUndef = src, base, p, undef, hit
	return v
}

func literal(name string) bool {
	switch name {
	case "true", "false", "nil", "null", "empty", "blank":
		return true
	}
	return false
}

// pendingUndef is a strict undefined variable not yet raised. It's kept unallocated
// because a leading default filter can still make it lenient.
type pendingUndef struct {
	set  bool
	pos  int
	path string
}

// reserved words that liquidjs parses as operators even where a variable is expected.
func reserved(name string) bool {
	return name == "contains" || name == "and" || name == "or" || name == "not"
}

// magic keys that liquidjs computes when the property is absent.
func magic(key string) bool { return key == "size" || key == "first" || key == "last" }

// rangeLit = "(" ws expr ws ".." ws expr ws ")" with integer bounds, liquidjs's range(+lo, +hi + 1).
func (r *renderer) rangeLit(eval bool) any {
	at := r.pos()
	r.p++
	lo := r.expr(eval, false)
	r.ws()
	if !strings.HasPrefix(r.src[r.p:], "..") {
		bail(r.pos(), "range-syntax", "expected .. in range")
	}
	r.p += 2
	hi := r.expr(eval, false)
	r.ws()
	if r.peek() != ')' {
		bail(r.pos(), "range-syntax", "expected ) after range")
	}
	r.p++
	if !eval {
		return nil
	}
	a, b := rangeBound(lo, at), rangeBound(hi, at)
	if b-a >= maxRange {
		bail(at, "range-too-long", "range of more than %d items", maxRange)
	}
	out := make([]any, 0, max(0, b-a+1))
	for i := a; i <= b; i++ {
		out = append(out, boxInt(i))
	}
	return out
}

const maxRange = 100000

// rangeBound is an integer range bound; liquidjs's unary + reads other values differently from toNumber.
func rangeBound(v val, at int) int {
	f, ok := v.check(at).num(at)
	if !ok || f != math.Trunc(f) || math.Abs(f) >= maxSafeInt {
		bail(at, "range-bound", "range bound that isn't an integer")
	}
	return int(f)
}

func (f *frame) item() any {
	if f.rev {
		return f.coll[len(f.coll)-1-int(f.idx)]
	}
	return f.coll[int(f.idx)]
}

// forloop resolves forloop.<prop> against the innermost for, as liquidjs's ForloopDrop.
func (r *renderer) forloop(eval bool, start int) any {
	f := r.loop()
	if f == nil || r.peek() != '.' {
		bail(r.base+start, "forloop-outside-for", "forloop outside a for loop or without a property")
	}
	r.p++
	at := r.pos()
	p := r.ident()
	if p == "" {
		bail(at, "expected-property-name", "expected property name")
	}
	if !eval {
		return nil
	}
	n, i := len(f.coll), int(f.idx)
	var v any
	switch p {
	case "index":
		v = boxInt(i + 1)
	case "index0":
		v = boxInt(i)
	case "rindex":
		v = boxInt(n - i)
	case "rindex0":
		v = boxInt(n - i - 1)
	case "first":
		v = i == 0
	case "last":
		v = i == n-1
	case "length":
		v = boxInt(n)
	case "name":
		v = f.name + "-" + r.tpl[f.collStart:f.collEnd]
	default:
		v = Undefined{} // liquidjs 10.26 has no parentloop or other properties
	}
	return v
}

// loop is the innermost for frame, or nil.
func (r *renderer) loop() *frame {
	for i := r.depth - 1; i >= 0; i-- {
		if f := r.frame(i); f.looping() {
			return f
		}
	}
	return nil
}
