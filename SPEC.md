# mist Liquid subset — v0.15.x

_Last updated 2026-10-03. Parity target: liquidjs 10.26.0 configured with `new Liquid({ lenientIf: true })`, running with `TZ=UTC` and the en-US locale._

**Contract.** For every template and data where mist returns output, liquidjs returns the same output. Where mist returns `ErrUndefined`, liquidjs fails too, though it may report a different error. Anything else returns `ErrUnsupported`, and the caller renders with the full engine. Bailing is always safe, so when in doubt, the spec bails.

**Checking a template:** `go run ./cmd/mistcheck FILE...` or `mist.Check(tpl)`. The checker is the renderer with evaluation switched off, so it cannot drift from this document's grammar. A template that passes can still bail at render time on the [runtime bails](#runtime-bails) below.

## Grammar

LL(1) EBNF. Each production is one function in `render.go`/`expr.go`. Anything that doesn't derive from `template` is out of spec.

```ebnf
template = { text | output | tag } ;
text     = ? bytes up to the next "{{" or "{%" ? ;
output   = "{{" [ "-" ] ws expr { filter } ws [ "-" ] "}}" ;
filter   = "|" ident [ ":" [ farg { "," farg } ] ] ;          (* at most 4 positional args; built-ins take the counts below; "f:" alone has none *)
farg     = expr | "allow_false" ":" ( "true" | "false" ) ;    (* named: only default's allow_false *)
tag      = "{%" [ "-" ] ws tagbody ws [ "-" ] "%}" ;

tagbody  = "if" fcond | "elsif" fcond | "else" | "endif"
         | "unless" fcond | "endunless"
         | "for" ident "in" ( path | range ) { [ "," ] forparam } | "endfor" | "break" | "continue"
         | "case" expr { filter } | "when" expr { ( "," | "or" ) expr } | "endcase"
         | "assign" ident "=" expr { filter }
         | "capture" ( ident | string ) | "endcapture"
         | "comment" | "endcomment"
         | "raw" | "endraw"
         | "cycle" [ expr ":" ] expr { "," expr }
         | custom ;
custom   = ident { ? any ? } ;                     (* only names registered in Engine.Tags *)

fcond    = cond { filter } ;                            (* filters apply to the condition's value *)
cond     = cmp [ ( "and" | "or" ) cond ] ;               (* right to left: a and b or c is a and (b or c) *)
cmp      = expr [ op expr ] ;
op       = "==" | "!=" | "<=" | ">=" | "<" | ">" | "contains" ;   (* contains: not followed by an ident character *)
expr     = path | string | number | range | "true" | "false" | "nil" | "null" | "blank" | "empty" ;   (* blank, empty: only as operands of == or != *)
range    = "(" ws expr ws ".." ws expr ws ")" ;
forparam = ( "limit" | "offset" ) ws ":" ws expr | "reversed" ;   (* each at most once *)
path     = ident { "." ws word | "[" ws expr ws "]" } ;   (* ".." ends a path, for ranges; forloop must be followed by "." word *)
ident    = ( letter | "_" ) { letter | digit | "_" | "-" | "?" } ;  (* ASCII only *)
word     = ( letter | digit | "_" | "-" | "?" ) { letter | digit | "_" | "-" | "?" } ;
string   = "'" { char | escape } "'" | '"' { char | escape } '"' ;  (* char: any but the quote or \ *)
escape   = "\" ( "b" | "f" | "n" | "r" | "t" | "v" | "u" hex{0..4} | oct{1..3} | ? any other ? ) ;
number   = [ "-" | "+" ] digit { digit } [ "." { digit } ] ;
ws       = { " " | "\t" | "\n" | "\v" | "\f" | "\r" | U+00A0 } ;
```

