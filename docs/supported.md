# What mist renders

Everything here renders exactly as liquidjs 10 does (`lenientIf: true`, `TZ=UTC`). Anything else, and any input mist can't reproduce exactly, returns `ErrUnsupported`. [SPEC.md](../SPEC.md) has the precise rules for each item, including the data-dependent cases that fall back.

## Tags

| Tag | Notes |
|---|---|
| `{{ … }}` | Output, with filters. Whitespace control (`{{-`, `-}}`, `{%-`, `-%}`) everywhere. |
| `if` / `elsif` / `else` / `unless` | Filters on conditions (`{% if x \| default: false %}`) too. |
| `case` / `when` / `else` | Every matching `when` renders, as in liquidjs. |
| `for` … `else` | Over arrays and ranges `(1..n)`; `limit`, `offset`, `reversed`; `break`, `continue`; `forloop`. |
| `assign`, `capture` | |
| `increment`, `decrement` | |
| `cycle` | With or without a group. |
| `echo`, `liquid` | |
| `comment`, `{% # … %}`, `raw` | |

Not rendered: `tablerow`, `include`, `render`, `layout`.

## Expressions

- Variables and paths: `a.b`, `a[0]`, `a[-1]`, `a['k']`, `a[key]`; `.size` of arrays, strings and objects, and `.first`/`.last` of arrays.
- Literals: strings with escapes, numbers, `true`, `false`, `nil`, ranges.
- Comparisons: `==`, `!=`, `<`, `>`, `<=`, `>=`, `contains`, `== blank`, `== empty`; `blank` and `empty` alone in a condition, ordered (always false) or printed.
- `and` / `or`, grouped right to left as in liquidjs.

## Filters

| Kind | Filters |
|---|---|
| Strings | `append` `prepend` `capitalize` `downcase` `upcase` `replace` `replace_first` `replace_last` `remove` `remove_first` `remove_last` `strip` `lstrip` `rstrip` `truncate` `truncatewords` `split` `slice` `strip_newlines` `newline_to_br` `strip_html` `normalize_whitespace` `number_of_words` `slugify` |
| Escaping and encoding | `escape` `escape_once` `xml_escape` `url_encode` `url_decode` `cgi_escape` `uri_escape` `base64_encode` `base64_decode` `sha256` `hmac_sha256` `json` `jsonify` `inspect` `raw` |
| Arrays | `first` `last` `size` `join` `reverse` `sort` `sort_natural` `uniq` `compact` `concat` `push` `pop` `shift` `unshift` `map` `sum` `where` `reject` `find` `find_index` `has` `group_by` `array_to_sentence_string` |
| Expression filters | `where_exp` `reject_exp` `find_exp` `find_index_exp` `has_exp` `group_by_exp` |
| Math | `plus` `minus` `times` `divided_by` `modulo` `abs` `ceil` `floor` `round` `at_least` `at_most` `to_integer` |
| Dates | `date` `date_to_xmlschema` `date_to_rfc822` `date_to_string` `date_to_long_string` |
| Other | `default` |

Not rendered: `sample` (random), and filters liquidjs doesn't define unless you register them (see [extending.md](extending.md)).

## Dates

`date` takes epoch seconds, `now`/`today`, and these strings, read as `new Date` reads them:

| Form | Example |
|---|---|
| ISO 8601 | `2024-03-15`, `2024-03-15T10:20:30.5+05:30`, `2024-03-15 10:20:30 UTC` |
| RFC 2822 | `Fri, 15 Mar 2024 10:20:30 GMT`, `15 Mar 2024 10:20 -0700` |
| Month names | `Mar 15, 2024`, `March 15 2024 3:04 PM`, `15 March 2024` |
| Numeric | `3/15/2024` (month first), `2024/3/15 10:20` |
| `Date#toString` | `Fri Mar 15 2024 10:20:30 GMT+0530 (India Standard Time)` |

Zones: `UTC`, `GMT`, the US names (`EST`, `PDT`, …) and `±HHMM`. Times without a zone are UTC. Strings V8 reads more loosely (two-digit years, `Feb 30`, other zone names) fall back. Formats are liquidjs's strftime, with timezone arguments.

## What falls back

Beyond the tags and filters above, mist returns `ErrUnsupported` wherever matching liquidjs exactly would need something it doesn't model, for example:
- printing an array or object;
- comparing objects;
- a string filter splitting a character outside the BMP;
- a math result that isn't finite.

These are data-dependent, so a template can render with some data and fall back with other data. [SPEC.md § Runtime bails](../SPEC.md#runtime-bails) lists them all.
