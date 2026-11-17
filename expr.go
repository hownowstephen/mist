package mist

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func (r *renderer) setSrc(b, e int) { r.src, r.base, r.p = r.tpl[b:e], b, 0 }

func (r *renderer) pos() int { return r.base + r.p }

func (r *renderer) peek() byte {
	if r.p < len(r.src) {
		return r.src[r.p]
	}
	return 0
}

// ws skips ASCII whitespace and U+00A0, both in liquidjs's BLANK class.
// ponytail: its other Unicode blanks bail; decoding them here would stop ws inlining.
func (r *renderer) ws() {
	s, i := r.src, r.p
	for i < len(s) {
		if c := s[i]; isBlank(c) {
			i++
		} else if c == 0xC2 && i+1 < len(s) && s[i+1] == 0xA0 {
			i += 2
		} else {
			break
		}
	}
	r.p = i
}

func (r *renderer) end() {
	r.ws()
	if r.p < len(r.src) {
		bail(r.pos(), "unexpected-token", "unexpected %q", r.src[r.p:])
	}
	if r.undef.set {
		panic(bailout{&Error{Kind: ErrUndefined, Code: "undefined", Pos: r.undef.pos, Msg: r.undef.path, Path: r.undef.names()}})
	}
}

func isIdentStart(c byte) bool { return identByte[c] == 2 }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentChar(c byte) bool { return identByte[c] != 0 }

// wordRune is the length of the non-ASCII rune at s[i] when liquidjs counts it as a
// word character, as it does every one but its Unicode blanks and curly double quotes;
// else 0.
func wordRune(s string, i int) int {
	if i >= len(s) || s[i] < utf8.RuneSelf {
		return 0
	}
	c, n := utf8.DecodeRuneInString(s[i:])
	switch {
	case c == utf8.RuneError && n == 1:
		return 0 // invalid UTF-8 ends the identifier, and the parse fails where it stops
	case c == 0xa0, c == 0x1680, c == 0x180e, c >= 0x2000 && c <= 0x200a, c == 0x2028, c == 0x2029, c == 0x202f, c == 0x205f, c == 0x3000, c == 0x201c, c == 0x201d:
		return 0
	}
	return n
}

// identEnd is where the run of liquidjs word characters starting at s[i] ends.
func identEnd(s string, i int) int {
	for i < len(s) {
		if isIdentChar(s[i]) {
			i++
		} else if n := wordRune(s, i); n > 0 {
			i += n
		} else {
			break
		}
	}
	return i
}

// identByte is 2 for bytes that start an identifier and 1 for the rest of liquidjs's
// ASCII word characters (digits, '-' and '?').
var identByte = func() (t [256]uint8) {
	for c := range 256 {
		switch {
		case c == '_', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			t[c] = 2
		case c >= '0' && c <= '9', c == '-', c == '?':
			t[c] = 1
		}
	}
	return t
}()

// ident = ( letter | "_" | uword ) { letter | digit | "_" | "-" | "?" | uword }
func (r *renderer) ident() string {
	i := r.p
	if i >= len(r.src) || !isIdentStart(r.src[i]) && wordRune(r.src, i) == 0 {
		return ""
	}
	i = identEnd(r.src, i)
	s := r.src[r.p:i]
	r.p = i
	return s
}

// fcond = value { filter }
// Returns false when not evaluating. Undefined variables are lenient (lenientIf).
func (r *renderer) fcond(eval bool) bool {
	v, _, _ := r.value(eval, true, true)
	at := r.pos()
	if r.peek() == '|' {
		if k := v.keyword(); k != "" {
			bail(at, "keyword-outside-equality", "%s is only supported in conditions and output", k)
		}
		v = r.filters(v, eval) // liquidjs filters the expression's value
	}
	r.end()
	return eval && truthy(v, at)
}

// operand is a value on value's stack, with where it starts.
type operand struct {
	v  val
	at int
}

// operator is an operator on value's stack, with where it ends.
type operator struct {
	op string
	at int
}

