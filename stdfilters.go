package mist

import (
	"errors"
	"html"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// builtinFilter applies a liquidjs filter mist implements, bailing on other names or argument counts.
// A switch rather than a table of funcs, so calls stay direct and nothing escapes.
func (r *renderer) builtinFilter(name string, v val, a []val, eval bool, at int) val {
	n := len(a)
	arity := func(min, max int) bool { return n >= min && n <= max }
	var ok bool
	switch name {
	case "capitalize", "downcase", "upcase", "escape", "escape_once", "url_encode", "json", "first", "last":
		ok = arity(0, 0)
	case "date", "replace", "replace_first", "truncate", "truncatewords":
		ok = arity(0, 2)
	case "remove", "remove_first", "strip", "lstrip", "rstrip", "split":
		ok = arity(0, 1)
	case "append", "prepend", "plus", "minus", "times", "divided_by", "modulo":
		ok = arity(1, 1)
	case "slice":
		ok = arity(1, 2)
	}
	if !ok {
		bail(at, "unsupported filter %q", name)
	}
	if !eval {
		return v
	}
	switch name {
	case "capitalize":
		return strVal(capitalize(toStr(v, at), at))
	case "date":
		return r.dateFilter(v, a, at)
	case "downcase":
		return strVal(caseMap(toStr(v, at), false, at))
	case "upcase":
		return strVal(caseMap(toStr(v, at), true, at))
	case "append":
		return strVal(toStr(v, at) + toStr(a[0], at))
	case "prepend":
		return strVal(toStr(a[0], at) + toStr(v, at))
	case "replace":
		return strVal(replace(toStr(v, at), argStr(a, 0, at), argStr(a, 1, at), at))
	case "replace_first":
		return strVal(strings.Replace(toStr(v, at), argStr(a, 0, at), argStr(a, 1, at), 1))
	case "remove":
		return strVal(strings.ReplaceAll(toStr(v, at), argStr(a, 0, at), ""))
	case "remove_first":
		return strVal(strings.Replace(toStr(v, at), argStr(a, 0, at), "", 1))
	case "strip":
		return strVal(strip(v, a, at, true, true))
	case "lstrip":
		return strVal(strip(v, a, at, true, false))
	case "rstrip":
		return strVal(strip(v, a, at, false, true))
	case "truncate":
		return truncate(v, a, at)
	case "truncatewords":
		return strVal(truncatewords(v, a, at))
	case "escape":
		return strVal(html.EscapeString(toStr(v, at)))
	case "escape_once":
		return strVal(html.EscapeString(unescaper.Replace(toStr(v, at))))
	case "url_encode":
		return strVal(urlEncode(toStr(v, at), at))
	case "json":
		if _, undef := v.x.(undefinedT); undef && v.lit == 0 {
			return v // JSON.stringify(undefined) is undefined
		}
		return strVal(string(appendJSON(nil, v, at)))
	case "split":
		return split(v, a, at)
	case "slice":
		return slice(v, a, at)
	case "first", "last":
		return endOf(v, name == "first", at)
	}
	x, y := toNumber(v, at), toNumber(a[0], at)
	var f float64
	switch name {
	case "plus":
		f = x + y
	case "minus":
		f = x - y
	case "times":
		f = x * y
	case "divided_by":
		f = x / y
	case "modulo":
		f = math.Mod(x, y)
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		bail(at, "math filter result %v", f)
	}
	return val{n: f, lit: litNum}
}

// unescaper is liquidjs's unescape, which knows only the entities escape produces.
var unescaper = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'")

func strVal(s string) val { return val{s: s, lit: litStr} }

// toStr is liquidjs's stringify.
func toStr(v val, at int) string {
	if s, ok := v.str(); ok {
		return s
	}
	return string(stringify(nil, v, at))
}

// argStr stringifies argument i, which JavaScript leaves undefined (so "") when absent.
func argStr(args []val, i, at int) string {
	if i >= len(args) {
		return ""
	}
	return toStr(args[i], at)
}

// argNum is a numeric argument; absent or undefined takes the JavaScript default d.
func argNum(args []val, i int, d float64, at int) float64 {
	if i >= len(args) {
		return d
	}
	if _, undef := args[i].x.(undefinedT); undef && args[i].lit == 0 {
		return d
	}
	f, ok := args[i].check(at).num(at)
	if !ok {
		bail(at, "non-numeric filter argument")
	}
	return f
}

// units splits s into UTF-16 code units, bailing where one would be half a surrogate pair.
func units(s string, at int) []rune {
	if !utf8.ValidString(s) {
		bail(at, "string filter on invalid UTF-8")
	}
	rs := []rune(s)
	for _, c := range rs {
		if c > 0xFFFF {
			bail(at, "string filter splitting a character outside the BMP")
		}
	}
	return rs
}

// utf16s is s as JavaScript's UTF-16 code units.
func utf16s(s string, at int) []uint16 {
	if !utf8.ValidString(s) {
		bail(at, "string filter on invalid UTF-8")
	}
	return utf16.Encode([]rune(s))
}

// sub16 is u.slice(i, j) as a string, bailing where that would split a surrogate pair.
func sub16(u []uint16, i, j, at int) string {
	if i < j && (u[i] >= 0xDC00 && u[i] <= 0xDFFF || u[j-1] >= 0xD800 && u[j-1] <= 0xDBFF) {
		bail(at, "string filter splitting a character outside the BMP")
	}
	return string(utf16.Decode(u[i:j]))
}

// len16 is JavaScript's string length.
func len16(s string) int {
	n := 0
	for _, c := range s {
		n++
		if c > 0xFFFF {
			n++
		}
	}
	return n
}

// caseMap is JavaScript's toUpperCase or toLowerCase, bailing on characters Go maps differently.
func caseMap(s string, upper bool, at int) string {
	for _, c := range s {
		if upper && jsUpperDiffers(c) || !upper && jsLowerDiffers(c) {
			bail(at, "case mapping of %q, whose JavaScript mapping differs", c)
		}
	}
	if upper {
		return strings.Map(unicode.ToUpper, s)
	}
	return strings.Map(unicode.ToLower, s)
}

// replace is str.split(p).join(r).
func replace(s, p, r string, at int) string {
	if p != "" {
		return strings.ReplaceAll(s, p, r)
	}
	us := units(s, at)
	var b strings.Builder
	for i, c := range us {
		if i > 0 {
			b.WriteString(r)
		}
		b.WriteRune(c)
	}
	return b.String()
}

// strip trims whitespace as String.prototype.trim does, or the characters of a truthy argument.
func strip(v val, a []val, at int, left, right bool) string {
	s := toStr(v, at)
	cut := jsSpace
	if len(a) == 1 && jsTruthy(a[0], at) {
		chars := argStr(a, 0, at)
		units(chars, at)
		cut = func(c rune) bool { return strings.ContainsRune(chars, c) }
	}
	if left {
		s = strings.TrimLeftFunc(s, cut)
	}
	if right {
		s = strings.TrimRightFunc(s, cut)
	}
	return s
}

func jsTruthy(v val, at int) bool {
	if s, ok := v.str(); ok {
		return s != ""
	}
	if f, ok := v.check(at).num(at); ok {
		return f != 0 && !math.IsNaN(f)
	}
	switch x := v.x.(type) {
	case nil, undefinedT:
		return false
	case nilLitT:
		bail(at, "the nil literal as a filter argument, which liquidjs passes as a truthy Drop")
	case bool:
		return x
	}
	return true
}

// truncate is liquidjs's truncate: v itself when it fits, else a prefix plus the ellipsis.
func truncate(v val, a []val, at int) val {
	s := toStr(v, at)
	l := argNum(a, 0, 50, at)
	o := "..."
	if len(a) > 1 {
		o = argOrDefault(a[1], o, at)
	}
	if float64(len16(s)) <= l {
		return v
	}
	u := utf16s(s, at)
	return strVal(sub16(u, 0, jsIndex(l-float64(len16(o)), len(u)), at) + o)
}

// argOrDefault stringifies v, or returns d where JavaScript's default parameter applies.
func argOrDefault(v val, d string, at int) string {
	if _, undef := v.x.(undefinedT); undef && v.lit == 0 {
		return d
	}
	return toStr(v, at)
}

// jsIndex clamps x to [0, n] after truncating it, as substring and slice do for non-negative indexes.
func jsIndex(x float64, n int) int {
	return int(math.Max(0, math.Min(math.Trunc(x), float64(n))))
}

// truncatewords is liquidjs's truncatewords, including its ellipsis when the count is exact.
func truncatewords(v val, a []val, at int) string {
	s := toStr(v, at)
	words := argNum(a, 0, 15, at)
	o := "..."
	if len(a) > 1 {
		o = argOrDefault(a[1], o, at)
	}
	// str.split(/\s+/): leading or trailing whitespace yields an empty first or last word.
	var arr []string
	start, inSpace := 0, false
	for i, c := range s {
		switch {
		case jsSpace(c) && !inSpace:
			arr, inSpace = append(arr, s[start:i]), true
		case !jsSpace(c) && inSpace:
			start, inSpace = i, false
		}
	}
	if inSpace {
		arr = append(arr, "")
	} else {
		arr = append(arr, s[start:])
	}
	if words <= 0 {
		words = 1
	}
	ret := strings.Join(arr[:jsIndex(words, len(arr))], " ")
	if float64(len(arr)) >= words {
		ret += o
	}
	return ret
}

// urlEncode is encodeURIComponent with %20 as +.
func urlEncode(s string, at int) string {
	if !utf8.ValidString(s) {
		bail(at, "url_encode of invalid UTF-8")
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// appendJSON is JSON.stringify for values whose output Go can reproduce: objects bail,
// because JavaScript keeps their key order and Go maps don't.
func appendJSON(dst []byte, v val, at int) []byte {
	if s, ok := v.str(); ok {
		return appendJSONString(dst, s)
	}
	if f, ok := v.check(at).num(at); ok {
		return appendJSNumber(dst, f)
	}
	switch x := v.x.(type) {
	case nil:
		return append(dst, "null"...)
	case bool:
		return strconv.AppendBool(dst, x)
	case []any:
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			if e == nil {
				dst = append(dst, "null"...) // JSON.stringify writes null for undefined elements too
				continue
			}
			dst = appendJSON(dst, val{x: e}, at)
		}
		return append(dst, ']')
	}
	bail(at, "json of %T", v.any())
	return nil
}

func appendJSONString(dst []byte, s string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\b':
			dst = append(dst, `\b`...)
		case '\f':
			dst = append(dst, `\f`...)
		case '\n':
			dst = append(dst, `\n`...)
		case '\r':
			dst = append(dst, `\r`...)
		case '\t':
			dst = append(dst, `\t`...)
		default:
			if c < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
			} else {
				dst = append(dst, c)
			}
		}
	}
	return append(dst, '"')
}

