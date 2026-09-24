# mist Liquid subset — v0.3.0

_Last updated 2026-09-24. Parity target: liquidjs 10.16.1 configured with `new Liquid({ lenientIf: true })`._

**Contract.** For every template and data where mist returns output, liquidjs returns the same output. Where mist returns `ErrUndefined`, liquidjs fails too, though it may report a different error. Anything else returns `ErrUnsupported`, and the caller renders with the full engine. Bailing is always safe, so when in doubt, the spec bails.

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
         | "raw" | "endraw"
         | custom ;
custom   = ident { ? any ? } ;                     (* only names registered in Engine.Tags *)

cond     = cmp { "and" cmp } | cmp { "or" cmp } ;      (* one connective per condition *)
cmp      = expr [ op expr ] ;
op       = "==" | "!=" | "<=" | ">=" | "<" | ">" ;
expr     = path | string | int | "true" | "false" | "nil" | "null" | "blank" ;   (* blank: only as an operand of == or != *)
path     = ident { "." ident | "[" int "]" | "[" string "]" } ;   (* no spaces inside *)
ident    = ( letter | "_" ) { letter | digit | "_" | "-" } ;      (* ASCII only *)
string   = "'" { ? any but ' or \ ? } "'" | '"' { ? any but " or \ ? } '"' ;
int      = [ "-" ] digit { digit } ;                              (* |n| < 2^53 *)
ws       = { " " | "\t" | "\n" | "\v" | "\f" | "\r" } ;
```

Structural rules the EBNF doesn't express:
- Blocks must nest and close: `if…endif` and `unless…endunless`, each with optional `elsif`s and at most one final `else`, and `for…endfor` with no `else`. The maximum nesting depth is 16.
- `comment … endcomment`: the body is tokenized but ignored. A nested `comment` or `raw`, or a tag with no name, bails.
- `contains`, `and`, `or` and `not` can't be variable names at the root of a path; liquidjs parses them as operators. They are fine as properties (`a.and`). Literal keywords can't start a path either (`for x in nil` bails).
- `raw … endraw`: the body is emitted verbatim. Trim markers on either tag bail.
- Tags end at the first `%}`, even inside quotes, as in liquidjs. Outputs end at the first `}}` outside quotes.
- Dead branches are parsed, not skipped. A syntax error or unknown tag anywhere bails, because liquidjs rejects the whole template.

## Semantics

| Topic | Rule |
|---|---|
| Truthiness | Only `false`, `nil`/`null`, and undefined are falsy. `""`, `0`, `[]` and `{}` are truthy. |
| Lookup order | Innermost `for` variable, then `assign`ed names, then data. |
| Undefined in `{{ }}` or a `for` collection | Strict: `ErrUndefined`. Lax: renders `""` / zero iterations. |
| Undefined in `if`/`elsif`/`unless`/`assign` | Never an error (`lenientIf`). Strict: the value becomes null, because liquidjs catches the error. Lax: it stays undefined. So `null == undefined` is true in strict and false in lax. |
| Strict undefined error | `ErrUndefined` names the whole path (`a.b.c`), even if the undefined part is a prefix. |
| Strict undefined timing | Returned as soon as the tag containing it parses cleanly, without scanning the rest of the template. If that tag doesn't parse cleanly, for example `{{ x \| default: 'a' }}` which liquidjs treats as lenient, mist bails instead. liquidjs parses everything first, so if the template also has a later syntax error, liquidjs reports that error instead. Either way both fail. |
| Null in a path | `a.b.c` with `a` null is null. Never an error, even under strict. |
| Missing key / out-of-range index | Undefined. Negative indexes count from the end. |
| Output: string, bool, nil | As-is; `true`/`false`; `""`. |
| Output: number | As JavaScript's `String(n)`: shortest round-trip digits, fixed notation for 1e-7 ≤ \|n\| < 1e21, otherwise exponent notation (`1e+21`, `2.5e-8`). |
| `assign` | Writes the render's scope, so it is visible after an enclosing `for` and never visible to other chain steps. |
| `for` | Arrays only. Null or undefined (lax) means zero iterations. The loop variable shadows and is restored after `endfor`. |
| `==` / `!=` | Same-type scalars compare by value (numbers numerically). Different types are never equal. `nil` matches both null and undefined, but a null variable ≠ an undefined variable. |
| `== blank` / `!= blank` | Blank means nil or undefined, `false`, `""` or a whitespace-only string (JavaScript's `\s`, so U+00A0 and U+FEFF count but U+0085 doesn't), `[]` or `{}`. `0` and `true` aren't blank. Works on either side. `blank` against `blank` or `nil` bails, because liquidjs is asymmetric there. |
| `<` `>` `<=` `>=` | Two numbers, or two ASCII strings (byte order). |
| `and` / `or` | Evaluated as written. Mixing them bails, which sidesteps Liquid's right-to-left associativity. |
| Whitespace control | `{{-`/`{%-` trim the template text before; `-}}`/`-%}` trim the template text after. Rendered values are never trimmed. The trimmed set is ASCII whitespace plus U+00A0, U+1680, U+180E, U+2000–200A, U+2028, U+2029, U+202F, U+205F, U+3000. |

### Runtime bails

Data-dependent. `Check` passes these; `Render` returns `ErrUnsupported`:
- Output of an array or an object.
- `.name` or `["key"]` on anything but an object, and `[n]` on anything but an array.
- `size`, `first` or `last` when the key is absent, because liquidjs computes them. This includes at the root.
- `==` with an array or object operand; ordering across types or with non-ASCII strings.
- `for` over anything but an array or nil.
- `assign` of nil, null or undefined. liquidjs stores a different null-ish value depending on the source, and each one compares differently.
- Go values other than `map[string]any`, `[]any`, `string`, `bool`, `nil`, `float64`, `int`, `int64` and `json.Number`.

### Out of spec (always bails)

Filters (`|`), `forloop`, `for` parameters (`limit`, `offset`, `reversed`), `for…else`, ranges, `case`, `capture`, `cycle`, `increment`/`decrement`, `include`/`render`, `liquid`, `echo`, inline `#` comments, `contains`, `empty`, float literals, string escapes, variable indexes (`a[b]`), and any tag not in the grammar.