// precedence is liquidjs's operator precedence.
func precedence(op string) int {
	switch op {
	case "and", "or":
		return 0
	case "not":
		return 1
	}
	return 2
}

// value = { op | expr }, read and evaluated as liquidjs's Tokenizer.readExpression and
// Expression do: operands and operators in any order, applied in postfix order, where an
// operator only pops operators of higher precedence, so equal ones group right to left
// (a and b or c is a and (b or c)). An operator short of operands gets undefined. The
// result is the bottom of the stack, so an operand after a complete expression is
// evaluated and ignored. lone reports a single operand; end is where the first ends.
// Anything outside cond (in a condition) or a lone operand (elsewhere) bails when the
// dialect rejects LooseExpressions.
func (r *renderer) value(eval, lenient, inCond bool) (v val, lone bool, end int) {
	start := r.pos()
	r.ws()
	if p := r.p; r.op() != "" || !r.valueStart() {
		r.p = p
		return r.operands(eval, lenient, inCond, start, p, nil)
	}
	first := operand{at: r.pos()}
	first.v = r.expr(eval, lenient)
	end = r.p
	if r.ws(); r.p == len(r.src) || r.peek() == '|' {
		return first.v, true, end
	}
	r.p = end
	return r.operands(eval, lenient, inCond, start, end, &first)
}

// operands is value from its first operand, if read.
func (r *renderer) operands(eval, lenient, inCond bool, start, end int, first *operand) (v val, lone bool, _ int) {
	var vb [4]operand
	var ob [4]operator
	vals, ops := vb[:0], ob[:0]
	// cond's grammar: 0 wants an operand, 1 an operator, 2 a compared operand, 3 and/or.
	state, strict, n := 0, true, 0
	lone = true
	if first != nil {
		vals, state, n = append(vals, *first), 1, 1
	}
	for {
		before := r.p
		r.ws()
		if op := r.op(); op != "" {
			if r.p-len(op) == before && n > 0 && precedence(op) == 2 && r.rejects(UnspacedOperators) {
				bail(r.pos(), "dialect-unspaced-operator", "operators without whitespace before them are rejected by the dialect")
			}
			for len(ops) > 0 && precedence(ops[len(ops)-1].op) > precedence(op) {
				vals = r.apply(vals, ops[len(ops)-1], eval)
				ops = ops[:len(ops)-1]
			}
			ops = append(ops, operator{op, r.pos()})
			strict = strict && op != "not"
			lone = false
			switch {
			case op == "and" || op == "or":
				strict = strict && (state == 1 || state == 3)
				state = 0
			default:
				strict = strict && state == 1
				state = 2
			}
			continue
		}
		if !r.valueStart() {
			break
		}
		at := r.pos()
		x := r.expr(eval, lenient)
		if n == 0 {
			end = r.p
		}
		n++
		vals = append(vals, operand{x, at})
		switch state {
		case 0:
			state = 1
		case 2:
			state = 3
		default:
			strict = false
		}
	}
	if n == 0 && len(ops) == 0 {
		bail(start, "expected-expression", "expected expression")
	}
	for len(ops) > 0 {
		vals = r.apply(vals, ops[len(ops)-1], eval)
		ops = ops[:len(ops)-1]
	}
	lone = lone && n == 1
	if loose := !lone && (!inCond || !strict || state == 0 || state == 2); loose && r.rejects(LooseExpressions) {
		bail(start, "dialect-loose-expression", "an expression outside the dialect's grammar")
	}
	return vals[0].v, lone, end
}

// strictLoose bails where a strict undefined met an operator ahead of filters: a leading
// default would make liquidjs read it as null, which compares unlike undefined.
func (r *renderer) strictLoose(lone bool, at int) {
	if !lone && r.undef.set {
		if r.ws(); r.peek() == '|' {
			bail(at, "strict-loose-default", "an undefined variable in an expression with operators, before filters, under strict")
		}
	}
}

