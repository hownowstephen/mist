package mist

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

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
		bail(at, "slice-begin", "slice without a begin")
	}
	if isNil(v) {
		return val{x: []any{}}
	}
	arr, isArr := v.check(at).x.([]any)
	var str string
	n := len(arr)
	if !isArr {
		str = toStr(v, at)
		n = len16(str)
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
	return strVal(sub16(str, i, j, at))
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
			bail(at, "first-last-empty", "first or last of an empty array, which is undefined")
		}
		if first {
			return val{x: arr[0]}
		}
		return val{x: arr[len(arr)-1]}
	}
	if s, ok := v.str(); ok && s != "" {
		if first {
			return strVal(sub16(s, 0, 1, at))
		}
		n := len16(s)
		return strVal(sub16(s, n-1, n, at))
	}
	if first {
		if isObj(v) {
			bail(at, "first-last-type", "first of an object")
		}
		return strVal("")
	}
	bail(at, "first-last-type", "last of %T", v.any())
	return val{}
}

// join is liquidjs's join: arrays join with sep (default " "), anything else is one element.
func join(v val, a []val, at int) string {
	sep := " "
	if len(a) > 0 && !isNil(a[0]) {
		sep = toStr(a[0], at)
	}
	if isNil(v) {
		return ""
	}
	arr, ok := v.check(at).x.([]any)
	if !ok || v.lit != 0 {
		return toStr(v, at)
	}
	var buf [128]byte
	dst := buf[:0]
	for i, e := range arr {
		if i > 0 {
			dst = append(dst, sep...)
		}
		if _, nested := e.([]any); nested {
			bail(at, "join-nested-array", "join of a nested array, which JavaScript joins with commas")
		}
		dst = stringify(dst, val{x: e}, at)
	}
	return string(dst)
}

// size is liquidjs's (v && v.length) || 0.
func size(v val, at int) val {
	if s, ok := v.str(); ok {
		if !utf8.ValidString(s) {
			bail(at, "invalid-utf8", "size of invalid UTF-8")
		}
		return val{n: float64(len16(s)), lit: litNum}
	}
	switch x := v.check(at).x.(type) {
	case []any:
		return val{n: float64(len(x)), lit: litNum}
	case map[string]any:
		if _, ok := x["length"]; ok {
			bail(at, "size-length-key", "size of an object with a length key")
		}
	}
	return val{n: 0, lit: litNum}
}

// toArray is liquidjs's toArray: nil is [], an array itself, anything else one item.
func toArray(v val, at int) []any {
	if isNil(v) {
		return nil
	}
	if a, ok := v.check(at).x.([]any); ok {
		return a
	}
	if _, isObj := v.x.(map[string]any); isObj {
		bail(at, "array-filter-object", "array filter on an object")
	}
	return []any{v.any()}
}

// argPath is the first argument as a dotted property path.
func argPath(a []val, at int) string {
	s, ok := a[0].str()
	if !ok || !propPath(s) {
		bail(at, "property-path", "property argument that isn't a plain path")
	}
	return s
}

