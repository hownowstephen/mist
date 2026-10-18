package mist

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"html"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// builtinFilter applies a liquidjs filter mist implements, bailing on other names or argument counts.
// A switch rather than a table of funcs, so calls stay direct and nothing escapes.
func (r *renderer) builtinFilter(name string, v val, a []val, eval bool, at int) val {
	n := len(a)
	// takes bails unless the filter got lo to hi arguments, and reports whether to apply it.
	takes := func(lo, hi int) bool {
		if n < lo || n > hi {
			bail(at, "filter-arguments:"+name, "%d arguments to filter %q", n, name)
		}
		return eval
	}
	switch name {
	case "capitalize":
		if !takes(0, 0) {
			return v
		}
		return strVal(capitalize(toStr(v, at), at))
	case "date":
		if !takes(0, 2) {
			return v
		}
		return r.dateFilter(v, a, at)
	case "downcase":
		if !takes(0, 0) {
			return v
		}
		return strVal(caseMap(toStr(v, at), false, at))
	case "upcase":
		if !takes(0, 0) {
			return v
		}
		return strVal(caseMap(toStr(v, at), true, at))
	case "append":
		if !takes(1, 1) {
			return v
		}
		return strVal(toStr(v, at) + toStr(a[0], at))
	case "prepend":
		if !takes(1, 1) {
			return v
		}
		return strVal(toStr(a[0], at) + toStr(v, at))
	case "replace":
		if !takes(0, 2) {
			return v
		}
		return strVal(replace(toStr(v, at), argStr(a, 0, at), argStr(a, 1, at), at))
	case "replace_first":
		if !takes(0, 2) {
			return v
		}
		return strVal(strings.Replace(toStr(v, at), argStr(a, 0, at), argStr(a, 1, at), 1))
	case "remove":
		if !takes(0, 1) {
			return v
		}
		return strVal(strings.ReplaceAll(toStr(v, at), argStr(a, 0, at), ""))
	case "remove_first":
		if !takes(0, 1) {
			return v
		}
		return strVal(strings.Replace(toStr(v, at), argStr(a, 0, at), "", 1))
	case "strip":
		if !takes(0, 1) {
			return v
		}
		return strVal(strip(v, a, at, true, true))
	case "lstrip":
		if !takes(0, 1) {
			return v
		}
		return strVal(strip(v, a, at, true, false))
	case "rstrip":
		if !takes(0, 1) {
			return v
		}
		return strVal(strip(v, a, at, false, true))
	case "truncate":
		if !takes(0, 2) {
			return v
		}
		return truncate(v, a, at)
	case "truncatewords":
		if !takes(0, 2) {
			return v
		}
		return strVal(truncatewords(v, a, at))
	case "escape", "xml_escape":
		if !takes(0, 0) {
			return v
		}
		return strVal(html.EscapeString(toStr(v, at)))
	case "escape_once":
		if !takes(0, 0) {
			return v
		}
		return strVal(html.EscapeString(unescaper.Replace(toStr(v, at))))
	case "url_encode":
		if !takes(0, 0) {
			return v
		}
		return strVal(percentEncode(toStr(v, at), "-_.!~*'()", true, at))
	case "cgi_escape":
		if !takes(0, 0) {
			return v
		}
		return strVal(percentEncode(toStr(v, at), "-_.~", true, at))
	case "uri_escape":
		if !takes(0, 0) {
			return v
		}
		return strVal(percentEncode(toStr(v, at), "-_.!~*'();,/?:@&=+$#[]", false, at))
	case "normalize_whitespace":
		if !takes(0, 0) {
			return v
		}
		return strVal(replaceRuns(validStr(toStr(v, at), at), ' ', jsSpace))
	case "number_of_words":
		if !takes(0, 1) {
			return v
		}
		var mode string
		if len(a) > 0 {
			mode, _ = a[0].str()
		}
		return val{n: float64(numberOfWords(validStr(toStr(v, at), at), mode)), lit: litNum}
	case "slugify":
		if !takes(0, 2) {
			return v
		}
		return strVal(slugify(toStr(v, at), a, at))
	case "array_to_sentence_string":
		if !takes(0, 1) {
			return v
		}
		return sentence(v, a, at)
	case "date_to_xmlschema":
		if !takes(0, 0) {
			return v
		}
		return r.dateFilter(v, []val{strVal("%Y-%m-%dT%H:%M:%S%:z")}, at)
	case "date_to_rfc822":
		if !takes(0, 0) {
			return v
		}
		return r.dateFilter(v, []val{strVal("%a, %d %b %Y %H:%M:%S %z")}, at)
	case "date_to_string", "date_to_long_string":
		if !takes(0, 2) {
			return v
		}
		month := "%b"
		if name == "date_to_long_string" {
			month = "%B"
		}
		format := "%d " + month + " %Y"
		if len(a) > 0 {
			if t, _ := a[0].str(); t == "ordinal" {
				format = "%-d%q " + month + " %Y"
				if len(a) > 1 {
					if style, _ := a[1].str(); style == "US" {
						format = month + " %-d%q, %Y"
					}
				}
			}
		}
		return r.dateFilter(v, []val{strVal(format)}, at)
	case "pop":
		if !takes(0, 0) {
			return v
		}
		arr := toArray(v, at)
		return val{x: arr[:max(len(arr)-1, 0)]}
	case "shift":
		if !takes(0, 0) {
			return v
		}
		arr := toArray(v, at)
		return val{x: arr[min(1, len(arr)):]}
	case "unshift":
		if !takes(1, 1) {
			return v
		}
		if isNil(a[0]) {
			bail(at, "push-nil", "unshift of nil or undefined") // liquidjs keeps distinct null-ish values
		}
		return val{x: append([]any{a[0].any()}, toArray(v, at)...)}
	case "where_exp", "reject_exp":
		apply := takes(2, 2)
		r.checkExp(a, at)
		if !apply {
			return v
		}
		// liquidjs keeps the items whose value is exactly true (where_exp) or false (reject_exp).
		keep := name == "where_exp"
		out := []any{}
		for _, it := range toArray(v, at) {
			x := r.expValue(true, a[1].s, a[0].s, it, at)
			if b, ok := x.x.(bool); ok && b == keep {
				out = append(out, it)
			}
		}
		return val{x: out}
	case "find_exp", "find_index_exp", "has_exp":
		apply := takes(2, 2)
		r.checkExp(a, at)
		if !apply {
			return v
		}
		// The first item whose value is truthy in JavaScript.
		for i, it := range toArray(v, at) {
			if !jsTruthy(r.expValue(true, a[1].s, a[0].s, it, at), at) {
				continue
			}
			switch name {
			case "find_exp":
				return val{x: it}
			case "find_index_exp":
				return val{x: boxInt(i)}
			}
			return val{x: true}
		}
		if name == "has_exp" {
			return val{x: false}
		}
		return val{x: Undefined{}}
	case "sha256":
		if !takes(0, 0) {
			return v
		}
		sum := sha256.Sum256([]byte(validStr(toStr(v, at), at)))
		return strVal(hex.EncodeToString(sum[:]))
	case "to_integer":
		if !takes(0, 0) {
			return v
		}
		return val{n: jsNumberOf(v, at), lit: litNum}
	case "raw":
		if !takes(0, 0) {
			return v
		}
		if r.dialect != nil && r.dialect.Output != nil {
			bail(at, "dialect-raw-filter", "raw with a dialect Output hook") // it makes liquidjs skip outputEscape
		}
		return v
	case "group_by":
		if !takes(1, 1) {
			return v
		}
		return val{x: r.groupBy(v, a, false, at)}
	case "group_by_exp":
		apply := takes(2, 2)
		r.checkExp(a, at)
		if !apply {
			return v
		}
		return val{x: r.groupBy(v, a, true, at)}
	case "json", "jsonify", "inspect":
		if !takes(0, 0) {
			return v
		}
		if _, undef := v.x.(Undefined); undef && v.lit == 0 {
			return v // JSON.stringify(undefined) is undefined
		}
		return strVal(jsonStr(v, at))
	case "split":
		if !takes(0, 1) {
			return v
		}
		return split(v, a, at)
	case "slice":
		if !takes(1, 2) {
			return v
		}
		return slice(v, a, at)
	case "first", "last":
		if !takes(0, 0) {
			return v
		}
		return endOf(v, name == "first", at)
	case "strip_newlines":
		if !takes(0, 0) {
			return v
		}
		return strVal(replaceNewlines(toStr(v, at), ""))
	case "newline_to_br":
		if !takes(0, 0) {
			return v
		}
		return strVal(replaceNewlines(toStr(v, at), "<br />\n"))
	case "strip_html":
		if !takes(0, 0) {
			return v
		}
		return strVal(stripHTML(toStr(v, at), at))
	case "join":
		if !takes(0, 1) {
			return v
		}
		return strVal(join(v, a, at))
	case "size":
		if !takes(0, 0) {
			return v
		}
		return size(v, at)
	case "where", "reject":
		if !takes(1, 2) {
			return v
		}
		m, _ := r.matching(v, a, at, false, name == "where")
		return val{x: m}
	case "find", "find_index", "has":
		if !takes(1, 2) {
			return v
		}
		m, i := r.matching(v, a, at, true, true)
		switch {
		case name == "has":
			return val{x: len(m) > 0}
		case len(m) == 0:
			return val{x: Undefined{}}
		case name == "find":
			return val{x: m[0]}
		}
		return val{x: boxInt(i)}
	case "compact":
		if !takes(0, 0) {
			return v
		}
		out := []any{}
		for _, it := range toArray(v, at) {
			if !isNil(val{x: it}) {
				out = append(out, it)
			}
		}
		return val{x: out}
	case "uniq":
		if !takes(0, 0) {
			return v
		}
		return val{x: uniq(v, at)}
	case "concat":
		if !takes(0, 1) {
			return v
		}
		var rest []any
		if len(a) > 0 {
			rest = toArray(a[0], at)
		}
		return val{x: append(slices.Clone(toArray(v, at)), rest...)}
	case "sort_natural":
		if !takes(0, 1) {
			return v
		}
		return val{x: r.sortNatural(v, a, at)}
	case "url_decode":
		if !takes(0, 0) {
			return v
		}
		return strVal(urlDecode(toStr(v, at), at))
	case "base64_encode":
		if !takes(0, 0) {
			return v
		}
		return strVal(base64.StdEncoding.EncodeToString([]byte(validStr(toStr(v, at), at))))
	case "base64_decode":
		if !takes(0, 0) {
			return v
		}
		return strVal(base64Decode(toStr(v, at), at))
	case "hmac_sha256":
		if !takes(0, 1) {
			return v
		}
		m := hmac.New(sha256.New, []byte(validStr(argStr(a, 0, at), at)))
		m.Write([]byte(validStr(toStr(v, at), at)))
		return strVal(hex.EncodeToString(m.Sum(nil)))
	case "replace_last", "remove_last":
		hi := 1
		if name == "replace_last" {
			hi = 2
		}
		if !takes(0, hi) {
			return v
		}
		s, pat, rep := toStr(v, at), argStr(a, 0, at), ""
		if name == "replace_last" {
			rep = argStr(a, 1, at)
		}
		i := strings.LastIndex(s, pat)
		if i < 0 {
			return strVal(s)
		}
		return strVal(s[:i] + rep + s[i+len(pat):])
	case "map":
		if !takes(1, 1) {
			return v
		}
		return val{x: mapProp(v, argPath(a, at), at)}
	case "sum":
		if !takes(0, 1) {
			return v
		}
		return sum(v, a, at)
	case "sort":
		if !takes(0, 1) {
			return v
		}
		return val{x: sortBy(v, a, at)}
	case "push":
		if !takes(1, 1) {
			return v
		}
		if isNil(a[0]) {
			bail(at, "push-nil", "push of nil or undefined") // liquidjs keeps distinct null-ish values
		}
		return val{x: append(slices.Clone(toArray(v, at)), a[0].any())}
	case "reverse":
		if !takes(0, 0) {
			return v
		}
		out := slices.Clone(toArray(v, at))
		slices.Reverse(out)
		return val{x: out}

	case "abs", "ceil", "floor":
		if !takes(0, 0) {
			return v
		}
	case "round":
		if !takes(0, 1) {
			return v
		}
	case "plus", "minus", "times", "divided_by", "modulo", "at_least", "at_most":
		if !takes(1, 1) {
			return v
		}
	case "default": // only reaches here with more arguments than it takes
		takes(0, 1)
	default:
		if r.passUnknown && !liquidjsFilters[name] {
			return v // liquidjs's strictFilters: false; the arguments were evaluated above
		}
		bail(at, "filter:"+name, "unsupported filter %q", name)
	}
	x := toNumber(v, at)
	var f float64
	switch name {
	case "abs":
		f = math.Abs(x)
	case "ceil":
		f = math.Ceil(x)
	case "floor":
		f = math.Floor(x)
	case "round":
		f = round(x, a, at)
	}
	if n > 0 && name != "round" {
		y := toNumber(a[0], at)
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
		case "at_least":
			f = math.Max(x, y)
		case "at_most":
			f = math.Min(x, y)
		}
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		bail(at, "math-non-finite", "math filter result %v", f)
	}
	return val{n: f, lit: litNum}
}