// valueStart reports whether expr can start here.
func (r *renderer) valueStart() bool {
	switch c := r.peek(); {
	case c == '"', c == '\'', c == '-', c == '+', c == '(', isDigit(c), isIdentStart(c):
		return true
	}
	return wordRune(r.src, r.p) > 0
}

// apply pops o's operands from vals, an undefined for each missing one, and pushes
// its result.
func (r *renderer) apply(vals []operand, o operator, eval bool) []operand {
	b, vals := pop(vals, o.at)
	if o.op == "not" {
		return append(vals, operand{val{x: eval && !truthy(b.v, o.at)}, b.at})
	}
	a, vals := pop(vals, o.at)
	var res bool
	switch o.op {
	case "and":
		res = eval && truthy(a.v, o.at) && truthy(b.v, o.at)
	case "or":
		res = eval && (truthy(a.v, o.at) || truthy(b.v, o.at))
	default:
		res = r.cmp(o.op, a, b, o.at, eval)
	}
	return append(vals, operand{val{x: res}, a.at})
}

func pop(vals []operand, at int) (operand, []operand) {
	if len(vals) == 0 {
		return operand{val{x: Undefined{}}, at}, vals
	}
	return vals[len(vals)-1], vals[:len(vals)-1]
}

// cmp evaluates a op b.
func (r *renderer) cmp(op string, l, rt operand, at int, eval bool) bool {
	a, b, start := l.v, rt.v, l.at
	if k, other := a.keyword(), b; k != "" || b.keyword() != "" {
		if k == "" {
			k, other = b.keyword(), a
		}
		switch {
		case op == "contains":
			bail(start, "keyword-outside-equality", "%s is only supported with ==, !=, <, >, <= and >=", k)
		case op != "==" && op != "!=":
			if r.dialect == nil || r.dialect.Compare == nil {
				return false // liquidjs's EmptyDrop orders before and after nothing
			}
		case other.keyword() != "" || isNilLit(other):
			bail(start, "keyword-against-keyword", "%s compared with blank, empty or nil", k) // liquidjs is asymmetric here
		}
	}
	if op == "contains" {
		if r.dialect != nil && r.dialect.Contains != nil {
			return eval && r.dialectContains(a, b, at)
		}
		if isNilLit(b) {
			bail(at, "contains-nil", "contains nil") // liquidjs searches for "null"
		}
		if r.dialect != nil && r.dialect.Compare != nil {
			bail(at, "dialect-contains", "contains with a dialect Compare hook but no Contains hook")
		}
		return eval && contains(a, b, at)
	}
	if c, ok := a.x.(Comparer); eval && ok && a.lit == 0 {
		return valueCompare(c, op, b, at)
	}
	if c, ok := b.x.(Comparer); eval && ok && b.lit == 0 {
		return valueCompare(c, mirror[op], a, at)
	}
	if eval && r.dialect != nil && r.dialect.Compare != nil {
		return r.dialectCompare(op, a, b, at)
	}
	return eval && compare(op, a, b, at)
}

// op reads an operator, as liquidjs's readOperator does: a word operator needs a
// non-word character after it.
func (r *renderer) op() string {
	rest := r.src[r.p:]
	if rest == "" {
		return ""
	}
	var op string
	switch rest[0] {
	case '=':
		op = "=="
	case '!':
		op = "!="
	case '<':
		op = "<"
		if len(rest) > 1 && rest[1] == '=' {
			op = "<="
		}
	case '>':
		op = ">"
		if len(rest) > 1 && rest[1] == '=' {
			op = ">="
		}
	case 'c':
		op = "contains"
	case 'a':
		op = "and"
	case 'o':
		op = "or"
	case 'n':
		op = "not"
	default:
		return ""
	}
	if !strings.HasPrefix(rest, op) {
		return ""
	}
	if c := op[0]; c >= 'a' && c <= 'z' && (len(rest) > len(op) && isIdentChar(rest[len(op)]) || wordRune(rest, len(op)) > 0) {
		return "" // a word: containsx is an operand
	}
	r.p += len(op)
	return op
}

