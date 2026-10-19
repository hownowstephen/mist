package mist

import (
	"encoding/json"
	"math"
)

// Dialect changes how values print and compare, and which constructs mist accepts,
// so an application can target a Liquid engine that differs from liquidjs. With a
// dialect set, matching that engine is up to the dialect's author.
type Dialect struct {
	// Output appends a printed non-string value: a number, bool, array, object, or
	// the Blank or Empty keyword. Strings, nil and undefined never reach it.
	Output func(dst []byte, v any) ([]byte, error)

	// Compare evaluates ==, !=, <, >, <=, >= in conditions, and matches when values
	// against the case subject with ==.
	Compare func(op string, a, b any) (bool, error)

	// Contains evaluates a contains b in conditions. With Compare set and Contains
	// nil, contains bails.
	Contains func(a, b any) (bool, error)

	// Reject makes these constructs bail, in both Render and Check.
	Reject Constructs

	// NoDefaultLeniency turns off liquidjs's rule that a leading `| default`
	// makes an undefined input lenient under strict.
	NoDefaultLeniency bool

	// NumberText hands number literals to hooks and registered filters as
	// json.Number holding their text, so an engine that distinguishes 2.0 from 2
	// (Ruby's Float and Integer) sees which was written.
	NumberText bool

	// NoBuiltinFilters makes every filter that isn't in Engine.Filters bail,
	// built-ins included, for an engine whose filters all differ from liquidjs's.
	// A registered filter returning ErrBuiltin bails too.
	NoBuiltinFilters bool

	// StrictAssign makes an undefined variable in an assign's value ErrUndefined under
	// strict, as Ruby Liquid's strict_variables does; liquidjs reads it leniently.
	StrictAssign bool
}

// Constructs is a set of template constructs a Dialect can reject.
type Constructs uint32

// The constructs a Dialect can reject.
const (
	TrimMarkers       Constructs = 1 << iota // {{- -}} {%- -%}
	NegativeLiterals                         // -3, including negative indexes
	UnspacedOperators                        // a comparison operator with no whitespace before it: x==2
	RawBlocks                                // {% raw %}…{% endraw %}
	BlankKeyword                             // == blank, != blank
	EmptyKeyword                             // == empty, != empty
)

// Keyword is how a keyword literal reaches Dialect.Compare and Dialect.Output.
type Keyword string

// Blank and Empty are the blank and empty keywords as the hooks see them.
const (
	Blank Keyword = "blank"
	Empty Keyword = "empty"
)

func (r *renderer) rejects(c Constructs) bool {
	return r.dialect != nil && r.dialect.Reject&c != 0
}

// hookValue is v as dialect hooks see it: nil for nil, undefined and the nil
// literal, int64 for integral number literals and results, float64 for the rest,
// Blank and Empty for those keywords.
func (v val) hookValue() any {
	switch v.lit {
	case litStr:
		return v.s
	case litNum:
		if v.s != "" {
			return json.Number(v.s) // a literal under Dialect.NumberText
		}
		if v.n == math.Trunc(v.n) && math.Abs(v.n) < maxSafeInt {
			return int64(v.n)
		}
		return v.n
	}
	switch v.x.(type) {
	case Undefined, nilLitT:
		return nil
	case blankT:
		return Blank
	case emptyT:
		return Empty
	}
	return v.x
}

func (r *renderer) dialectOutput(v val) {
	out, err := r.dialect.Output(r.out, v.hookValue())
	if err != nil {
		panic(bailout{hookErr("dialect", "output", r.base, err)})
	}
	r.out = out
}

func (r *renderer) dialectCompare(op string, a, b val, at int) bool {
	ok, err := r.dialect.Compare(op, a.hookValue(), b.hookValue())
	if err != nil {
		panic(bailout{hookErr("dialect", "compare", at, err)})
	}
	return ok
}

// when matches a when value against the case subject, with the dialect's == unless
// either is a Comparer, as in conditions.
func (r *renderer) when(subject, v val, at int) bool {
	if r.dialect != nil && r.dialect.Compare != nil && !isComparer(subject) && !isComparer(v) {
		return r.dialectCompare("==", subject, v, at)
	}
	return eq(subject, v, at)
}

func (r *renderer) dialectContains(a, b val, at int) bool {
	ok, err := r.dialect.Contains(a.hookValue(), b.hookValue())
	if err != nil {
		panic(bailout{hookErr("dialect", "contains", at, err)})
	}
	return ok
}
