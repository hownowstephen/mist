# Extending mist

The package-level `Render`, `Append` and `Check` use an `Engine` with nothing registered. An `Engine` adds custom tags and filters, a clock, and a dialect.

## Rendering

- `Render(tpl, vars, strict)` returns a string.
- `Append(dst, tpl, vars, strict)` appends to a buffer you reuse, without allocating.
- `Check(tpl)` reports whether a template parses within what mist renders, without data. A template that passes can still fall back at render time on data-dependent rules.
- `RenderChain(steps, vars)` renders templates whose outputs feed later ones, such as snippets, then subject, then body, then layout. Each `Step` can bind its output to a variable path (`Key`); a step keyed `content` is bound even when it fails, so a layout still renders. It stops at the first step mist can't render and returns its index, with the variables hydrated so far, for the full engine to continue from.

`Engine.Now` sets the clock for `'now'` and `'today'`. `Engine.Limits` bounds each render's filter memory, output and loop iterations; keep `Limits.Memory` at or below your full engine's `memoryLimit` so mist falls back wherever that engine would refuse (see [SPEC.md § Limits](../SPEC.md#limits)).

Errors are `*mist.Error`s matching `ErrUnsupported` (render with the full engine) or `ErrUndefined` (strict mode), or wrapping the error your tag, filter or dialect hook returned. `Error.Code` names the reason and stays stable across releases, so count fallbacks by it (`filter:NAME` and `tag:NAME` for ones mist doesn't support, `hook:filter:NAME` for your own filter's error, and reasons such as `nesting-too-deep`; [testdata/codes.txt](../testdata/codes.txt) lists them). `Msg` is for people and may change. A panic in mist or in your tags, filters or dialect hooks comes back as `ErrInternal`, which also matches `ErrUnsupported`, so falling back is still right; log it with `Error.Stack`, since it's a bug.

## Custom tags

Inline tags only (`{% name args %}`); block tags aren't supported. `Tag.Lookup` resolves a path in the tag's scope: loop variables, then assigns, then data.

```go
e := mist.Engine{Tags: map[string]mist.TagFunc{
	"link": func(dst []byte, t mist.Tag) ([]byte, error) {
		url, ok := t.Lookup(t.Args) // {% link product.url %}
		if !ok {
			return nil, mist.ErrUnsupported // let the full engine handle it
		}
		return fmt.Appendf(dst, `<a href="%s">`, url), nil
	},
}}
```

`Tag.Render` renders Liquid in the tag's scope, for tags that render a variable or an argument as a template; return its error as is.

Built-in tag names can't be overridden. Returning `ErrUnsupported` hands the template to the full engine; any other error stops rendering.

## Custom filters

```go
e.Filters = map[string]mist.FilterFunc{
	"shout": func(f mist.Filter) (any, error) {
		s, ok := f.Input.(string)
		if !ok {
			return nil, mist.ErrUnsupported
		}
		return strings.ToUpper(s) + "!", nil
	},
}
```

- Registered filters override built-ins of the same name.
- Return `mist.ErrBuiltin` to apply the built-in after all, for example to change only how nil input behaves.
- Return `mist.ErrUnchanged` to leave the input as it was, keeping an undefined input undefined; returning `Filter.Input` would turn it into null, which compares differently.
- `Filter.Input` is nil for both null and undefined, and `Filter.Strict` is the render's strict flag. Inside arrays and objects, undefined is `mist.Undefined{}`, for example in `items | map: 'missing'`.
- Return a value implementing `mist.Comparer` to define how it compares in conditions, as a JavaScript object with its own semantics would (a decimal.js `Decimal`, say).
- `Engine.PassUnknownFilters` passes filters liquidjs doesn't define through unchanged, as liquidjs does with `strictFilters` off. Turn it on only when `Filters` covers every filter your full engine has, or templates using a filter mist doesn't know would render without it.

## Dialects

`Engine.Dialect` adapts mist to an engine that differs from liquidjs:
- `Output` prints non-string values, for engines that stringify arrays or objects differently;
- `Compare` evaluates comparisons;
- `Reject` makes constructs fall back;
- `NoDefaultLeniency` turns off the leading-`default` leniency.

With a dialect set, matching the target engine is up to you. See [SPEC.md § Dialects](../SPEC.md#dialects).