// expr = path | string | number | range | "true" | "false" | "nil" | "null" | "blank" | "empty"
func (r *renderer) expr(eval, lenient bool) val {
	r.ws()
	at := r.pos()
	switch c := r.peek(); {
	case c == '"' || c == '\'':
		return val{s: r.str(), lit: litStr}
	case c == '-' || c == '+' || isDigit(c):
		start := r.p
		n := r.number()
		if r.dialect != nil && r.dialect.NumberText {
			return val{n: n, s: r.src[start:r.p], lit: litNum}
		}
		return val{n: n, lit: litNum}
	case c == '(':
		return val{x: r.rangeLit(eval)}
	case isIdentStart(c) || wordRune(r.src, r.p) > 0:
		save := r.p
		var v any
		switch r.ident() {
		case "true":
			v = true
		case "false":
			v = false
		case "nil", "null":
			v = nilLit
		case "blank":
			if r.rejects(BlankKeyword) {
				bail(at, "dialect-blank", "blank is rejected by the dialect")
			}
			v = blankLit
		case "empty":
			if r.rejects(EmptyKeyword) {
				bail(at, "dialect-empty", "empty is rejected by the dialect")
			}
			v = emptyLit
		default:
			r.p = save
			return val{x: r.path(eval, lenient)}
		}
		if c := r.peek(); c == '.' || c == '[' {
			bail(at, "literal-property", "property access on a literal")
		}
		return val{x: v}
	}
	bail(at, "expected-expression", "expected expression")
	return val{}
}

func (r *renderer) str() string {
	at := r.pos()
	end := quotedEnd(r.src, r.p)
	if end < 0 {
		bail(at, "unterminated-string", "unterminated string")
	}
	s := r.src[r.p+1 : end-1]
	r.p = end
	if strings.IndexByte(s, '\\') < 0 {
		return s
	}
	return unescape(s, at)
}

// quotedEnd returns the index just past the quote closing the string at s[i], skipping
// backslash escapes as liquidjs's readQuoted does, or -1.
func quotedEnd(s string, i int) int {
	k := strings.IndexByte(s[i+1:], s[i])
	if k < 0 {
		return -1
	}
	if strings.IndexByte(s[i+1:i+1+k], '\\') < 0 {
		return i + k + 2
	}
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case s[i]:
			return j + 1
		}
	}
	return -1
}

// unescape is liquidjs's parseStringLiteral: \b \f \n \r \t \v, \u with up to 4 hex
// digits, up to 3 octal digits, and any other escaped character as itself.
func unescape(s string, at int) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if k := strings.IndexByte("bfnrtv", s[i]); k >= 0 {
			b.WriteByte("\b\f\n\r\t\v"[k])
			continue
		}
		base, maxDigits, j := 8, 3, i
		switch {
		case s[i] == 'u':
			base, maxDigits, j = 16, 4, i+1
		case s[i] < '0' || s[i] > '7':
			_, n := utf8.DecodeRuneInString(s[i:])
			b.WriteString(s[i : i+n])
			i += n - 1
			continue
		}
		c, k := rune(0), j
		for ; k < len(s) && k < j+maxDigits; k++ {
			d := strings.IndexByte("0123456789abcdef", s[k]|0x20)
			if d < 0 || d >= base || s[k] < '0' { // |0x20 maps 0x10-0x19 onto the digits
				break
			}
			c = c*rune(base) + rune(d)
		}
		if utf16.IsSurrogate(c) {
			bail(at, "string-escape-surrogate", "string escape of half a surrogate pair")
		}
		b.WriteRune(c)
		i = k - 1
	}
	return b.String()
}