func strVal(s string) val { return val{s: s, lit: litStr} }

// toStr is liquidjs's stringify.
func toStr(v val, at int) string {
	if s, ok := v.str(); ok {
		return s
	}
	var buf [32]byte // numbers and bools fit
	return string(stringify(buf[:0], v, at))
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
	if _, undef := args[i].x.(Undefined); undef && args[i].lit == 0 {
		return d
	}
	f, ok := args[i].check(at).num(at)
	if !ok {
		bail(at, "numeric-argument", "non-numeric filter argument")
	}
	return f
}

func jsTruthy(v val, at int) bool {
	if s, ok := v.str(); ok {
		return s != ""
	}
	if f, ok := v.check(at).num(at); ok {
		return f != 0 && !math.IsNaN(f)
	}
	switch x := v.x.(type) {
	case nil, Undefined:
		return false
	case nilLitT:
		bail(at, "nil-literal-argument", "the nil literal as a filter argument, which liquidjs passes as a truthy Drop")
	case bool:
		return x
	}
	return true
}

// argOrDefault stringifies v, or returns d where JavaScript's default parameter applies.
func argOrDefault(v val, d string, at int) string {
	if _, undef := v.x.(Undefined); undef && v.lit == 0 {
		return d
	}
	return toStr(v, at)
}