// propPath accepts dotted identifiers, the property paths whose parse is unambiguous.
func propPath(s string) bool {
	for key := range strings.SplitSeq(s, ".") {
		if key == "" || literal(key) || reserved(key) || key[0] == '-' || key[0] >= '0' && key[0] <= '9' {
			return false
		}
		for _, c := range key {
			if c != '_' && c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

// lookupPath reads a dotted path off an item as liquidjs's _getFromScope does, without strict errors.
func lookupPath(item any, path string, at int, what string) any {
	for k := range strings.SplitSeq(path, ".") {
		if _, isObj := item.(map[string]any); !isObj {
			bail(at, "item-not-object", "%s of an item that isn't an object", what)
		}
		item = prop(item, k, at)
	}
	return item
}

// mapProp is liquidjs's map: each item's property, undefined where it's missing.
func mapProp(v val, path string, at int) []any {
	items := toArray(v, at)
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = lookupPath(it, path, at, "map")
	}
	return out
}

// sum is liquidjs's sum: Number of each item (or its property), NaN counting as 0.
func sum(v val, a []val, at int) val {
	var path string
	if len(a) > 0 && truthy(a[0], at) {
		path = argPath(a, at)
	}
	total := 0.0
	for _, it := range toArray(v, at) {
		if path != "" {
			it = lookupPath(it, path, at, "sum")
		}
		switch it.(type) {
		case map[string]any, []any:
			bail(at, "sum-type", "sum of an object or array")
		}
		total += toNumber(val{x: it}, at)
	}
	if math.IsInf(total, 0) {
		bail(at, "sum-non-finite", "sum of %v", total)
	}
	return val{n: total, lit: litNum}
}

// sortBy is liquidjs's sort: stable, nil last, otherwise JavaScript's < on all-number or
// all-string keys (strings without characters outside the BMP, so UTF-8 order is UTF-16 order).
func sortBy(v val, a []val, at int) []any {
	var path string
	if len(a) > 0 && truthy(a[0], at) {
		path = argPath(a, at)
	}
	items := toArray(v, at)
	type pair struct{ item, key any }
	pairs := make([]pair, len(items))
	kind := 0 // 1 numbers, 2 strings
	for i, it := range items {
		k := it
		if path != "" {
			k = lookupPath(it, path, at, "sort by")
		}
		switch x := k.(type) {
		case nil, Undefined:
		case float64:
			kind |= 1
		case string:
			kind |= 2
			for _, c := range x {
				if c > 0xFFFF {
					bail(at, "sort-astral", "sort of a string outside the BMP")
				}
			}
		default:
			f, ok := num(x, at)
			if !ok {
				bail(at, "sort-type", "sort of %T", k)
			}
			kind |= 1
			k = f
		}
		pairs[i] = pair{it, k}
	}
	if kind == 3 {
		bail(at, "sort-mixed", "sort of mixed numbers and strings")
	}
	slices.SortStableFunc(pairs, func(a, b pair) int { return orderedCompare(a.key, b.key) })
	out := make([]any, len(pairs))
	for i, p := range pairs {
		out[i] = p.item
	}
	return out
}

func orderedCompare(a, b any) int {
	an, bn := a == nil || a == (Undefined{}), b == nil || b == (Undefined{})
	switch {
	case an && bn:
		return 0
	case an:
		return 1
	case bn:
		return -1
	}
	if x, ok := a.(float64); ok {
		return cmpFloat(x, b.(float64))
	}
	return strings.Compare(a.(string), b.(string))
}

// svzKey is x as a map key that compares as JavaScript's SameValueZero does. Objects
// and arrays compare by reference in JavaScript, so they bail.
func svzKey(x any, what string, at int) any {
	switch x.(type) {
	case nil, bool, string, Undefined:
		return x
	case map[string]any, []any:
		bail(at, "same-value-object", "%s of an object or array", what)
	}
	f, ok := num(x, at)
	if !ok || math.IsNaN(f) {
		bail(at, "same-value-type", "%s of %T", what, x)
	}
	return f
}

// uniq is liquidjs's uniq: first occurrences by SameValueZero.
func uniq(v val, at int) []any {
	items := toArray(v, at)
	seen := make(map[any]bool, len(items))
	out := []any{}
	for _, it := range items {
		if k := svzKey(it, "uniq", at); !seen[k] {
			seen[k] = true
			out = append(out, it)
		}
	}
	return out
}

// sortNatural is liquidjs's sort_natural: sort with keys lower-cased by
// String.prototype.toLowerCase, which stringifies numbers and booleans first.
func (r *renderer) sortNatural(v val, a []val, at int) []any {
	var path string
	if len(a) > 0 && truthy(a[0], at) {
		path = argPath(a, at)
	}
	items := toArray(v, at)
	type pair struct {
		item any
		key  string
		nil  bool
	}
	pairs := make([]pair, len(items))
	for i, it := range items {
		k := it
		if path != "" {
			k = lookupPath(it, path, at, "sort_natural by")
		}
		p := pair{item: it, nil: isNil(val{x: k})}
		if !p.nil {
			switch k.(type) {
			case map[string]any, []any:
				bail(at, "sort-type", "sort_natural of an object or array")
			}
			p.key = caseMap(toStr(val{x: k}, at), false, at)
			for _, c := range p.key {
				if c > 0xFFFF {
					bail(at, "sort-astral", "sort_natural of a string outside the BMP")
				}
			}
		}
		pairs[i] = p
	}
	slices.SortStableFunc(pairs, func(x, y pair) int {
		switch {
		case x.nil && y.nil:
			return 0
		case x.nil:
			return 1
		case y.nil:
			return -1
		}
		return strings.Compare(x.key, y.key)
	})
	out := make([]any, len(pairs))
	for i, p := range pairs {
		out[i] = p.item
	}
	return out
}

// sentence is liquidjs's array_to_sentence_string: "a", "a and b", "a, b, and c".
func sentence(v val, a []val, at int) val {
	arr, ok := v.check(at).x.([]any)
	if !ok || v.lit != 0 {
		bail(at, "sentence-type", "array_to_sentence_string of %T", v.any()) // liquidjs indexes strings, or throws
	}
	conn := "and"
	if len(a) > 0 {
		conn = argOrDefault(a[0], conn, at)
	}
	switch len(arr) {
	case 0:
		return strVal("")
	case 1:
		return val{x: arr[0]}
	case 2:
		return strVal(jsString(val{x: arr[0]}, at) + " " + conn + " " + jsString(val{x: arr[1]}, at))
	}
	var b strings.Builder
	for i, e := range arr[:len(arr)-1] {
		if i > 0 {
			b.WriteString(", ")
		}
		if !isNil(val{x: e}) { // Array.prototype.join writes null and undefined as ""
			b.WriteString(jsString(val{x: e}, at))
		}
	}
	b.WriteString(", " + conn + " " + jsString(val{x: arr[len(arr)-1]}, at))
	return strVal(b.String())
}

// matching is liquidjs's where (and, with first, find): items whose property path is truthy,
// or equals expected.
func (r *renderer) matching(v val, a []val, at int, first, keep bool) (out []any, idx int) {
	path, ok := a[0].str()
	if !ok || !propPath(path) {
		bail(at, "property-path", "where property that isn't a plain path")
	}
	var items []any
	switch x := v.check(at).x.(type) {
	case nil, Undefined, nilLitT:
		if !isNil(v) {
			bail(at, "where-input", "where on a scalar")
		}
	case []any:
		items = x
	case map[string]any:
		items = []any{x}
	default:
		bail(at, "where-input", "where on %T", v.any())
	}
	matchTruthy := len(a) == 1
	if !matchTruthy {
		_, undef := a[1].x.(Undefined)
		matchTruthy = undef && a[1].lit == 0 // liquidjs's expected === undefined
	}
	out, idx = []any{}, -1
	for i, it := range items {
		x := r.itemPath(it, path, at)
		if (matchTruthy && truthy(val{x: x}, at) || !matchTruthy && eq(val{x: x}, a[1], at)) == keep {
			out = append(out, it)
			if first {
				return out, i
			}
		}
	}
	return out, idx
}

// itemPath reads a dotted path off item as liquidjs's evalToken on a scope spawned from it does.
func (r *renderer) itemPath(item any, path string, at int) any {
	if _, isMap := item.(map[string]any); !isMap && item != nil {
		if first, _, _ := strings.Cut(path, "."); magic(first) {
			// liquidjs scopes the lookup to the item, where a string's or array's size, first
			// and last aren't computed.
			bail(at, "where-computed-key", "where by %s on an item that isn't an object", first)
		}
	}
	x, strict := item, r.strict // a range-over-func body capturing r moves the renderer to the heap before Go 1.25
	for key := range strings.SplitSeq(path, ".") {
		if strict && isNil(val{x: x}) {
			bail(at, "where-strict-missing", "where on an item missing %q under strict", path) // liquidjs throws
		}
		x = prop(x, key, at)
	}
	if _, undef := x.(Undefined); undef && r.strict {
		bail(at, "where-strict-missing", "where on an item missing %q under strict", path)
	}
	return x
}

// checkExp requires literal arguments for where_exp, find_exp and group_by_exp, so Check
// can parse the expression: an item name that reads back as a variable, and the expression.
func (r *renderer) checkExp(a []val, at int) {
	name := a[0].s
	if a[0].lit != litStr || a[1].lit != litStr || name == "" || !isIdentStart(name[0]) || literal(name) || reserved(name) || name == "forloop" {
		bail(at, "expression-filter-arguments", "expression filter arguments other than an item name and an expression as string literals")
	}
	for i := range len(name) {
		if !isIdentChar(name[i]) {
			bail(at, "expression-filter-arguments", "expression filter item name %q", name)
		}
	}
	r.expValue(false, a[1].s, name, nil, at)
}

// groupBy is liquidjs's group_by and group_by_exp: {name, items} for each distinct key, in
// order of appearance.
func (r *renderer) groupBy(v val, a []val, exp bool, at int) []any {
	var items []any
	switch x := v.check(at).x.(type) {
	case []any:
		items = x
	case map[string]any:
		bail(at, "group-by-object", "group_by of an object") // liquidjs groups its entries
	}
	if s, ok := v.str(); ok && s != "" {
		items = []any{s}
	}
	var path string
	if !exp {
		path = argPath(a, at)
	}
	idx := map[any]int{}
	var groups []map[string]any
	for _, it := range items {
		var k any
		if exp {
			k = r.expValue(true, a[1].s, a[0].s, it, at).any()
		} else {
			k = r.itemPath(it, path, at)
		}
		sk := svzKey(k, "group_by", at)
		i, ok := idx[sk]
		if !ok {
			i = len(groups)
			idx[sk] = i
			groups = append(groups, map[string]any{"name": k, "items": []any{}})
		}
		groups[i]["items"] = append(groups[i]["items"].([]any), it)
	}
	out := make([]any, len(groups))
	for i, g := range groups {
		out[i] = g
	}
	return out
}