// number = [ "-" | "+" ] digit { digit } [ "." { digit } ]
func (r *renderer) number() float64 {
	at, i := r.p, r.p
	if c := r.src[i]; c == '-' || c == '+' {
		if c == '-' && r.rejects(NegativeLiterals) {
			bail(r.base+at, "dialect-negative-literal", "negative literals are rejected by the dialect")
		}
		i++
	}
	j := i
	for j < len(r.src) && isDigit(r.src[j]) {
		j++
	}
	if j > i && j+1 < len(r.src) && r.src[j] == '.' && r.src[j+1] != '.' || j > i && j+1 == len(r.src) && r.src[j] == '.' {
		j++
		for j < len(r.src) && isDigit(r.src[j]) {
			j++
		}
	}
	rangeDots := strings.HasPrefix(r.src[j:], "..")
	if j == i || j < len(r.src) && !rangeDots && (r.src[j] == '.' || isIdentChar(r.src[j]) || r.src[j] >= utf8.RuneSelf && !r.blankAt(j)) {
		bail(r.base+at, "malformed-number", "malformed number literal")
	}
	f, err := strconv.ParseFloat(r.src[at:j], 64)
	if err != nil {
		bail(r.base+at, "malformed-number", "malformed number literal")
	}
	r.p = j
	return f
}

func (r *renderer) blankAt(i int) bool {
	return strings.HasPrefix(r.src[i:], "\u00a0")
}

// path = ident { "." ws word | "[" ws expr ws "]" }
func (r *renderer) path(eval, lenient bool) any {
	start := r.p
	name := r.ident()
	var kb [8]val // the keys so far, for ErrUndefined's Path
	keys := kb[:0]
	track := eval && r.strict
	if track {
		keys = append(keys, strVal(name))
	}
	var v any
	switch {
	case name == "forloop":
		v = r.forloop(eval, start)
		if track {
			keys = append(keys, strVal(r.src[start+len("forloop."):r.p])) // the property forloop read
		}
	case reserved(name):
		bail(r.base+start, "reserved-word-variable", "%q is not supported as a variable", name)
	case eval:
		v = r.root(name, start)
	}
	ruby := eval && r.dialect != nil && r.dialect.RubyLookups
	falsyRoot := ruby && (v == nil || v == false)
	depth := 0
	for {
		if eval && r.strict && v == (Undefined{}) {
			if lenient {
				v = nil // liquidjs catches the strict error and substitutes null
				r.lenientUndef = true
			} else if !r.undef.set {
				// Raised by end(): a trailing filter such as `| default` makes liquidjs lenient.
				r.undef = newPendingUndef(r.base+start, r.src[start:r.p], keys)
			}
		}
		switch r.peek() {
		case '.':
			if strings.HasPrefix(r.src[r.p:], "..") {
				return v // a range's dots end the path, as in liquidjs
			}
			r.p++
			r.ws() // liquidjs skips blanks before a property name
			at := r.pos()
			src, i := r.src, r.p
			j := identEnd(src, i)
			r.p = j
			seg := src[i:j]
			if seg == "" || j < len(src) && src[j] >= utf8.RuneSelf && !r.blankAt(j) {
				bail(at, "expected-property-name", "expected property name")
			}
			if eval {
				v = segment(v, val{s: seg, lit: litStr}, at, ruby, falsyRoot, depth)
			}
			depth++
			if track {
				keys = append(keys, strVal(seg))
			}
		case '[':
			r.p++
			r.ws()
			at := r.pos()
			k := r.expr(eval, false)
			if k.keyword() != "" {
				bail(at, "keyword-index", "%s as an index", k.keyword())
			}
			if eval && r.strict && isUndef(k) {
				// liquidjs evaluates index keys strictly, even where the path itself is lenient.
				e := &Error{Kind: ErrUndefined, Code: "undefined", Pos: at, Msg: r.src[at-r.base : r.p]}
				if r.undef.set && r.undef.pos == at {
					e.Path = r.undef.names()
				}
				panic(bailout{e})
			}
			r.ws()
			if r.peek() != ']' {
				bail(r.pos(), "expected-bracket", "expected ]")
			}
			if eval {
				v = segment(v, k, at, ruby, falsyRoot, depth)
			}
			depth++
			if track {
				keys = append(keys, k)
			}
			r.p++
		default:
			if r.undef.set && r.undef.pos == r.base+start {
				r.undef.path = r.src[start:r.p] // name the whole path, not just the undefined prefix
			}
			return v
		}
	}
}

