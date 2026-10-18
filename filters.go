package mist

import (
	"errors"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FilterFunc implements a filter. Returning an error that wraps ErrUnsupported
// hands the template to the full engine, ErrBuiltin applies the built-in filter of
// the same name instead, and ErrUnchanged leaves the input as it was; any other
// error stops rendering.
type FilterFunc func(f Filter) (any, error)

// Filter is one application of a registered filter.
type Filter struct {
	Name   string
	Input  any   // nil for both nil and undefined; inside arrays and objects, undefined is Undefined
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
			bail(at, "expected-filter-name", "expected filter name")
		}
		if first && name == "default" && r.undef.set && (r.dialect == nil || !r.dialect.NoDefaultLeniency) {
			// liquidjs makes the input lenient when default is the first filter (lenientIf).
			r.undef, v = pendingUndef{}, val{}
		}

		var buf [maxFilterArgs]val
		args, allowFalse, named := buf[:0], false, false
		r.ws()
		for sep := byte(':'); r.peek() == sep; sep = ',' {
			r.p++
			r.ws()
			if sep == ':' && (r.p == len(r.src) || r.peek() == '|') {
				break // liquidjs takes "| f:" as no arguments
			}
			if key, ok := r.namedArg(); ok {
				r.ws()
				w := r.ident()
				if key != "allow_false" || name != "default" || (w != "true" && w != "false") {
					bail(at, "named-filter-arguments", "named filter arguments other than default's allow_false: true/false are not supported")
				}
				if len(args) == 0 {
					bail(at, "named-filter-arguments", "allow_false needs a default value before it") // liquidjs uses the pair as the default
				}
				allowFalse, named = w == "true", true
			} else {
				if named {
					bail(at, "named-filter-arguments", "positional filter arguments after allow_false are not supported")
				}
				if len(args) == maxFilterArgs {
					bail(at, "too-many-filter-arguments", "more than %d filter arguments", maxFilterArgs)
				}
				arg := r.expr(eval, false)
				if k := arg.keyword(); k != "" {
					bail(at, "keyword-outside-equality", "%s is only supported in conditions and output", k)
				}
				args = append(args, arg)
			}
			r.ws()
		}

		if eval {
			r.spend(name, v, args, at)
		}
		fn, registered := r.filterFns[name]
		if !registered && r.dialect != nil && r.dialect.NoBuiltinFilters {
			bail(at, "filter:"+name, "filter %q isn't registered, and the dialect has no built-ins", name)
		}
		if registered {
			if !eval {
				continue
			}
			out, builtin := r.callFilter(fn, name, v, args, at)
			if !builtin {
				v = out
				continue
			}
		}
		switch {
		case name == "default" && len(args) <= 1:
			if eval {
				v = defaultFilter(v, args, allowFalse, at)
			}
		default:
			v = r.builtinFilter(name, v, args, eval, at)
		}
	}
}

// spend charges a filter at least what liquidjs's memoryLimit does, before it runs.
// ponytail: charges inputs, as liquidjs does, plus the output of replace and date, the
// filters whose output can dwarf their input; other results aren't charged.
func (r *renderer) spend(name string, v val, args []val, at int) {
	cost := memSize(v)
	for _, a := range args {
		cost += memSize(a)
	}
	switch name {
	case "escape_once": // escape(unescape(s)) charges s twice
		cost += memSize(v)
	case "join":
		sep := 1 // a nil separator joins with ' '
		if len(args) > 0 && !isNil(args[0]) {
			sep = memSize(args[0])
		}
		n := 1 // liquidjs's toArray wraps a value that isn't an array or nil
		if a, ok := v.x.([]any); ok && v.lit == 0 {
			n = len(a)
		} else if isNil(v) {
			n = 0
		}
		cost += n * (1 + sep)
	case "truncate", "truncatewords", "array_to_sentence_string":
		i := 1 // an undefined ellipsis defaults to '...'
		if name == "array_to_sentence_string" {
			i = 0 // and an undefined connector to 'and'
		}
		if len(args) <= i || args[i].lit == 0 && args[i].x == (Undefined{}) {
			cost += 3
		}
	case "slice", "sample": // liquidjs charges slice's length and sample's count
		if i := len(args) - 1; i >= 0 && (name == "sample" || i == 1) {
			if f, ok := args[i].check(at).num(at); ok {
				cost += int(min(math.Abs(f), maxSafeInt))
			}
		}
	case "replace":
		if s, ok := v.str(); ok && len(args) == 2 {
			p, _ := args[0].str()
			if growth := memSize(args[1]) - len(p); growth > 0 {
				cost += (strings.Count(s, p) + 1) * growth
			}
		}
	}
	r.charge(cost, at)
}

func (r *renderer) charge(cost, at int) {
	if r.spent += cost; r.spent > r.limits.Memory {
		bail(at, "memory-limit", "filters over the memory limit of %d", r.limits.Memory)
	}
}

