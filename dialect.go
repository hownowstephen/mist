package mist

// Dialect changes how values print and compare, and which constructs mist accepts,
// so an application can target a Liquid engine that differs from liquidjs. With a
// dialect set, matching that engine is up to the dialect's author.
type Dialect struct {
	// Output appends a printed non-string value: a number, bool, array or object.
	// Strings, nil and undefined never reach it.
	Output func(dst []byte, v any) ([]byte, error)

	// Compare evaluates ==, !=, <, >, <=, >= in conditions.
	Compare func(op string, a, b any) (bool, error)

	// Reject makes these constructs bail, in both Render and Check.
	Reject Constructs

	// NoDefaultLeniency turns off liquidjs's rule that a leading `| default`
	// makes an undefined input lenient under strict.
	NoDefaultLeniency bool
}

// Constructs is a set of template constructs a Dialect can reject.
type Constructs uint32

const (
	TrimMarkers       Constructs = 1 << iota // {{- -}} {%- -%}
	NegativeLiterals                         // -3, including negative indexes
	UnspacedOperators                        // a comparison operator with no whitespace before it: x==2
	RawBlocks                                // {% raw %}…{% endraw %}
	BlankKeyword                             // == blank, != blank
	EmptyKeyword                             // == empty, != empty
)

// Keyword is how a keyword literal reaches Dialect.Compare.
type Keyword string

// Blank and Empty are the blank and empty keywords as Dialect.Compare sees them.
const (
	Blank Keyword = "blank"
	Empty Keyword = "empty"
)

func (r *renderer) rejects(c Constructs) bool {
	return r.dialect != nil && r.dialect.Reject&c != 0
}

// hookValue is v as dialect hooks see it: nil for nil, undefined and the nil
// literal, int64 for integer literals, Blank and Empty for those keywords.
func (v val) hookValue() any {
	switch v.lit {
	case litStr:
		return v.s
	case litNum:
		return int64(v.n) // the grammar only has integer literals
	}
	switch v.x.(type) {
	case undefinedT, nilLitT:
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
		panic(bailout{wrapErr("dialect", "output", r.base, err)})
	}
	r.out = out
}

func (r *renderer) dialectCompare(op string, a, b val, at int) bool {
	ok, err := r.dialect.Compare(op, a.hookValue(), b.hookValue())
	if err != nil {
		panic(bailout{wrapErr("dialect", "compare", at, err)})
	}
	return ok
}
