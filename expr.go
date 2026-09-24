package mist

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// undefinedT is a missing variable. liquidjs distinguishes it from null: only
// undefined trips strict mode, and undefined != null under ==.
type undefinedT struct{}

// nilLitT is the nil/null literal, which == matches against both null and undefined.
type nilLitT struct{}

var nilLit = nilLitT{}

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

func (r *renderer) ws() {
	for r.p < len(r.src) && isBlank(r.src[r.p]) {
		r.p++
	}
}

func (r *renderer) end() {
	r.ws()
	if r.p < len(r.src) {
		bail(r.pos(), "unexpected %q", r.src[r.p:])
	}
	if r.undef != nil {
		panic(bailout{r.undef})
	}
}

func isIdentStart(c byte) bool { return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentChar(c byte) bool  { return isIdentStart(c) || isDigit(c) || c == '-' }

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
	res := r.cmp(eval)
	join := ""
	for {
		r.ws()
		if r.p == len(r.src) {
			return res
		}
		at := r.pos()
		w := r.ident()
		if w != "and" && w != "or" {
			bail(at, "expected and/or, got %q", r.src[at-r.base:])
		}
		if join != "" && w != join {
			bail(at, "mixed and/or")
		}
		join = w
		v := r.cmp(eval)
		if w == "and" {
			res = res && v
		} else {
			res = res || v
		}
	}
}

// cmp = expr [ op expr ]
func (r *renderer) cmp(eval bool) bool {
	a := r.expr(eval, true)
	r.ws()
	op := r.op()
	if op == "" {
		return eval && truthy(a, r.pos())
	}
	at := r.pos()
	b := r.expr(eval, true)
	return eval && compare(op, a, b, at)
}

func (r *renderer) op() string {
	rest := r.src[r.p:]
	for _, op := range [...]string{"==", "!=", "<=", ">=", "<", ">"} {
		if strings.HasPrefix(rest, op) {
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
	case c == '-' || isDigit(c):
		return val{n: r.number(), lit: litNum}
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
		case "empty", "blank":
			bail(at, "empty/blank are not supported")
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
	q := r.src[r.p]
	k := strings.IndexByte(r.src[r.p+1:], q)
	if k < 0 {
		bail(r.pos(), "unterminated string")
	}
	s := r.src[r.p+1 : r.p+1+k]
	if strings.IndexByte(s, '\\') >= 0 {
		bail(r.pos(), "escapes in strings are not supported")
	}
	r.p += k + 2
	return s
}

// int = [ "-" ] digit { digit }
func (r *renderer) number() float64 {
	at, i := r.p, r.p
	if r.src[i] == '-' {
		i++
	}
	j := i
	for j < len(r.src) && isDigit(r.src[j]) {
		j++
	}
	if j == i || (j < len(r.src) && (r.src[j] == '.' || isIdentChar(r.src[j]))) {
		bail(r.base+at, "only integer literals are supported")
	}
	n, err := strconv.ParseInt(r.src[at:j], 10, 64)
	if err != nil || n >= maxSafeInt || n <= -maxSafeInt {
		bail(r.base+at, "integer literal out of range")
	}
	r.p = j
	return float64(n)
}

// path = ident { "." ident | "[" int "]" | "[" string "]" }
func (r *renderer) path(eval, lenient bool) any {
	start := r.p
	name := r.ident()
	if name == "forloop" || reserved(name) || literal(name) {
		bail(r.base+start, "%q is not supported as a variable", name)
	}
	var v any
	if eval {
		v = r.root(name, start)
	}
	for {
		if eval && r.strict && v == (undefinedT{}) {
			if lenient {
				v = nil // liquidjs catches the strict error and substitutes null
			} else if r.undef == nil {
				// Raised by end(): a trailing filter such as `| default` makes liquidjs lenient.
				r.undef = &Error{Kind: ErrUndefined, Pos: r.base + start, Msg: r.src[start:r.p]}
			}
		}
		switch r.peek() {
		case '.':
			r.p++
			at := r.pos()
			seg := r.ident()
			if seg == "" {
				bail(at, "expected property name")
			}
			if eval {
				v = prop(v, seg, at)
			}
		case '[':
			r.p++
			at := r.pos()
			switch c := r.peek(); {
			case c == '"' || c == '\'':
				key := r.str()
				if eval {
					v = prop(v, key, at)
				}
			case c == '-' || isDigit(c):
				n := int(r.number())
				if eval {
					v = index(v, n, at)
				}
			default:
				bail(at, "index must be an integer or string literal")
			}
			if r.peek() != ']' {
				bail(r.pos(), "expected ]")
			}
			r.p++
		default:
			return v
		}
	}
}

func (r *renderer) root(name string, at int) any {
	for i := r.depth - 1; i >= 0; i-- {
		if f := &r.stack[i]; f.kind == kFor && f.active && f.name == name {
			return f.coll[f.idx]
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

// reserved words that liquidjs parses as operators even where a variable is expected.
func reserved(name string) bool {
	return name == "contains" || name == "and" || name == "or" || name == "not"
}

// magic keys that liquidjs computes when the property is absent.
func magic(key string) bool { return key == "size" || key == "first" || key == "last" }

func prop(v any, key string, at int) any {
	switch m := v.(type) {
	case nil, undefinedT:
		return v // liquidjs propagates nil without tripping strict mode
	case map[string]any:
		if x, ok := m[key]; ok {
			return x
		}
		if magic(key) {
			bail(at, "%q resolves to a liquidjs built-in", key)
		}
		return undefinedT{}
	}
	bail(at, "property %q on %T", key, v)
	return nil
}

func index(v any, n int, at int) any {
	switch a := v.(type) {
	case nil, undefinedT:
		return v
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
	switch x := v.x.(type) {
	case bool:
		r.out = strconv.AppendBool(r.out, x)
		return
	case nil, undefinedT, nilLitT:
		if v.lit == 0 {
			return
		}
	}
	if f, ok := v.num(r.base); ok && f == math.Trunc(f) && math.Abs(f) < maxSafeInt {
		r.out = strconv.AppendInt(r.out, int64(f), 10)
		return
	}
	bail(r.base, "cannot output %T %v", v.any(), v.any())
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
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// eq mirrors liquidjs: === on scalars, except the nil literal matches null and undefined.
func eq(a, b val, at int) bool {
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