## Custom tags

`Engine.Tags` registers inline tags (`{% name args %}`). Block tags such as `{% x %}…{% endx %}` aren't supported.
- A tag's function runs each time the tag is reached in a live branch. That includes every loop iteration but never a dead branch.
- The function gets the raw, trimmed text after the name (`Args`), the data passed to `Render` (`Vars`), and the strict flag. `Lookup(path)` resolves a variable in the tag's scope: loop variables, then assigns, then data.
- Returning an error that wraps `ErrUnsupported` hands the template to the full engine. Any other error stops rendering and is returned wrapped, so `errors.Is` still matches it.
- Built-in tag names can't be overridden, and names that aren't registered bail as before. `Engine.Check` accepts registered names without calling them. Their arguments aren't checked.
- The parity contract doesn't cover custom tags. Matching the full engine's output for them is up to whoever implements the tag.

## Chains

`RenderChain` renders a sequence of templates whose outputs feed later ones, such as snippets, then subject, then body, then a layout:
- Steps run in order. A step's `Vars`, if set, replace the chain vars for that step only.
- On success, the output is bound at `Key` (for example `["snippets","greeting"]`) into the chain vars, provided the step used them. Nested maps are created as needed.
- A step whose `Key[0]` is `content` always binds `content` at the top level, following the layout convention of `{{ content }}`. If that step errors, the raw template body is bound instead, so a layout can still render.
- `ErrUndefined` is recorded in the step's result and the chain continues.
- The first unsupported step stops the chain. The caller renders `steps[n:]` with the returned vars.
- Post-processing such as CSS inlining is the caller's job. A step whose output must be post-processed before later steps read it belongs with the full engine.
- The caller's maps are never mutated.

## Verification

| Layer | What it proves |
|---|---|
| `testdata/cases.json` | Hand-written cases, one or more per rule above. `node scripts/parity.mjs` checks each expected output against liquidjs. |
| `testdata/corpus.json` | Every template with plain-JSON data from [Shopify/liquid-spec](https://github.com/Shopify/liquid-spec) (including production recordings and the Dawn theme) and Shopify/liquid's integration tests, plus 5,000 grammar-generated templates, each with liquidjs's lax and strict result. `TestCorpus` requires exact agreement wherever mist doesn't bail. |
| `scripts/gen` | Random templates from the grammar above with random data. Run large batches on demand (below). |
| `FuzzRender` | No panics. `Check` rejects ⇒ `Render` fails, and `Check` accepts ⇒ `Render` never bails on syntax. |

Regenerate the corpus (needs Ruby, Node, and clones of both Shopify repos):

```bash
cd scripts && npm i
ruby harvest.rb ../../liquid-spec ../../liquid /tmp/harvest.json
go run ./gen -n 5000 -seed 1 > /tmp/gen.json
node oracle.mjs /tmp/harvest.json /tmp/gen.json
```

For a large differential run without committing it, use a new seed each time:

```bash
go run ./scripts/gen -n 100000 -seed 3 > /tmp/g.json
node scripts/oracle.mjs -o /tmp/g.oracle.json /tmp/g.json
MIST_CORPUS=/tmp/g.oracle.json go test -run TestCorpus -v .
```

## Changing the spec

1. Add cases to `testdata/cases.json` and confirm them with `node scripts/parity.mjs`.
2. Update the grammar and semantics here in the same change. If the grammar changed, extend `scripts/gen`.
3. Regenerate the corpus, run `go test ./...`, and do a 100k generated run plus a couple of minutes of `go test -fuzz FuzzRender`.