// split is str.split(sep) with trailing empty strings dropped, as Ruby does.
func split(v val, a []val, at int) val {
	s, sep := toStr(v, at), argStr(a, 0, at)
	var parts []string
	if sep != "" {
		parts = strings.Split(s, sep)
	} else {
		for _, c := range units(s, at) {
			parts = append(parts, string(c))
		}
	}
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return val{x: out}
}

// slice is liquidjs's slice: begin counts from the end when negative, length defaults to 1.
func slice(v val, a []val, at int) val {
	begin, length := argNum(a, 0, math.NaN(), at), argNum(a, 1, 1, at)
	if math.IsNaN(begin) {
		bail(at, "slice without a begin")
	}
	if isNil(v) {
		return val{x: []any{}}
	}
	arr, isArr := v.check(at).x.([]any)
	var u []uint16
	n := len(arr)
	if !isArr {
		u = utf16s(toStr(v, at), at)
		n = len(u)
	}
	if begin < 0 {
		begin += float64(n)
	}
	i, j := jsRel(begin, n), jsRel(begin+length, n)
	if j < i {
		j = i
	}
	if isArr {
		return val{x: arr[i:j]}
	}
	return strVal(sub16(u, i, j, at))
}

// jsRel resolves a relative index the way Array.prototype.slice does.
func jsRel(x float64, n int) int {
	x = math.Trunc(x)
	if x < 0 {
		return int(math.Max(0, x+float64(n)))
	}
	return int(math.Min(x, float64(n)))
}

