package mist

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// undefinedT is a missing variable. liquidjs distinguishes it from null: only
// undefined trips strict mode, and undefined != null under ==.
type undefinedT struct{}

// nilLitT is the nil/null literal, which == matches against both null and undefined.
type nilLitT struct{}

var nilLit = nilLitT{}

// blankT and emptyT are the blank and empty literals, supported only as operands of == and !=.
type (
	blankT struct{}
	emptyT struct{}
)

var (
	blankLit = blankT{}
	emptyLit = emptyT{}
)

// val carries literals unboxed so evaluating them doesn't allocate.
type val struct {
	x   any // data values, bools, nil, undefined
	s   string
	n   float64
	lit uint8
}

const (
	litStr = iota + 1
	litNum
)

// keyword names v if it's the blank or empty literal.
func (v val) keyword() string {
	switch {
	case v.lit != 0:
	case v.x == blankLit:
		return "blank"
	case v.x == emptyLit:
		return "empty"
	}
	return ""
}

func (v val) any() any {
	switch v.lit {
	case litStr:
		return v.s
	case litNum:
		return v.n
	}
	return v.x
}

const maxSafeInt = 1 << 53

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
		bail(r.pos(), "unexpected %q", r.src[r.p:])
	}
	if r.undef.set {
		panic(bailout{&Error{Kind: ErrUndefined, Pos: r.undef.pos, Msg: r.undef.path}})
	}
}

func isIdentStart(c byte) bool { return identByte[c] == 2 }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentChar(c byte) bool  { return identByte[c] != 0 }

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
		bail(at, "expected and/or, got %q", r.src[at-r.base:])
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
		bail(r.pos(), "operators without whitespace before them are rejected by the dialect")
	}
	if op == "" {
		if k := a.keyword(); k != "" {
			bail(start, "%s is only supported with == and !=", k)
		}
		return eval && truthy(a, r.pos()), a, true
	}
	at := r.pos()
	b := r.expr(eval, true)
	if k, other := a.keyword(), b; k != "" || b.keyword() != "" {
		if k == "" {
			k, other = b.keyword(), a
		}
		switch {
		case op != "==" && op != "!=":
			bail(start, "%s is only supported with == and !=", k)
		case other.keyword() != "" || (other.lit == 0 && other.x == nilLit):
			bail(start, "%s compared with blank, empty or nil", k) // liquidjs is asymmetric here
		}
	}
	if op == "contains" {
		if b.lit == 0 && b.x == nilLit {
			bail(at, "contains nil") // liquidjs searches for "null"
		}
		if r.dialect != nil && r.dialect.Compare != nil {
			bail(at, "contains with a dialect Compare hook")
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
				bail(r.pos(), "contains followed by an identifier character") // liquidjs splits containsx into contains x
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
				bail(at, "blank is rejected by the dialect")
			}
			v = blankLit
		case "empty":
			if r.rejects(EmptyKeyword) {
				bail(at, "empty is rejected by the dialect")
			}
			v = emptyLit
		default:
			r.p = save
			return val{x: r.path(eval, lenient)}
		}
		if c := r.peek(); c == '.' || c == '[' {
			bail(at, "property access on a literal")
		}
		return val{x: v}
	}
	bail(at, "expected expression")
	return val{}
}