// segment is the value of a path's next segment: key's, or with ruby (Dialect.RubyLookups)
// Ruby Liquid's, where a nil or false first segment is the whole lookup's value and past
// it a property or index of nil or false is undefined.
func segment(v any, k val, at int, ruby, falsyRoot bool, depth int) any {
	switch {
	case falsyRoot:
		return v
	case ruby && depth > 0 && (v == nil || v == false):
		return Undefined{}
	}
	return key(v, k, at)
}

func (r *renderer) root(name string, at int) any {
	for i := len(r.binds) - 1; i >= 0; i-- {
		if r.binds[i].name == name {
			return r.binds[i].item
		}
	}
	for i := r.depth - 1; i >= 0; i-- {
		if f := r.frame(i); f.looping() && f.active && f.name == name {
			return f.item()
		}
	}
	if v, ok := r.assigns[name]; ok {
		return v
	}
	if v, ok := r.env[name]; ok {
		return v
	}
	if v, ok := r.vars[name]; ok {
		return v
	}
	if magic(name) {
		bail(r.base+at, "builtin-variable-name", "%q resolves to a liquidjs built-in", name)
	}
	return Undefined{}
}

// binding is an expression filter's item, which shadows every other variable of its name.
type binding struct {
	name string
	item any
}

// expValue evaluates exp as liquidjs's Value does for where_exp and the like (a condition's
// value, as in filtered conditions, then its filters) with name bound to item. Under strict,
// undefined variables throw unless the first filter is default.
func (r *renderer) expValue(eval bool, exp, name string, item any, at int) val {
	src, base, p, undef, hit := r.src, r.base, r.p, r.undef, r.lenientUndef
	r.exps++
	r.src, r.base, r.p, r.undef, r.lenientUndef = exp, at, 0, pendingUndef{}, false
	r.binds = append(r.binds, binding{name, item})
	v, _, _ := r.value(eval, true, true)
	if k := v.keyword(); k != "" {
		bail(at, "keyword-outside-equality", "%s is only supported in conditions and output", k)
	}
	lenient := false
	if r.peek() == '|' {
		save := r.p
		r.p++
		r.ws()
		lenient = r.ident() == "default"
		r.p = save
		v = r.filters(v, eval)
	}
	r.end()
	if r.lenientUndef && !lenient {
		panic(bailout{&Error{Kind: ErrUndefined, Code: "undefined", Pos: at, Msg: exp}})
	}
	r.binds = r.binds[:len(r.binds)-1]
	r.src, r.base, r.p, r.undef, r.lenientUndef = src, base, p, undef, hit
	r.exps--
	return v
}

func literal(name string) bool {
	switch name {
	case "true", "false", "nil", "null", "empty", "blank":
		return true
	}
	return false
}

// pendingUndef is a strict undefined variable not yet raised. It's kept unallocated
// because a leading default filter can still make it lenient.
type pendingUndef struct {
	set   bool
	pos   int
	path  string
	keys  [8]val // up to the first undefined one, for Error.Path
	nkeys int    // -1 when there were more than fit
}

func newPendingUndef(pos int, path string, keys []val) pendingUndef {
	u := pendingUndef{set: true, pos: pos, path: path}
	if u.nkeys = copy(u.keys[:], keys); len(keys) > len(u.keys) {
		u.nkeys = -1
	}
	return u
}

// names is the keys as liquidjs joins them into its undefined variable error, or nil when
// mist can't say: too many keys, or one with no JavaScript string mist reproduces.
func (u *pendingUndef) names() (names []string) {
	if !u.set || u.nkeys < 0 {
		return nil
	}
	defer func() {
		if e := recover(); e != nil {
			if _, ok := e.(bailout); !ok {
				panic(e)
			}
			names = nil
		}
	}()
	names = make([]string, u.nkeys)
	for i, k := range u.keys[:u.nkeys] {
		names[i] = jsString(k, u.pos)
	}
	return names
}