// endOf is liquidjs's first (v[0] of an array or string, else "") or last (v[v.length-1]).
func endOf(v val, first bool, at int) val {
	if arr, ok := v.check(at).x.([]any); ok {
		if len(arr) == 0 {
			bail(at, "first or last of an empty array, which is undefined")
		}
		if first {
			return val{x: arr[0]}
		}
		return val{x: arr[len(arr)-1]}
	}
	if s, ok := v.str(); ok && s != "" {
		u := utf16s(s, at)
		if first {
			return strVal(sub16(u, 0, 1, at))
		}
		return strVal(sub16(u, len(u)-1, len(u), at))
	}
	if first {
		if isObj(v) {
			bail(at, "first of an object")
		}
		return strVal("")
	}
	bail(at, "last of %T", v.any())
	return val{}
}

// toNumber is liquidjs's +value || 0.
func toNumber(v val, at int) float64 {
	if s, ok := v.str(); ok {
		return strNumber(strings.TrimFunc(s, jsSpace), at)
	}
	if f, ok := v.check(at).num(at); ok {
		return f
	}
	switch x := v.x.(type) {
	case nil, undefinedT:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	}
	bail(at, "number from %T", v.any())
	return 0
}

// strNumber is JavaScript's Number(t) || 0 for a trimmed string: decimals, and unsigned
// 0x, 0o and 0b integers. Everything else is NaN, so 0.
func strNumber(t string, at int) float64 {
	if t == "" {
		return 0
	}
	if decimal(t) {
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	switch t {
	case "Infinity", "+Infinity", "-Infinity":
		bail(at, "number from string %q", t)
	}
	if len(t) > 2 && t[0] == '0' {
		base := 0
		switch t[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 && !strings.ContainsAny(t[2:], "_+-") {
			n, err := strconv.ParseUint(t[2:], base, 64)
			if errors.Is(err, strconv.ErrRange) {
				bail(at, "number from string %q", t)
			}
			if err == nil {
				return float64(n)
			}
		}
	}
	return 0
}

// decimal matches [+-]?(digits[.digits?]|.digits)([eE][+-]?digits)?.
func decimal(s string) bool {
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
	}
	intStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	digits := i > intStart
	if i < len(s) && s[i] == '.' {
		i++
		fracStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		digits = digits || i > fracStart
	}
	if !digits {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		expStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == expStart {
			return false
		}
	}
	return i == len(s)
}
