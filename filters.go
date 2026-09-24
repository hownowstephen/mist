package mist

import (
	"unicode"
	"unicode/utf8"
)

// FilterFunc implements a filter. Returning an error that wraps ErrUnsupported
// hands the template to the full engine; any other error stops rendering.
type FilterFunc func(f Filter) (any, error)

// Filter is one application of a registered filter.
type Filter struct {
	Name   string
	Input  any   // nil for both nil and undefined
	Args   []any // positional arguments
	Strict bool
}

const maxFilterArgs = 4

// filters applies a `| name: arg, …` chain to v.
func (r *renderer) filters(v val, eval bool) val {
	for first := true; ; first = false {
		r.ws()
		if r.peek() != '|' {
			return v
		}
		r.p++
		r.ws()
		at := r.pos()
		name := r.ident()
		if name == "" {
			bail(at, "expected filter name")
		}
		if first && name == "default" && r.undef.set {
			// liquidjs makes the input lenient when default is the first filter (lenientIf).
			r.undef, v = pendingUndef{}, val{}
		}

		var buf [maxFilterArgs]val
		args, allowFalse := buf[:0], false
		r.ws()
		for sep := byte(':'); r.peek() == sep; sep = ',' {
			r.p++
			r.ws()
			if key, ok := r.namedArg(); ok {
				r.ws()
				w := r.ident()
				if key != "allow_false" || name != "default" || (w != "true" && w != "false") {
					bail(at, "named filter arguments other than default's allow_false: true/false are not supported")
				}
				allowFalse = w == "true"
			} else {
				if len(args) == maxFilterArgs {
					bail(at, "more than %d filter arguments", maxFilterArgs)
				}
				arg := r.expr(eval, false)
				if arg.isBlank() {
					bail(at, "blank is only supported with == and !=")
				}
				args = append(args, arg)
			}
			r.ws()
		}

		fn, registered := r.filterFns[name]
		switch {
		case registered:
			if eval {
				v = r.callFilter(fn, name, v, args, at)
			}
		case name == "default" && len(args) <= 1:
			if eval {
				v = defaultFilter(v, args, allowFalse, at)
			}
		case name == "capitalize" && len(args) == 0:
			if eval {
				v = val{s: capitalize(stringify(nil, v, at), at), lit: litStr}
			}
		default:
			bail(at, "unsupported filter %q", name)
		}
	}
}

// namedArg consumes `key:` if the next argument is named.
func (r *renderer) namedArg() (string, bool) {
	save := r.p
	key := r.ident()
	r.ws()
	if key != "" && r.peek() == ':' {
		r.p++
		return key, true
	}
	r.p = save
	return "", false
}

func (r *renderer) callFilter(fn FilterFunc, name string, v val, args []val, at int) val {
	if fn == nil {
		bail(at, "filter %q is registered without a function", name) // e.g. for Check only
	}
	f := Filter{Name: name, Input: v.data(), Args: make([]any, len(args)), Strict: r.strict}
	for i, a := range args {
		f.Args[i] = a.data()
	}
	out, err := fn(f)
	if err != nil {
		panic(bailout{wrapErr("filter", name, at, err)})
	}
	return val{x: out}
}

// data is v as a registered filter sees it: nil stands for undefined too.
func (v val) data() any {
	if isNil(v) {
		return nil
	}
	return v.any()
}

// defaultFilter mirrors liquidjs: empty strings and arrays, nil, undefined and
// false (unless allow_false) are replaced.
func defaultFilter(v val, args []val, allowFalse bool, at int) val {
	var d val
	if len(args) == 1 {
		d = args[0]
	}
	if s, ok := v.str(); ok {
		if s == "" {
			return d
		}
		return v
	}
	if v.lit == litNum {
		return v
	}
	switch x := v.check(at).x.(type) {
	case nil, undefinedT, nilLitT:
		return d
	case bool:
		if !x && !allowFalse {
			return d
		}
	case []any:
		if len(x) == 0 {
			return d
		}
	}
	return v
}

// stringify appends v as liquidjs's stringify would render it.
func stringify(dst []byte, v val, at int) []byte {
	if s, ok := v.str(); ok {
		return append(dst, s...)
	}
	if v.lit == litNum {
		return appendJSNumber(dst, v.n)
	}
	switch x := v.check(at).x.(type) {
	case nil, undefinedT, nilLitT:
		return dst
	case bool:
		if x {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case []any:
		for _, e := range x {
			dst = stringify(dst, val{x: e}, at)
		}
		return dst
	case map[string]any:
		bail(at, "stringify of an object")
	}
	f, _ := v.num(at)
	return appendJSNumber(dst, f)
}

// capitalize mirrors JavaScript's s.charAt(0).toUpperCase() + s.slice(1).toLowerCase(),
// bailing on characters whose JavaScript mapping Go's simple case tables don't match.
func capitalize(s []byte, at int) string {
	if len(s) == 0 {
		return ""
	}
	out := make([]byte, 0, len(s))
	first, n := utf8.DecodeRune(s)
	if first > 0xFFFF {
		out = append(out, s[:n]...) // charAt(0) is half a surrogate pair, which has no case
	} else {
		if jsUpperDiffers(first) {
			bail(at, "capitalize of %q, whose JavaScript upper case differs", first)
		}
		out = utf8.AppendRune(out, unicode.ToUpper(first))
	}
	for _, c := range string(s[n:]) {
		if jsLowerDiffers(c) {
			bail(at, "capitalize of %q, whose JavaScript lower case differs", c)
		}
		out = utf8.AppendRune(out, unicode.ToLower(c))
	}
	return string(out)
}

// jsUpperDiffers covers full-Unicode expansions (ß→SS, ligatures, polytonic Greek) and
// characters whose case mappings Go's and Node's Unicode versions disagree on (Go 1.26
// is behind Node 22, Go 1.27 ahead). TestCaseMapping checks every code point against Node.
func jsUpperDiffers(c rune) bool {
	switch {
	case c == 0xDF, c == 0x149, c == 0x19B, c == 0x1F0, c == 0x264, c == 0x390, c == 0x3B0,
		c == 0x587, c == 0x1C8A:
		return true
	case c >= 0x1E96 && c <= 0x1E9A, c >= 0x1F50 && c <= 0x1FFC, c >= 0xA7CB && c <= 0xA7DC, c >= 0xFB00 && c <= 0xFB17:
		return true
	}
	return false
}

// jsLowerDiffers covers İ→i̇, Σ (whose JavaScript lower case depends on word position),
// and mappings newer than Go's tables.
func jsLowerDiffers(c rune) bool {
	switch {
	case c == 0x130, c == 0x3A3, c == 0x1C89:
		return true
	case c >= 0xA7CB && c <= 0xA7DC, c >= 0x10D50 && c <= 0x10D65, c >= 0x16EA0 && c <= 0x16EB8:
		return true
	}
	return false
}
