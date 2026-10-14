package mist

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Undefined is JavaScript's undefined, a missing value, which liquidjs distinguishes from
// null: only undefined trips strict mode, and undefined != null under ==. Hooks see it
// inside arrays and objects mist builds, such as `map` of a missing property; at the top
// level they get nil for it instead.
type Undefined struct{}

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
		if v.n == math.Trunc(v.n) && v.n >= 0 && !math.Signbit(v.n) && v.n < float64(len(boxed)) {
			return boxed[int(v.n)]
		}
		return v.n
	}
	return v.x
}

const maxSafeInt = 1 << 53

// prop is liquidjs's readProperty for a key: own keys first, then the computed size,
// first and last.
func prop(v any, key string, at int) any {
	switch m := v.(type) {
	case nil, Undefined:
		return v // liquidjs propagates nil without tripping strict mode
	case map[string]any:
		if x, ok := m[key]; ok {
			return x
		}
		if key == "size" {
			return boxInt(len(m))
		}
		return Undefined{}
	case []any:
		switch key {
		case "size":
			return boxInt(len(m))
		case "first", "last":
			if len(m) == 0 {
				return Undefined{}
			}
			if key == "first" {
				return m[0]
			}
			return m[len(m)-1]
		case "length":
			bail(at, "array-length-property", "length of an array, an own JavaScript property")
		}
		return Undefined{}
	case string:
		switch {
		case key == "size":
			if !utf8.ValidString(m) {
				bail(at, "invalid-utf8", "size of invalid UTF-8")
			}
			return boxInt(len16(m))
		case key == "length" || strings.Trim(key, "0123456789") == "":
			bail(at, "string-own-property", "property %q of a string, an own JavaScript property", key)
		}
		return Undefined{}
	}
	bail(at, "property-on-scalar", "property %q on %T", key, v)
	return nil
}

// key is v[k] as liquidjs reads it: strings name object keys, and integers and
// canonical integer strings index arrays (negative from the end).
func key(v any, k val, at int) any {
	switch v.(type) {
	case nil, Undefined:
		return v
	}
	arr, isArr := v.([]any)
	if s, ok := k.str(); ok {
		if !isArr {
			return prop(v, s, at)
		}
		if n, ok := canonicalInt(s); ok {
			return index(arr, n)
		}
		if strings.TrimLeft(strings.TrimPrefix(s, "-"), "0123456789") == "" && s != "-" && s != "" {
			return Undefined{} // a digit string JavaScript doesn't print, such as "01"
		}
		return prop(v, s, at)
	}
	if f, ok := k.check(at).num(at); ok {
		switch {
		case !isArr:
			return prop(v, string(appendJSNumber(nil, f)), at) // JavaScript keys objects by String(f)
		case f == math.Trunc(f) && math.Abs(f) < maxSafeInt:
			return index(arr, int(f))
		}
		return Undefined{}
	}
	bail(at, "index-type", "index of %T", k.any())
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

func index(a []any, n int) any {
	if n < 0 {
		n += len(a)
	}
	if n < 0 || n >= len(a) {
		return Undefined{}
	}
	return a[n]
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
			bail(at, "bad-json-number", "bad json.Number %q", n)
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
	case nil, Undefined, nilLitT, bool, string, map[string]any, []any:
		return v
	}
	if _, ok := num(v.x, at); !ok {
		bail(at, "value-type", "unsupported value type %T", v.x)
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
	case nil, Undefined, nilLitT:
		return v.lit == 0
	}
	return false
}

func truthy(v val, at int) bool {
	switch x := v.check(at).x.(type) {
	case nil, Undefined, nilLitT:
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
		bail(r.base, "output-nil-literal-result", "cannot output the nil literal") // e.g. from default: nil
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
	bail(r.base, "output-type", "cannot output %T %v", v.any(), v.any())
}

// appendJSNumber formats f as JavaScript's Number.prototype.toString does.
func appendJSNumber(dst []byte, f float64) []byte {
	switch {
	case math.IsNaN(f):
		return append(dst, "NaN"...) // only registered filters produce these
	case math.IsInf(f, 1):
		return append(dst, "Infinity"...)
	case math.IsInf(f, -1):
		return append(dst, "-Infinity"...)
	}
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

// boxed holds float64s for small integers already in interfaces, so loop indexes, sizes and
// range items don't allocate. Converting a float64 to any otherwise allocates.
var boxed = func() (t [1024]any) {
	for i := range t {
		t[i] = float64(i)
	}
	return t
}()

func boxInt(i int) any {
	if i >= 0 && i < len(boxed) {
		return boxed[i]
	}
	return float64(i)
}
