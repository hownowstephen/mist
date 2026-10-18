package mist

import (
	"math"
	"strings"
)

var mirror = map[string]string{"==": "==", "!=": "!=", "<": ">", ">": "<", "<=": ">=", ">=": "<="}

func valueCompare(c Comparer, op string, other val, at int) bool {
	ok, err := c.Compare(op, other.hookValue())
	if err != nil {
		panic(bailout{hookErr("value", "compare", at, err)})
	}
	return ok
}

func compare(op string, a, b val, at int) bool {
	switch op {
	case "==":
		return eq(a, b, at)
	case "!=":
		return !eq(a, b, at)
	}
	var c int
	x, xNum := a.num(at)
	y, yNum := b.num(at)
	xs, xStr := a.str()
	ys, yStr := b.str()
	// JavaScript compares a number and a string as numbers, reading the string as Number does.
	switch {
	case xNum && yStr:
		y, yNum = jsNumberOf(b, at), true
	case xStr && yNum:
		x, xNum = jsNumberOf(a, at), true
	}
	switch {
	case xNum && yNum:
		if math.IsNaN(x) || math.IsNaN(y) {
			return false
		}
		c = cmpFloat(x, y)
	case xNum:
		bail(at, "ordering-types", "%s between number and %T", op, b.any())
	case xStr && yStr && ascii(xs) && ascii(ys):
		c = strings.Compare(xs, ys)
	default:
		bail(at, "ordering-types", "%s needs two numbers, two ASCII strings, or a number and a string", op)
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
	if c, ok := a.x.(Comparer); ok && a.lit == 0 {
		return valueCompare(c, "==", b, at)
	}
	if c, ok := b.x.(Comparer); ok && b.lit == 0 {
		return valueCompare(c, "==", a, at)
	}
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
	if isNilLit(a) || isNilLit(b) {
		return isNil(a) && isNil(b)
	}
	if isObj(a) || isObj(b) {
		return objEq(a, b, at)
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

// objEq is liquidjs's equals with an array or object operand: arrays compare item by item,
// anything else by reference.
func objEq(a, b val, at int) bool {
	x, xArr := a.x.([]any)
	y, yArr := b.x.([]any)
	switch {
	case xArr && yArr:
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !eq(val{x: x[i]}, val{x: y[i]}, at) {
				return false
			}
		}
		return true
	case isObj(a) && isObj(b) && !xArr && !yArr:
		bail(at, "object-equality", "== between two objects") // JavaScript compares references
	}
	return false
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
	case nil, Undefined:
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

// contains mirrors liquidjs: array elements by ===, substrings of a string (the right side
// coerced as indexOf does), and false for anything else.
func contains(a, b val, at int) bool {
	if isComparer(a) {
		return false // an object with no indexOf contains nothing
	}
	a = a.check(at)
	if !isComparer(b) {
		b = b.check(at)
	}
	if isObj(b) {
		bail(at, "contains-object", "contains with an object or array on the right")
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
	switch undef := isUndef(b); {
	case b.lit != 0 || b.x != nil && !undef:
		needle = toStr(b, at)
	case undef:
		needle = "undefined"
	}
	return strings.Contains(s, needle)
}