func (r *renderer) str() string {
	at := r.pos()
	end := quotedEnd(r.src, r.p)
	if end < 0 {
		bail(at, "unterminated string")
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
			bail(at, "string escape of half a surrogate pair")
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
			bail(r.base+at, "negative literals are rejected by the dialect")
		}
		i++
	}
	j := i
	for j < len(r.src) && isDigit(r.src[j]) {
		j++
	}
	if j > i && j+1 < len(r.src) && r.src[j] == '.' && r.src[j+1] != '.' || j > i && j+1 == len(r.src) && r.src[j] == '.' {
		for j++; j < len(r.src) && isDigit(r.src[j]); j++ {
		}
	}
	rangeDots := strings.HasPrefix(r.src[j:], "..")
	if j == i || j < len(r.src) && !rangeDots && (r.src[j] == '.' || isIdentChar(r.src[j]) || r.src[j] >= utf8.RuneSelf && !r.blankAt(j)) {
		bail(r.base+at, "malformed number literal")
	}
	f, err := strconv.ParseFloat(r.src[at:j], 64)
	if err != nil {
		bail(r.base+at, "malformed number literal")
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
		bail(r.base+start, "%q is not supported as a variable", name)
	case eval:
		v = r.root(name, start)
	}
	for {
		if eval && r.strict && v == (undefinedT{}) {
			if lenient {
				v = nil // liquidjs catches the strict error and substitutes null
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
				bail(at, "expected property name")
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
				bail(at, "%s as an index", k.keyword())
			}
			if _, undef := k.x.(undefinedT); eval && r.strict && undef && k.lit == 0 {
				// liquidjs evaluates index keys strictly, even where the path itself is lenient.
				panic(bailout{&Error{Kind: ErrUndefined, Pos: at, Msg: r.src[at-r.base : r.p]}})
			}
			r.ws()
			if r.peek() != ']' {
				bail(r.pos(), "expected ]")
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
	for i := r.depth - 1; i >= 0; i-- {
		if f := &r.stack[i]; f.kind == kFor && f.active && f.name == name {
			return f.item()
		}
	}
	if v, ok := r.assigns[name]; ok {
		return v
	}
	if v, ok := r.vars[name]; ok {
		return v
	}
	if magic(name) {
		bail(r.base+at, "%q resolves to a liquidjs built-in", name)
	}
	return undefinedT{}
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

// prop is liquidjs's readProperty for a key: own keys first, then the computed size,
// first and last.
func prop(v any, key string, at int) any {
	switch m := v.(type) {
	case nil, undefinedT:
		return v // liquidjs propagates nil without tripping strict mode
	case map[string]any:
		if x, ok := m[key]; ok {
			return x
		}
		if key == "size" {
			return float64(len(m))
		}
		return undefinedT{}
	case []any:
		switch key {
		case "size":
			return float64(len(m))
		case "first", "last":
			if len(m) == 0 {
				return undefinedT{}
			}
			if key == "first" {
				return m[0]
			}
			return m[len(m)-1]
		case "length":
			bail(at, "length of an array, an own JavaScript property")
		}
		return undefinedT{}
	case string:
		switch {
		case key == "size":
			if !utf8.ValidString(m) {
				bail(at, "size of invalid UTF-8")
			}
			return float64(len16(m))
		case key == "length" || strings.Trim(key, "0123456789") == "":
			bail(at, "property %q of a string, an own JavaScript property", key)
		}
		return undefinedT{}
	}
	bail(at, "property %q on %T", key, v)
	return nil
}

// key is v[k] as liquidjs reads it: strings name object keys, and integers and
// canonical integer strings index arrays (negative from the end).
func key(v any, k val, at int) any {
	switch v.(type) {
	case nil, undefinedT:
		return v
	}
	_, isArr := v.([]any)
	if s, ok := k.str(); ok {
		if !isArr {
			return prop(v, s, at)
		}
		if n, ok := canonicalInt(s); ok {
			return index(v, n, at)
		}
		if strings.TrimLeft(strings.TrimPrefix(s, "-"), "0123456789") == "" && s != "-" && s != "" {
			return undefinedT{} // a digit string JavaScript doesn't print, such as "01"
		}
		return prop(v, s, at)
	}
	if f, ok := k.check(at).num(at); ok {
		switch {
		case !isArr:
			return prop(v, string(appendJSNumber(nil, f)), at) // JavaScript keys objects by String(f)
		case f == math.Trunc(f) && math.Abs(f) < maxSafeInt:
			return index(v, int(f), at)
		}
		return undefinedT{}
	}
	bail(at, "index of %T", k.any())
	return nil
}

// canonicalInt reports whether s is an integer as JavaScript prints one.
func canonicalInt(s string) (int, bool) {
	t := strings.TrimPrefix(s, "-")
	if t == "" || len(t) > 15 || t[0] == '0' && (len(t) > 1 || t != s) {
		return 0, false
	}
	for i := range len(t) {
		if !isDigit(t[i]) {
			return 0, false
		}
	}
	n, _ := strconv.Atoi(s)
	return n, true
}

func index(v any, n int, at int) any {
	switch a := v.(type) {
	case []any:
		if n < 0 {
			n += len(a)
		}
		if n < 0 || n >= len(a) {
			return undefinedT{}
		}
		return a[n]
	}
	bail(at, "index on %T", v)
	return nil
}

// num reports v as a float64, the only number type liquidjs sees after JSON.
func num(v any, at int) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := strconv.ParseFloat(string(n), 64)
		if err != nil {
			bail(at, "bad json.Number %q", n)
		}
		return f, true
	}
	return 0, false
}

func (v val) num(at int) (float64, bool) {
	switch v.lit {
	case litNum:
		return v.n, true
	case litStr:
		return 0, false
	}
	return num(v.x, at)
}

func (v val) str() (string, bool) {
	if v.lit == litStr {
		return v.s, true
	}
	s, ok := v.x.(string)
	return s, ok
}

// check bails on values liquidjs would see differently after JSON, or that we don't model.
func (v val) check(at int) val {
	if v.lit != 0 {
		return v
	}
	switch v.x.(type) {
	case nil, undefinedT, nilLitT, bool, string, map[string]any, []any:
		return v
	}
	if _, ok := num(v.x, at); !ok {
		bail(at, "unsupported value type %T", v.x)
	}
	return v
}

func isObj(v val) bool {
	switch v.x.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

// blankValue mirrors liquidjs's BlankDrop: nil, false, and empty or whitespace-only strings, arrays and objects.
func blankValue(v val) bool {
	if s, ok := v.str(); ok {
		for _, c := range s {
			if !jsSpace(c) {
				return false
			}
		}
		return true
	}
	switch x := v.x.(type) {
	case nil, undefinedT:
		return v.lit == 0
	case bool:
		return !x
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false // numbers
}

// jsSpace is JavaScript's \s, which BlankDrop tests strings against.
func jsSpace(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return c >= 0x2000 && c <= 0x200A
}

func isNil(v val) bool {
	switch v.x.(type) {
	case nil, undefinedT, nilLitT:
		return v.lit == 0
	}
	return false
}

func truthy(v val, at int) bool {
	switch x := v.check(at).x.(type) {
	case nil, undefinedT, nilLitT:
		return v.lit != 0
	case bool:
		return x
	}
	return true
}

func (r *renderer) write(v val) {
	if s, ok := v.str(); ok {
		r.out = append(r.out, s...)
		return
	}
	if v.lit == 0 && v.x == nilLit {
		// liquidjs's nil literal is a Drop: "" by default, but "[object Object]" under
		// an outputEscape that String()s objects, so the output depends on configuration.
		bail(r.base, "cannot output the nil literal") // e.g. from default: nil
	}
	if isNil(v) {
		return
	}
	if r.dialect != nil && r.dialect.Output != nil {
		r.dialectOutput(v)
		return
	}
	if x, ok := v.x.(bool); ok {
		r.out = strconv.AppendBool(r.out, x)
		return
	}
	if f, ok := v.num(r.base); ok {
		r.out = appendJSNumber(r.out, f)
		return
	}
	bail(r.base, "cannot output %T %v", v.any(), v.any())
}

// appendJSNumber formats f as JavaScript's Number.prototype.toString does.
func appendJSNumber(dst []byte, f float64) []byte {
	if f == 0 {
		return append(dst, '0') // including -0
	}
	if f == math.Trunc(f) && math.Abs(f) < maxSafeInt {
		return strconv.AppendInt(dst, int64(f), 10) // below 2^53 every digit is needed to round-trip
	}
	var buf [32]byte
	e := strconv.AppendFloat(buf[:0], f, 'e', -1, 64) // shortest round-trip digits, as in JS
	if e[0] == '-' {
		dst = append(dst, '-')
		e = e[1:]
	}
	i := bytes.IndexByte(e, 'e')
	var digits [24]byte
	s := append(digits[:0], e[0])
	if i > 2 {
		s = append(s, e[2:i]...)
	}
	exp, neg := 0, e[i+1] == '-'
	for _, c := range e[i+2:] {
		exp = exp*10 + int(c-'0')
	}
	if neg {
		exp = -exp
	}
	k, n := len(s), exp+1 // f = 0.s × 10^n
	switch {
	case k <= n && n <= 21:
		dst = append(dst, s...)
		for range n - k {
			dst = append(dst, '0')
		}
	case 0 < n && n <= 21:
		dst = append(append(append(dst, s[:n]...), '.'), s[n:]...)
	case -6 < n && n <= 0:
		dst = append(dst, "0."...)
		for range -n {
			dst = append(dst, '0')
		}
		dst = append(dst, s...)
	default:
		dst = append(dst, s[0])
		if k > 1 {
			dst = append(append(dst, '.'), s[1:]...)
		}
		dst = append(dst, 'e')
		if n > 0 {
			dst = append(dst, '+')
		}
		dst = strconv.AppendInt(dst, int64(n-1), 10)
	}
	return dst
}

func compare(op string, a, b val, at int) bool {
	switch op {
	case "==":
		return eq(a, b, at)
	case "!=":
		return !eq(a, b, at)
	}
	var c int
	if x, ok := a.num(at); ok {
		y, ok := b.num(at)
		if !ok {
			bail(at, "%s between number and %T", op, b.any())
		}
		c = cmpFloat(x, y)
	} else {
		x, ok1 := a.str()
		y, ok2 := b.str()
		if !ok1 || !ok2 || !ascii(x) || !ascii(y) {
			bail(at, "%s needs two numbers or two ASCII strings", op)
		}
		c = strings.Compare(x, y)
	}
	switch op {
	case "<":
		return c < 0
	case ">":
		return c > 0
	case "<=":
		return c <= 0
	}
	return c >= 0
}

func cmpFloat(x, y float64) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// ascii strings compare the same in Go (bytes) and JS (UTF-16 units).
func ascii(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// eq mirrors liquidjs: === on scalars, except the nil literal matches null and undefined.
func eq(a, b val, at int) bool {
	if a.keyword() != "" {
		a, b = b, a
	}
	switch b.keyword() { // cmp has ruled out a keyword against a keyword or nil
	case "blank":
		return blankValue(a.check(at))
	case "empty":
		return emptyValue(a.check(at))
	}
	a, b = a.check(at), b.check(at)
	if (a.lit == 0 && a.x == nilLit) || (b.lit == 0 && b.x == nilLit) {
		return isNil(a) && isNil(b)
	}
	if isObj(a) || isObj(b) {
		bail(at, "== with an object or array")
	}
	if x, ok := a.num(at); ok {
		y, ok := b.num(at)
		return ok && x == y
	}
	if x, ok := a.str(); ok {
		y, ok := b.str()
		return ok && x == y
	}
	if _, ok := b.num(at); ok {
		return false
	}
	if _, ok := b.str(); ok {
		return false
	}
	return a.x == b.x // nil, undefined, bool
}

// emptyValue mirrors liquidjs's EmptyDrop: only "", [] and {} equal empty.
func emptyValue(v val) bool {
	if s, ok := v.str(); ok {
		return s == ""
	}
	switch x := v.x.(type) {
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// contains mirrors liquidjs: array elements by ===, substrings of a string (the right side
// coerced as indexOf does), and false for anything else.
func contains(a, b val, at int) bool {
	a, b = a.check(at), b.check(at)
	if isObj(b) {
		bail(at, "contains with an object or array on the right")
	}
	if arr, ok := a.x.([]any); ok {
		for _, e := range arr {
			if ev := (val{x: e}).check(at); !isObj(ev) && eq(ev, b, at) {
				return true
			}
		}
		return false
	}
	s, ok := a.str()
	if !ok {
		return false
	}
	needle := "null"
	switch _, undef := b.x.(undefinedT); {
	case b.lit != 0 || b.x != nil && !undef:
		needle = string(stringify(nil, b, at))
	case undef:
		needle = "undefined"
	}
	return strings.Contains(s, needle)
}

// rangeLit = "(" ws expr ws ".." ws expr ws ")" with integer bounds, liquidjs's range(+lo, +hi + 1).
func (r *renderer) rangeLit(eval bool) any {
	at := r.pos()
	r.p++
	lo := r.expr(eval, false)
	r.ws()
	if !strings.HasPrefix(r.src[r.p:], "..") {
		bail(r.pos(), "expected .. in range")
	}
	r.p += 2
	hi := r.expr(eval, false)
	r.ws()
	if r.peek() != ')' {
		bail(r.pos(), "expected ) after range")
	}
	r.p++
	if !eval {
		return nil
	}
	a, b := rangeBound(lo, at), rangeBound(hi, at)
	if b-a >= maxRange {
		bail(at, "range of more than %d items", maxRange)
	}
	out := make([]any, 0, max(0, b-a+1))
	for i := a; i <= b; i++ {
		out = append(out, float64(i))
	}
	return out
}

const maxRange = 100000

// rangeBound is an integer range bound; liquidjs's unary + reads other values differently from toNumber.
func rangeBound(v val, at int) int {
	f, ok := v.check(at).num(at)
	if !ok || f != math.Trunc(f) || math.Abs(f) >= maxSafeInt {
		bail(at, "range bound that isn't an integer")
	}
	return int(f)
}

func (f *frame) item() any {
	if f.rev {
		return f.coll[len(f.coll)-1-f.idx]
	}
	return f.coll[f.idx]
}

// forloop resolves forloop.<prop> against the innermost for, as liquidjs's ForloopDrop.
func (r *renderer) forloop(eval bool, start int) any {
	f := r.loop()
	if f == nil || r.peek() != '.' {
		bail(r.base+start, "forloop outside a for loop or without a property")
	}
	r.p++
	at := r.pos()
	p := r.ident()
	if !eval {
		return nil
	}
	n, i := len(f.coll), f.idx
	var v any
	switch p {
	case "index":
		v = float64(i + 1)
	case "index0":
		v = float64(i)
	case "rindex":
		v = float64(n - i)
	case "rindex0":
		v = float64(n - i - 1)
	case "first":
		v = i == 0
	case "last":
		v = i == n-1
	case "length":
		v = float64(n)
	case "name":
		v = f.name + "-" + f.collText
	case "":
		bail(at, "expected property name")
	default:
		v = undefinedT{} // liquidjs 10.26 has no parentloop or other properties
	}
	return v
}

// loop is the innermost for frame, or nil.
func (r *renderer) loop() *frame {
	for i := r.depth - 1; i >= 0; i-- {
		if r.stack[i].kind == kFor {
			return &r.stack[i]
		}
	}
	return nil
}