// reserved words that liquidjs parses as operators even where a variable is expected.
func reserved(name string) bool {
	return name == "contains" || name == "and" || name == "or" || name == "not"
}

// magic keys that liquidjs computes when the property is absent.
func magic(key string) bool { return key == "size" || key == "first" || key == "last" }

// rangeLit = "(" ws expr ws ".." ws expr ws ")" with integer bounds, liquidjs's range(+lo, +hi + 1).
func (r *renderer) rangeLit(eval bool) any {
	lo, n := r.rangeBounds(eval)
	if !eval {
		return nil
	}
	out := make([]any, n)
	for i := range out {
		out[i] = boxInt(lo + i)
	}
	return out
}

// rangeBounds parses a range literal and charges for its items, returning its first item and count.
func (r *renderer) rangeBounds(eval bool) (first, n int) {
	at := r.pos()
	r.p++
	lo := r.expr(eval, false)
	r.ws()
	if !strings.HasPrefix(r.src[r.p:], "..") {
		bail(r.pos(), "range-syntax", "expected .. in range")
	}
	r.p += 2
	hi := r.expr(eval, false)
	r.ws()
	if r.peek() != ')' {
		bail(r.pos(), "range-syntax", "expected ) after range")
	}
	r.p++
	if !eval {
		return 0, 0
	}
	a, b := rangeBound(lo, at), rangeBound(hi, at)
	if b-a >= maxRange {
		bail(at, "range-too-long", "range of more than %d items", maxRange)
	}
	if b >= a {
		r.charge(b-a+1, at) // liquidjs charges memoryLimit for the items
	}
	return a, max(0, b-a+1)
}

const maxRange = 100000

// rangeBound is an integer range bound; liquidjs's unary + reads other values differently from toNumber.
func rangeBound(v val, at int) int {
	f, ok := v.check(at).num(at)
	if !ok || f != math.Trunc(f) || math.Abs(f) >= maxSafeInt {
		bail(at, "range-bound", "range bound that isn't an integer")
	}
	return int(f)
}

func (f *frame) item() any {
	i := int(f.idx)
	if f.rev {
		i = f.len() - 1 - i
	}
	if f.coll != nil {
		return f.coll[i]
	}
	if f.x == nil {
		f.x = boxInt(int(f.n) + i)
	}
	return f.x
}

// forloop resolves forloop.<prop> against the innermost for, as liquidjs's ForloopDrop.
func (r *renderer) forloop(eval bool, start int) any {
	f := r.loop()
	if f == nil || r.peek() != '.' {
		bail(r.base+start, "forloop-outside-for", "forloop outside a for loop or without a property")
	}
	r.p++
	at := r.pos()
	p := r.ident()
	if p == "" {
		bail(at, "expected-property-name", "expected property name")
	}
	switch p {
	case "next", "valueOf", "toString", "toLocaleString", "constructor", "hasOwnProperty", "isPrototypeOf",
		"propertyIsEnumerable", "liquidMethodMissing", "__proto__", "__defineGetter__", "__defineSetter__",
		"__lookupGetter__", "__lookupSetter__":
		bail(at, "forloop-js-property", "forloop.%s is a JavaScript property of liquidjs's ForloopDrop", p)
	}
	if !eval {
		return nil
	}
	n, i := f.len(), int(f.idx)
	var v any
	switch p {
	case "index":
		v = boxInt(i + 1)
	case "index0", "i":
		v = boxInt(i)
	case "rindex":
		v = boxInt(n - i)
	case "rindex0":
		v = boxInt(n - i - 1)
	case "first":
		v = i == 0
	case "last":
		v = i == n-1
	case "length":
		v = boxInt(n)
	case "name":
		v = f.name + "-" + r.tpl[f.collStart:f.collEnd]
	default:
		v = Undefined{} // liquidjs 10.26 has no parentloop or other properties
	}
	return v
}

// loop is the innermost for frame, or nil.
func (r *renderer) loop() *frame {
	for i := r.depth - 1; i >= 0; i-- {
		if f := r.frame(i); f.looping() {
			return f
		}
	}
	return nil
}
