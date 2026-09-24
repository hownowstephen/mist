# mist Liquid subset — v0

_Last updated 2026-09-23. Parity target: liquidjs 10.16.1 with `new Liquid({ lenientIf: true })`, the render service configuration._

**Contract.** For every template and data where mist returns output or `ErrUndefined`, liquidjs returns the same output or an undefined-variable error. Anything else returns `ErrUnsupported`, and the caller renders with the full engine. Bailing is always safe, so when in doubt, the spec bails.

**Checking a template:** `go run ./cmd/mistcheck FILE...` or `mist.Check(tpl)`. The checker is the renderer with evaluation switched off, so it cannot drift from this document's grammar. A template that passes can still bail at render time on the [runtime bails](#runtime-bails) below.

## Grammar

LL(1) EBNF. Each production is one function in `render.go`/`expr.go`. Anything that doesn't derive from `template` is out of spec.

```ebnf
template = { text | output | tag } ;
text     = ? bytes up to the next "{{" or "{%" ? ;
output   = "{{" [ "-" ] ws expr ws [ "-" ] "}}" ;
tag      = "{%" [ "-" ] ws tagbody ws [ "-" ] "%}" ;

tagbody  = "if" cond | "elsif" cond | "else" | "endif"
         | "unless" cond | "endunless"
         | "for" ident "in" path | "endfor"
         | "assign" ident "=" expr
         | "comment" | "endcomment"
         | "raw" | "endraw" ;

cond     = cmp { "and" cmp } | cmp { "or" cmp } ;      (* one connective per condition *)
cmp      = expr [ op expr ] ;
op       = "==" | "!=" | "<=" | ">=" | "<" | ">" ;
expr     = path | string | int | "true" | "false" | "nil" | "null" ;
path     = ident { "." ident | "[" int "]" | "[" string "]" } ;   (* no spaces inside *)
ident    = ( letter | "_" ) { letter | digit | "_" | "-" } ;      (* ASCII only *)
string   = "'" { ? any but ' or \ ? } "'" | '"' { ? any but " or \ ? } '"' ;
int      = [ "-" ] digit { digit } ;                              (* |n| < 2^53 *)
ws       = { " " | "\t" | "\n" | "\v" | "\f" | "\r" } ;
```

Structural rules the EBNF doesn't express:
- Blocks must nest and close: `if…endif` and `unless…endunless`, each with optional `elsif`s and at most one final `else`, and `for…endfor` with no `else`. The maximum nesting depth is 16.
- `comment … endcomment`: the body is tokenized but ignored. A nested `comment` or `raw` bails.
- `raw … endraw`: the body is emitted verbatim. Trim markers on either tag bail.
- Tags end at the first `%}`, even inside quotes, as in liquidjs. Outputs end at the first `}}` outside quotes.
- Dead branches are parsed, not skipped. A syntax error or unknown tag anywhere bails, because liquidjs rejects the whole template.

## Semantics

| Topic | Rule |
|---|---|
| Truthiness | Only `false`, `nil`/`null`, and undefined are falsy. `""`, `0`, `[]` and `{}` are truthy. |
| Lookup order | Innermost `for` variable, then `assign`ed names, then data. |
| Undefined in `{{ }}` or a `for` collection | Strict: `ErrUndefined`. Lax: renders `""` / zero iterations. |
| Undefined in `if`/`elsif`/`unless`/`assign` | Never an error (`lenientIf`); the value is undefined. |
| Null in a path | `a.b.c` with `a` null is null. Never an error, even under strict. |
| Missing key / out-of-range index | Undefined. Negative indexes count from the end. |
| Output: string, bool, nil | As-is; `true`/`false`; `""`. |
| Output: number | Integral with \|n\| < 2^53: decimal digits. |
| `assign` | Writes the render's scope, so it is visible after an enclosing `for` and never visible to other chain steps. An undefined value is stored as nil. |
| `for` | Arrays only. Null or undefined (lax) means zero iterations. The loop variable shadows and is restored after `endfor`. |
| `==` / `!=` | Same-type scalars compare by value (numbers numerically). Different types are never equal. `nil` matches both null and undefined, but a null variable ≠ an undefined variable. |
| `<` `>` `<=` `>=` | Two numbers, or two ASCII strings (byte order). |
| `and` / `or` | Evaluated as written. Mixing them bails, which sidesteps Liquid's right-to-left associativity. |
| Whitespace control | `{{-`/`{%-` trim the template text before; `-}}`/`-%}` trim the template text after. Rendered values are never trimmed. The trimmed set is ASCII whitespace plus U+00A0, U+1680, U+180E, U+2000–200A, U+2028, U+2029, U+202F, U+205F, U+3000. |

### Runtime bails

Data-dependent. `Check` passes these; `Render` returns `ErrUnsupported`:
- Output of a non-integral number, \|n\| ≥ 2^53, an array or an object.
- `.name` or `["key"]` on anything but an object, and `[n]` on anything but an array.
- `size`, `first` or `last` when the key is absent, because liquidjs computes them. This includes at the root.
- `==` with an array or object operand; ordering across types or with non-ASCII strings.
- `for` over anything but an array or nil.
- Go values other than `map[string]any`, `[]any`, `string`, `bool`, `nil`, `float64`, `int`, `int64` and `json.Number`.

### Out of spec (always bails)

Filters (`|`), `forloop`, `for` parameters (`limit`, `offset`, `reversed`), `for…else`, ranges, `case`, `capture`, `cycle`, `increment`/`decrement`, `include`/`render`, `liquid`, `echo`, inline `#` comments, `contains`, `empty`/`blank`, float literals, string escapes, variable indexes (`a[b]`), and every Customer.io tag (`cio_link`, `unsubscribe_url`, `countdown`, …).

## Chains

`RenderChain` mirrors the render service's `render` array (`liquidController.js` `parse_liquid`):
- Steps run in order. A step's `Vars`, if set, replace the chain vars for that step only.
- On success, the output is bound at `Key` (for example `["snippets","greeting"]`) into the chain vars, provided the step used them. Nested maps are created as needed.
- A step whose `Key[0]` is `content` always binds `content` at the top level. If that step errors, the raw template body is bound instead.
- `ErrUndefined` is recorded in the step's result and the chain continues.
- The first unsupported step, or any step with `Premailer`, stops the chain. The caller renders `steps[n:]` with the returned vars.
- The caller's maps are never mutated.

## Changing the spec

1. Add the case to `testdata/cases.json`, and run `node scripts/parity.mjs` to confirm liquidjs agrees.
2. Update the grammar and semantics table here in the same change.
3. `go test ./...`, and `go test -fuzz FuzzRender` for a minute or so.