// memSize is at least the length liquidjs charges for a value: a string's UTF-16 length
// (no more than its bytes) but 1 for ”, which array filters wrap in a one-item array,
// an array's length plus its items', and a bound on the length a number, boolean or
// object prints as.
func memSize(v val) int {
	switch v.lit {
	case litStr:
		return max(len(v.s), 1)
	case litNum:
		return 24
	}
	return dataSize(v.x)
}

func dataSize(x any) int {
	switch x := x.(type) {
	case string:
		return max(len(x), 1)
	case []any:
		n := len(x)
		for _, e := range x {
			n += dataSize(e)
		}
		return n
	case nil, Undefined, nilLitT:
		return 0
	}
	return 24
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

// callFilter applies a registered filter, or reports that it asked for the built-in.
func (r *renderer) callFilter(fn FilterFunc, name string, v val, args []val, at int) (out val, builtin bool) {
	if fn == nil {
		bail(at, "nil-filter-func", "filter %q is registered without a function", name) // e.g. for Check only
	}
	f := Filter{Name: name, Input: v.data(), Args: make([]any, len(args)), Strict: r.strict}
	if isNilLit(v) {
		bail(at, "nil-literal-registered-filter", "the nil literal into a registered filter, which liquidjs passes as a Drop")
	}
	for i, a := range args {
		if isNilLit(a) {
			bail(at, "nil-literal-registered-filter", "the nil literal into a registered filter, which liquidjs passes as a Drop")
		}
		f.Args[i] = a.data()
	}
	x, err := fn(f)
	switch {
	case errors.Is(err, ErrBuiltin):
		if r.dialect != nil && r.dialect.NoBuiltinFilters {
			bail(at, "filter:"+name, "filter %q fell back to a built-in, and the dialect has none", name)
		}
		return val{}, true
	case errors.Is(err, ErrUnchanged):
		return v, false
	}
	if err != nil {
		panic(bailout{hookErr("filter", name, at, err)})
	}
	return val{x: x}, false
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
	d := val{x: Undefined{}} // the JavaScript default parameter
	if len(args) == 1 {
		d = args[0]
	}
	if s, ok := v.str(); ok {
		if s == "" {
			return d
		}
		return v
	}
	if v.lit == litNum || isComparer(v) {
		return v
	}
	switch x := v.check(at).x.(type) {
	case nil, Undefined, nilLitT:
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
	if s, ok := comparerString(v); ok {
		return append(dst, s...)
	}
	if v.lit == litNum {
		return appendJSNumber(dst, v.n)
	}
	switch x := v.check(at).x.(type) {
	case nil, Undefined, nilLitT:
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
		bail(at, "stringify-object", "stringify of an object")
	}
	f, _ := v.num(at)
	return appendJSNumber(dst, f)
}

// capitalize mirrors JavaScript's s.charAt(0).toUpperCase() + s.slice(1).toLowerCase(),
// bailing on characters whose JavaScript mapping Go's simple case tables don't match.
// It returns s itself when nothing changes, which is common for names.
func capitalize(s string, at int) string {
	if s == "" {
		return ""
	}
	first, n := utf8.DecodeRuneInString(s)
	up := first
	if first <= 0xFFFF { // above, charAt(0) is half a surrogate pair, which has no case
		if jsUpperDiffers(first) {
			bail(at, "case-mapping", "capitalize of %q, whose JavaScript upper case differs", first)
		}
		up = unicode.ToUpper(first)
	}
	same := up == first
	for _, c := range s[n:] {
		if jsLowerDiffers(c) {
			bail(at, "case-mapping", "capitalize of %q, whose JavaScript lower case differs", c)
		}
		same = same && unicode.ToLower(c) == c
	}
	if same {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + utf8.UTFMax)
	b.WriteRune(up)
	for _, c := range s[n:] {
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}

// jsUpperDiffers covers full-Unicode expansions (ß→SS, ligatures, polytonic Greek) and
// characters whose case mappings Go's and Node's Unicode versions disagree on (Go 1.26
// is behind Node 22, Go 1.27 ahead). TestCaseMapping checks every code point against Node.
func jsUpperDiffers(c rune) bool {
	switch {
	case c == 0xDF, c == 0x149, c == 0x19B, c == 0x1F0, c == 0x264, c == 0x390, c == 0x3B0,
		c == 0x587, c == 0x1C8A:
		return true
	case c >= 0x1E96 && c <= 0x1E9A, c >= 0x1F50 && c <= 0x1FFC, c >= 0xA7CB && c <= 0xA7DC, c >= 0xFB00 && c <= 0xFB17,
		c >= 0x10D70 && c <= 0x10D85, c >= 0x16EBB && c <= 0x16ED3:
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