Structural rules the EBNF doesn't express:
- Blocks must nest and close: `if…endif` and `unless…endunless`, each with optional `elsif`s and at most one final `else`, `case…endcase` with any number of `when`s and at most one final `else`, `for…endfor` with no `else`, and `capture…endcapture`. `break`, `continue` and `forloop` must be inside a `for`. The maximum nesting depth is 16.
- `comment … endcomment`: the body is tokenized but ignored. A nested `comment` or `raw`, or a tag with no name, bails.
- `contains`, `and`, `or` and `not` can't be variable names at the root of a path; liquidjs parses them as operators. They are fine as properties (`a.and`). Literal keywords can't start a path either (`for x in nil` bails).
- `raw … endraw`: the body is emitted verbatim. Trim markers on either tag bail.
- Tags end at the first `%}`, even inside quotes, as in liquidjs. Outputs end at the first `}}` outside quotes; a backslash-escaped quote doesn't close a quote.
- A number can't be followed by `.`, an identifier character or a non-blank non-ASCII character (`1.2.3` and `1x` bail). Inside tags, Unicode blanks other than U+00A0 bail, though liquidjs skips them.
- Filters on a condition must come after the whole `and`/`or` chain; a filter before an operator (`a | upcase == "X"`) is a liquidjs syntax error and bails.
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
| Output: string, bool, nil | As-is; `true`/`false`; `""` for a nil or undefined variable. Printing the `nil`/`null` literal itself (including through `default: nil`) bails: liquidjs represents it as a Drop, which prints differently depending on the `outputEscape` configuration. |
| Output: number | Number literals and data print as JavaScript's `String(n)`: shortest round-trip digits, fixed notation for 1e-7 ≤ \|n\| < 1e21, otherwise exponent notation (`1e+21`, `2.5e-8`). |
| `assign` | Writes the render's scope, so it is visible after an enclosing `for` and never visible to other chain steps. |
| `capture` | Renders its body instead of printing it, and assigns the result as a string, exactly as `assign` would (so it replaces data and earlier assigns of the same name). A quoted name is stored as written. In a dead branch nothing is rendered or assigned. |
| `case` / `when` | The `case` value (with any filters) and each `when` value are evaluated as conditions are: undefined variables are lenient under strict. Every `when` with a value `==` to the `case` value renders, in order, not just the first; a `when` stops evaluating its values at the first match. `else` renders only when no `when` matched. The body between `case` and the first `when` is parsed but never rendered. A `nil` case value is null, so it doesn't match an undefined `when` value. |
| `cycle` | Writes its values in turn, one per render of the tag, skipping the write when the value is falsy in JavaScript (`0`, `""`, `false`, nil, undefined). Only the value written is evaluated, so under strict only it can be `ErrUndefined`. The position is kept for the whole render, keyed as liquidjs keys it: by the group (`String` of its value, or `undefined` without one) and the number of values, not the values themselves, so `{% cycle 'a', 'b' %}{% cycle 'c', 'd' %}` prints `ad`. |
| `for` | Arrays and ranges only. Null or undefined (lax) means zero iterations. The loop variable shadows and is restored after `endfor`. `offset: n`, `limit: n` and `reversed` apply in that order whatever order they're written in, as `slice(n)`, `slice(0, n)` and a reversal; `n` must be an integer, and a negative one counts from the end. A render runs at most 1,000,000 loop iterations; past that it bails. |
| `break` / `continue` | End the innermost `for`, or move it to its next item. The rest of its body is skipped, including inside nested `if`, `case` and `capture` blocks; a `capture` still assigns what it rendered before the `break`. |
| `forloop` | Inside a `for`, `forloop.index`, `index0`, `rindex`, `rindex0`, `first`, `last`, `length` (after the modifiers) and `name` (`variable-collection` as written) describe the innermost loop. Any other property, including `parentloop`, is undefined, as in liquidjs 10.26. |
| Ranges | `(a..b)` with integer bounds is the array `a`, `a+1`, … `b` (empty when `b < a`), at most 100,000 items. |
| `.size` / `.first` / `.last` | An object's own key wins. Otherwise `size` is an array's length, a string's UTF-16 length or an object's key count, and `first`/`last` are an array's first and last items; on strings and objects they're undefined. Other keys of arrays and strings are undefined. |
| `==` / `!=` | Same-type scalars compare by value (numbers numerically). Different types are never equal. `nil` matches both null and undefined, but a null variable ≠ an undefined variable. |
| `== blank` / `!= blank` | Blank means nil or undefined, `false`, `""` or a whitespace-only string (JavaScript's `\s`, so U+00A0 and U+FEFF count but U+0085 doesn't), `[]` or `{}`. `0` and `true` aren't blank. Works on either side. `blank` against `blank`, `empty` or `nil` bails, because liquidjs is asymmetric there. |
| `== empty` / `!= empty` | Only `""`, `[]` and `{}` are empty; whitespace, nil, undefined, `0` and `false` aren't. Works on either side. `empty` against `blank`, `empty` or `nil` bails. |
| `contains` | Same precedence as `==`. An array contains an element equal by JavaScript's `===` (same type and value; arrays and objects are never equal to a scalar). A string contains the right side's string form, as `indexOf` coerces it: numbers as JavaScript prints them, booleans as `true`/`false`, nil as `null` and undefined (lax) as `undefined`; the empty string is always contained. Anything else contains nothing. |
| Filters | Applied left to right in `{{ }}` and `assign`. Built in: `default`, `capitalize`, `date` and the string, array and math filters below. Registered filters (`Engine.Filters`) override built-ins of the same name. Any other filter name bails, even in a dead branch, unless `Engine.PassUnknownFilters` is set (below). Filter arguments are never lenient: under strict an undefined argument is `ErrUndefined`. |
| Leading `default` | When `default` is the first filter, an undefined input is lenient under strict (liquidjs's `lenientIf`). Later in a chain it isn't: `{{ x \| capitalize \| default: 'd' }}` is `ErrUndefined` for an undefined `x`. |
| `default: d` | Replaces nil, undefined, `false`, `""` and `[]` with `d` (nil with no argument). Whitespace-only strings, `0` and `{}` are kept. `allow_false: true` keeps `false`. |
| `capitalize` | Stringifies the input (nil → `""`, booleans → `true`/`false`, numbers as JavaScript prints them, arrays joined), then upper-cases the first character and lower-cases the rest, exactly as JavaScript's `toUpperCase`/`toLowerCase` do. A first character outside the BMP is left as is (`charAt(0)` sees half a surrogate pair). |
| String filters | All stringify their input and string arguments as `capitalize` does, and a missing string argument is `""`. `append: s` and `prepend: s` (exactly one argument). `downcase` and `upcase` use JavaScript's case mapping with the same bails as `capitalize`. `replace: a, b` replaces every `a` and `replace_first: a, b` the first, both literally (no `$` patterns); an empty `a` puts `b` between every UTF-16 unit for `replace` and before the string for `replace_first`. `remove: a` and `remove_first: a` delete them. `strip`, `lstrip` and `rstrip` trim what `String.prototype.trim` does, or, given a truthy argument, the characters of its string (`0`, `false` and `""` are falsy, so they trim whitespace). `escape` replaces `& < > " '` with `&amp; &lt; &gt; &#34; &#39;`; `escape_once` first unescapes exactly those five entities. `url_encode` is `encodeURIComponent` with spaces as `+`, and `url_decode` is `decodeURIComponent` followed by `+` → space (so `%2B` decodes to a space). `replace_last: a, b` and `remove_last: a` act on the last `a`; an empty `a` matches at the end. `base64_encode` is the UTF-8 bytes in standard base64 with padding; `base64_decode` decodes as Node's `Buffer.from(s, 'base64')` does: standard and URL-safe alphabets, other characters skipped, stopping at the first `=`. `hmac_sha256: key` is the hex HMAC-SHA256 of the UTF-8 strings. `xml_escape` is `escape`. `cgi_escape` is `encodeURIComponent` with spaces as `+` and `! ' ( ) *` escaped too; `uri_escape` is `encodeURI` with `[` and `]` kept. `normalize_whitespace` replaces each run of JavaScript whitespace with one space. `number_of_words` counts runs of non-whitespace; with `'cjk'`, or `'auto'` when the string has any, each CJK character (U+4E00–9FFF, F900–FAFF, 3400–4DBF, 3040–30FF, AC00–D7AF) counts as a word and the runs exclude them. `slugify: mode, cased` replaces each run of characters the mode doesn't keep with `-` (`default` and `latin` keep letters, marks and decimal digits, `pretty` also `._~!$&'()+,;=@`, `ascii` only `A-Za-z0-9`, `raw` everything but whitespace, `none` does nothing), drops one leading and one trailing `-`, and lower-cases unless `cased` is truthy; `latin` first strips the accents liquidjs's table lists. `strip_newlines` deletes every `\n` and a `\r` directly before one; `newline_to_br` replaces the same with `<br />\n` (a lone `\r` stays). `strip_html` removes `<script…</script>`, `<style…</style>` and `<!--…-->` blocks, then `<…>` tags, all case-sensitive; once an opener has no closer it is no longer tried, and an unclosed `<` at the start or right after a removed tag is kept with everything after it. |
| `truncate: n, e` / `truncatewords: n, e` | Lengths are UTF-16 units, `n` defaults to 50 (or 15 words) and `e` to `...`; an undefined argument takes the default and a nil one stringifies to `""`. `truncate` returns its input unchanged (even a number) when it fits in `n`, else the first `n − len(e)` units plus `e`. `truncatewords` splits on runs of JavaScript whitespace (leading or trailing whitespace makes an empty first or last word), keeps `max(n, 1)` words joined by single spaces, and appends `e` whenever there are at least `n` words, even exactly `n`. |
| `split: s` / `slice: i, n` / `first` / `last` | `split` returns an array, splitting UTF-16 units for an empty separator, and drops trailing empty strings. `slice` takes a string (in UTF-16 units) or an array: a negative `i` counts from the end, `n` defaults to 1, and nil gives `[]`. `first` is the first element or unit, or `""` for anything else; `last` is the last element or unit. Arrays can't be printed, so these results must feed a `for`, an assign or another filter. |
| `join: s` / `size` / `where: p, v` | `join` joins an array's elements with `s` (default `" "`, also for nil) as JavaScript's `join` does: nil elements are `""`. A non-array is one element, and nil is `""`. `size` is the UTF-16 length of a string or the length of an array; anything else, including an object, is 0. `where` keeps the array's items whose dotted property path `p` is truthy, or `==` to `v` when given. A single object is a one-item array and nil is `[]`. |
| `map: p` / `sum` / `sort` / `find: p, v` / `push: x` / `reverse` | Arrays as `toArray` sees them: nil is `[]` and any other non-array is one item. `map` takes each item's dotted property path `p` (undefined where missing). `sum` adds `Number` of each item, or of its `p`, counting `NaN` as 0. `sort` is stable, puts nil and undefined last and orders all-number or all-string keys (each item, or its `p`) by JavaScript's `<`. `find` is the first item `where` would keep, or undefined; `find_index` is its index, `has` whether there is one, and `reject` keeps the items `where` drops. `compact` drops nil and undefined items. `uniq` keeps first occurrences of numbers, strings, booleans and nil (by JavaScript's SameValueZero). `concat: b` appends `b` as `toArray` sees it. `sort_natural` sorts as `sort` does but compares keys lower-cased by JavaScript's `toLowerCase` after stringifying them, so numbers and booleans sort as text. `push` appends a copy; `reverse` reverses a copy. `unshift: x` prepends to a copy, and `pop` and `shift` drop the last or first item of a copy. `group_by: p` groups items (an array, or a non-empty string as one item; anything else is `[]`) by their `p`, distinct by SameValueZero, into `{name, items}` objects in order of first appearance. |
| `array_to_sentence_string: c` | Of an array: `""` for none, the item itself for one, `a c b` for two, and `a, b, c z` for more, with `c` defaulting to `and`. Items print as JavaScript's `String` does (nil is `null`), except that the items before the last in the longer form print nil as `""`, as `join` does. |
| `where_exp: n, e` / `find_exp: n, e` / `group_by_exp: n, e` | `n` and `e` must be string literals: `n` a variable name other than a keyword or `forloop`, `e` a condition with optional filters, which `Check` parses. For each item, `e` is evaluated with `n` bound to the item (shadowing loop variables, assigns and data), as a filtered condition's value is: a lone operand's value, else `true`/`false`, then the filters. `where_exp` keeps items whose value is exactly `true` (so a truthy string isn't kept); `find_exp` returns the first item whose value is truthy in JavaScript, or undefined; `group_by_exp` groups by the value as `group_by` does. Under strict an undefined variable in `e` is `ErrUndefined`, unless `e`'s first filter is `default`. |
| `json` | `JSON.stringify` of strings, numbers, booleans, nil and arrays of them, with no spacing. Undefined, including what `default` returns without an argument, prints nothing. |
| `plus` `minus` `times` `divided_by` `modulo` `at_least` `at_most` | Exactly one argument. Both operands go through liquidjs's `toNumber` (`+x \|\| 0`): numbers as is, booleans as 1 and 0, nil and undefined as 0, and strings as JavaScript's `Number` reads them after trimming whitespace: decimals, unsigned `0x`/`0o`/`0b` integers, and 0 for anything else. `divided_by` is float division, as in liquidjs (not Liquid's integer division); `modulo` is JavaScript's `%`; `at_least` is `Math.max` and `at_most` is `Math.min`. `abs`, `ceil` and `floor` take no argument and are JavaScript's `Math` functions on the same `toNumber`. `round: d` rounds half away from zero at `d` decimal places (default 0). |
| `date: format, tz` | Input: nil passes through (`""`); `'now'` and `'today'` are both the current instant (`Engine.Now`, default `time.Now`); numbers and all-digit strings are epoch seconds; `""` and numbers outside JavaScript's Date range pass through unchanged; other strings must be ISO 8601 (below). A nil or missing format is `%A, %B %-e, %Y at %-l:%M %P %z`; other formats are stringified. Output is liquidjs's strftime: directives match `%[-_0^#:]*[0-9]*[EO]?.`, unknown ones print as written, and the flags pad without (`-`), with spaces (`_`) or zeros (`0`), upcase (`^`), swap case (`#`) or add a colon to `%z` (`:`). `%N` is the milliseconds right-padded with zeros, because JavaScript dates have no finer precision. |
| `date_to_xmlschema` / `date_to_rfc822` / `date_to_string` / `date_to_long_string` | `date` with the formats `%Y-%m-%dT%H:%M:%S%:z`, `%a, %d %b %Y %H:%M:%S %z`, `%d %b %Y` and `%d %B %Y`. `date_to_string` and `date_to_long_string` with `'ordinal'` print the day as `2nd` (`%-d%q`), and with `'ordinal', 'US'` put the month first: `March 2nd, 2024`. |
| `date` ISO 8601 input | `YYYY-MM-DD`, optionally followed by `T` or a space, `HH:MM`, optional `:SS` and `.fraction` (1–9 digits, truncated to milliseconds), and optional `Z` or `±HH:MM`. Without an offset the time is UTC, as it is for `new Date` under `TZ=UTC`. |
| `date` timezone | Without one, times print in UTC and `%z` is `+0000`. A string is an IANA name or alias resolved with Go's `time.LoadLocation`; `%Z` prints it as written. A number is minutes west of UTC, as JavaScript's `getTimezoneOffset` counts them; `%Z` prints the offset. Either way `%s` shifts by the offset, as it does in liquidjs. Programs deployed without a system tz database can import `time/tzdata`. |
| `<` `>` `<=` `>=` | Two numbers, or two ASCII strings (byte order). |
| `and` / `or` | Grouped right to left, as in liquidjs: `a and b or c` is `a and (b or c)`. Every operand is evaluated. |
| Filtered conditions | `{% if a.b \| default: false %}`: the filters apply to the condition's value (a lone operand's value, else the `and`/`or` result as `true`/`false`), then truthiness decides. The operand stays lenient under strict, as in any condition. |
| `.` and `[ ]` | `a.k` and `a["k"]` read object keys; a dot may be followed by blanks. On arrays, integers and integer strings as JavaScript prints them (`1`, `-1`, `"2"`) index, negative from the end, and other digit strings (`01`) are undefined. `a[k]` evaluates `k` first: a string reads that key, a number indexes an array or reads the key `String(n)` of an object, and a non-integer index of an array is undefined. Under strict, an undefined variable inside `[ ]` is `ErrUndefined` even where the rest is lenient (a leading `default`, a condition), as in liquidjs. |
| String literals | Escapes as liquidjs's `parseStringLiteral`: `\b \f \n \r \t \v`; `\u` with up to 4 hex digits (none gives U+0000); up to 3 octal digits; any other escaped character as itself (`\'`, `\"`, `\\`, `\x` → `x`). |
| Whitespace control | `{{-`/`{%-` trim the template text before; `-}}`/`-%}` trim the template text after. Rendered values are never trimmed. The trimmed set is ASCII whitespace plus U+00A0, U+1680, U+180E, U+2000–200A, U+2028, U+2029, U+202F, U+205F, U+3000. |

### Runtime bails

Data-dependent. `Check` passes these; `Render` returns `ErrUnsupported`:
- Output of an array or an object, or of the nil literal coming out of a filter (`{{ x | default: nil }}` with `x` empty).
- `capitalize` of an object, or of a string whose case mapping Go can't reproduce exactly: full-Unicode expansions such as `ß` → `SS` or polytonic Greek as the first character, and `İ`, `Σ` (final-sigma depends on position) or characters newer than Go's Unicode tables in the rest.
- `.name` or `["key"]` on anything but an object, array or string, `[n]` on anything but an array or object, `length` of an array or string and digit keys of a string (JavaScript own properties), and `[k]` whose key evaluates to nil, a bool, an object or an array (or, in lax mode, is undefined).
- `size`, `first` or `last` as a root variable that isn't defined, because liquidjs computes them from the scope.
- `for` over anything but an array, a range or nil; `limit`/`offset` that aren't integers; range bounds that aren't integers or span more than 100,000 items; more than 1,000,000 loop iterations in one render.
- `uniq` of objects or arrays (JavaScript compares them by reference); `sort_natural` of objects, arrays or strings outside the BMP; `url_decode` of a malformed escape or of bytes that aren't UTF-8 (`decodeURIComponent` throws); `base64_decode` to bytes that aren't UTF-8 (Node substitutes U+FFFD).
- `map`, `sum`, `sort` and `find` on an object, or with a path reaching an item that isn't an object; `sum` of an object or array item; `sort` of booleans, arrays or objects, of mixed numbers and strings, or of strings outside the BMP; `push` or `unshift` of nil or undefined; `group_by` of an object, or by a key that's an object or array.
- `contains` with an array or object on the right.
- `array_to_sentence_string` of anything but an array (liquidjs indexes a string's characters or throws), or of two or more items including an object or array; `slugify` with a mode other than those above, or of a character Go's Unicode tables don't assign.
- `==` with an array or object operand; ordering across types or with non-ASCII strings.
- `for` over anything but an array or nil.
- `assign` of nil, null or undefined. liquidjs stores a different null-ish value depending on the source, and each one compares differently.
- String filters splitting a character outside the BMP, as `truncate`, `slice`, `first`, `last`, `split: ''`, `replace: ''` or `strip` with such a character can; string filters on invalid UTF-8; `downcase` or `upcase` of a character whose JavaScript mapping differs; `strip` whose argument is the `nil` literal (liquidjs passes it as a truthy Drop).
- `truncate`, `truncatewords` or `slice` with a non-numeric count; `first` or `last` of an empty array, `last` of anything but a non-empty string or array, `first` of an object; `json` of an object.
- `strip_html` of an unclosed `<` that follows text or a kept character, where liquidjs 10.26 loops forever.
- `join` of an array holding an array (JavaScript joins nested arrays with commas); `size` of an object with a `length` key; `round` to digits that aren't an integer from 0 to 15.
- `where` on a string, number or bool; with a property path that isn't dotted identifiers (`a[0]`, or a keyword such as `nil`); on items whose path reaches an array or a scalar; and under strict when an item, or a step of its path, is missing or nil (liquidjs throws).
- Math filters on arrays, objects, the `nil` literal or `Infinity`, on hex strings above 2^64, or whose result is `NaN` or infinite.
- `date` of a bool, array or object; of a string that isn't `now`, `today`, all digits or the ISO 8601 form above (V8's fallback parser accepts far more); of an ISO string with an out-of-range field (V8 rolls `2024-02-30` over to March); or of an instant that displays outside the years 1000–9999.
- `date` formats with `%c`, `%x` or `%X` (they print with the ICU locale), `%Z` without a timezone (it prints the process's zone name), or a width over 1024.
- `date` timezones Go can't load, `Local`, names that aren't plain IANA paths, zones whose offset at that instant has seconds, and non-integer, boolean or other non-string timezone arguments. Go's tz database can lag or lead Node's ICU; recent rule changes may differ.
- Go values other than `map[string]any`, `[]any`, `string`, `bool`, `nil`, `float64`, `int`, `int64` and `json.Number`.

### Out of spec (always bails)

Filters other than the built-ins above and registered ones (with `Engine.PassUnknownFilters`, only those liquidjs defines); built-ins with more arguments than listed (or none where one is required); named filter arguments other than `allow_false`; `for…else`, `for` parameters other than `limit`, `offset` and `reversed` (and `reversed` with a value), `break`/`continue`/`forloop` outside a `for`, `cycle` values not separated by commas or that are `blank`, `empty` or `nil`, a `when` after `else`, `when` values separated by anything but `,` or `or`, `case` on `blank` or `empty`, `increment`/`decrement`, `include`/`render`, `liquid`, `echo`, inline `#` comments, `contains nil`, `contains` glued to the next word (`a containsb`), `capture` with anything after its name, a string escape of half a surrogate pair, and any tag not in the grammar.

## Custom tags

`Engine.Tags` registers inline tags (`{% name args %}`). Block tags such as `{% x %}…{% endx %}` aren't supported.
- A tag's function runs each time the tag is reached in a live branch. That includes every loop iteration but never a dead branch.
- The function gets the raw, trimmed text after the name (`Args`), the data passed to `Render` (`Vars`), and the strict flag. `Lookup(path)` resolves a variable in the tag's scope: loop variables, then assigns, then data.
- Returning an error that wraps `ErrUnsupported` hands the template to the full engine. Any other error stops rendering and is returned wrapped, so `errors.Is` still matches it.
- Built-in tag names can't be overridden, and names that aren't registered bail as before. `Engine.Check` accepts registered names without calling them. Their arguments aren't checked.
- The parity contract doesn't cover custom tags. Matching the full engine's output for them is up to whoever implements the tag.

## Custom filters

`Engine.Filters` registers filters. A `FilterFunc` receives a `Filter` with the input, positional arguments and the strict flag.
- Undefined and nil both arrive as `nil`. Returned values render as built-in values do: strings, booleans, numbers and nil output, while arrays and objects bail.
- Registered names take precedence over built-in filters, so an application can supply its own `default`, `divided_by` and so on.
- Returning an error that wraps `ErrUnsupported` hands the template to the full engine. Other errors stop rendering and are returned wrapped.
- A `nil` `FilterFunc` is accepted by `Check` and bails at render time.
- The parity contract doesn't cover registered filters.
- `Engine.PassUnknownFilters` matches liquidjs's `strictFilters: false`, its default: a filter that liquidjs 10.26 doesn't define and that isn't registered returns its input unchanged. Its arguments are still evaluated, so under strict an undefined argument is `ErrUndefined`, and the leading-`default` leniency doesn't apply to it. Filters liquidjs does define but mist doesn't implement (`uniq`, `compact`, …) still bail. Off by default, because an unknown name usually means a filter the full engine has and mist doesn't; set it only when `Filters` covers every filter the full engine defines.

## Dialects

`Engine.Dialect` adapts mist to a Liquid engine that differs from liquidjs, for example one that emulates another implementation. Everything in this spec describes a nil dialect. With a dialect set, matching the target engine is up to the dialect's author, and mist makes no parity claim.

| Hook | Effect |
|---|---|
| `Output(dst, v)` | Prints non-string values: numbers, bools, arrays, objects. Strings, nil and undefined are printed by mist as usual. |
| `Compare(op, a, b)` | Evaluates `==`, `!=`, `<`, `>`, `<=`, `>=` in conditions. Truthiness and `and`/`or` are unchanged. |
| `Reject` | A set of constructs that bail in both `Render` and `Check`, including in dead branches: `TrimMarkers`, `NegativeLiterals` (including negative indexes), `UnspacedOperators` (a comparison operator with no whitespace before it, such as `x==2`), `RawBlocks`, `BlankKeyword`, `EmptyKeyword`. |
| `NoDefaultLeniency` | Turns off the leading-`default` leniency rule. Combine with an overriding `default` in `Engine.Filters` to change `default` itself. |

- **Values the hooks see:** data values as the caller supplied them (decode with `json.Decoder.UseNumber` to keep `2.0` distinct from `2`), `int64` for integer literals, `nil` for nil, undefined and the `nil` literal, and `mist.Blank` and `mist.Empty` for those keywords.
- **Errors:** a hook returning an error that wraps `ErrUnsupported` bails; other errors stop rendering and are returned wrapped.
- **Always core rules:** the dialect-independent bails (printing the `nil` literal, `blank` or `empty` against a keyword or `nil`, assigning nil, and everything out of spec) still apply. `contains` and `case` bail when `Compare` is set, since the hook doesn't cover them.

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
| `testdata/cases.json` | Hand-written cases, one or more per rule above. `node scripts/parity.mjs` checks each expected output against liquidjs. A case's `now` fixes `Date.now` for `'now'` and `'today'`. |
| `testdata/corpus.json` | Every template with plain-JSON data from [Shopify/liquid-spec](https://github.com/Shopify/liquid-spec) (including production recordings and the Dawn theme) and Shopify/liquid's integration tests, plus 5,000 grammar-generated templates, each with liquidjs's lax and strict result, rendered with `TZ=UTC` and a fixed `Date.now` that `TestCorpus` shares. `TestCorpus` requires exact agreement wherever mist doesn't bail. |
| `scripts/gen` | Random templates from the grammar above with random data. Run large batches on demand (below). |
| `testdata/jscase.json` | JavaScript's `toUpperCase`/`toLowerCase` for every code point (`node scripts/jscase.mjs`). `TestCaseMapping` requires `capitalize`, `downcase` and `upcase` to match or bail for each one. |
| `FuzzRender` | No panics. `Check` rejects ⇒ `Render` fails, and `Check` accepts ⇒ `Render` never bails on syntax. |

Regenerate the corpus (needs Ruby, Node, and clones of both Shopify repos):

```bash
cd scripts && npm ci
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