// jsIndex clamps x to [0, n] after truncating it, as substring and slice do for non-negative indexes.
func jsIndex(x float64, n int) int {
	return int(math.Max(0, math.Min(math.Trunc(x), float64(n))))
}

// toNumber is liquidjs's +value || 0.
func toNumber(v val, at int) float64 {
	if s, ok := v.str(); ok {
		f := jsNumberOf(v, at)
		switch {
		case math.IsNaN(f):
			return 0
		case math.IsInf(f, 0):
			bail(at, "number-from-string", "number from string %q", s)
		}
		return f
	}
	if f, ok := v.check(at).num(at); ok {
		return f
	}
	switch x := v.x.(type) {
	case nil, Undefined:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	}
	bail(at, "number-from-type", "number from %T", v.any())
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

// round is liquidjs's round: half away from zero at 10^digits.
func round(x float64, a []val, at int) float64 {
	digits := 0.0
	if len(a) > 0 {
		digits = toNumber(a[0], at)
	}
	// ponytail: 10^d is exact in both engines only for small non-negative d; widen if real templates need it.
	if digits != math.Trunc(digits) || digits < 0 || digits > 15 {
		bail(at, "round-digits", "round to %v digits", digits)
	}
	amp := math.Pow(10, digits)
	sign := x
	if x > 0 {
		sign = 1
	} else if x < 0 {
		sign = -1
	}
	return sign * math.Round(math.Abs(x*amp)) / amp
}

// validStr bails on strings JavaScript couldn't hold, as the UTF-8 encoders would differ.
func validStr(s string, at int) string {
	if !utf8.ValidString(s) {
		bail(at, "invalid-utf8", "invalid UTF-8")
	}
	return s
}

// jsString is JavaScript's String(x), for the values mist reproduces it for.
func jsString(v val, at int) string {
	if s, ok := v.str(); ok {
		return s
	}
	if s, ok := comparerString(v); ok {
		return s
	}
	if f, ok := v.check(at).num(at); ok {
		return string(appendJSNumber(nil, f))
	}
	switch x := v.x.(type) {
	case nil:
		return "null"
	case Undefined:
		return "undefined"
	case bool:
		return strconv.FormatBool(x)
	}
	bail(at, "string-type", "string of %T", v.any())
	return ""
}

// jsNumberOf is JavaScript's Number(v), which liquidjs's to_integer is: strings as
// numeric literals (NaN otherwise), null 0, undefined NaN, booleans 1 and 0.
func jsNumberOf(v val, at int) float64 {
	if s, ok := v.str(); ok {
		t := strings.TrimFunc(s, jsSpace)
		switch {
		case t == "":
			return 0
		case decimal(t):
			f, _ := strconv.ParseFloat(t, 64)
			return f
		case t == "Infinity", t == "+Infinity":
			return math.Inf(1)
		case t == "-Infinity":
			return math.Inf(-1)
		}
		if base := radix(t); base != 0 {
			return radixValue(t[2:], base, at)
		}
		return math.NaN()
	}
	if f, ok := v.check(at).num(at); ok {
		return f
	}
	switch x := v.x.(type) {
	case nil:
		return 0
	case Undefined:
		return math.NaN()
	case bool:
		if x {
			return 1
		}
		return 0
	case map[string]any:
		return math.NaN()
	}
	bail(at, "to-integer-type", "to_integer of %T", v.any()) // arrays convert through their joined string
	return 0
}

// radixValue is the value of base-radix digits s, NaN if one isn't a digit. Values from
// 2^53 up bail, where JavaScript's rounding would need reproducing.
func radixValue(s string, base int, at int) float64 {
	n := 0.0
	for i := range len(s) {
		d := strings.IndexByte("0123456789abcdef", s[i]|0x20)
		if d < 0 || d >= base || s[i] == '_' {
			return math.NaN()
		}
		if n = n*float64(base) + float64(d); n >= maxSafeInt {
			bail(at, "number-from-string", "number from a %d-digit base-%d integer", len(s), base)
		}
	}
	return n
}

// radix is the base of a 0x, 0o or 0b integer literal, or 0.
func radix(t string) int {
	if len(t) < 3 || t[0] != '0' {
		return 0
	}
	switch t[1] {
	case 'x', 'X':
		return 16
	case 'o', 'O':
		return 8
	case 'b', 'B':
		return 2
	}
	return 0
}
