# mist

[![CI](https://github.com/hownowstephen/mist/actions/workflows/ci.yml/badge.svg)](https://github.com/hownowstephen/mist/actions/workflows/ci.yml)

A single-pass renderer for a strict subset of [Liquid](https://shopify.github.io/liquid/), fast enough that it's cheap to try before falling back to a full engine.

mist evaluates as it scans: no tokenize, compile or AST step, and no allocations on the hot path. As soon as it meets anything outside the subset it returns `ErrUnsupported`, and you render with your full Liquid engine instead. For every template it accepts, its output matches [liquidjs](https://liquidjs.com) 10 (`lenientIf: true`) byte for byte.

```go
out, err := mist.Render(tpl, vars, strict)
switch {
case errors.Is(err, mist.ErrUnsupported):
	// outside the subset: render with the full engine
case errors.Is(err, mist.ErrUndefined):
	// strict mode, undefined variable; the full engine fails too
}
```

`Append(dst, tpl, vars, strict)` does the same into a reusable buffer, with no allocations.

`RenderChain` renders a sequence of templates whose outputs feed later ones, such as snippets, then subject, then body, then layout. It stops at the first step mist can't handle, so only the remaining steps go to the full engine.

## Custom tags and filters

Register inline tags and filters on an `Engine`; the package-level functions use an `Engine` with none. Registered filters override built-in ones.

```go
e := mist.Engine{Tags: map[string]mist.TagFunc{
	"link": func(dst []byte, t mist.Tag) ([]byte, error) {
		url, ok := t.Lookup(t.Args) // {% link product.url %}, resolved in scope
		if !ok {
			return nil, mist.ErrUnsupported // let the full engine handle it
		}
		return fmt.Appendf(dst, `<a href="%s">`, url), nil
	},
}}
out, err := e.Render(tpl, vars, strict)
```

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

## Dialects

`Engine.Dialect` adapts mist to a Liquid engine that differs from liquidjs. It has hooks for how values print (`Output`) and compare (`Compare`), a set of constructs to reject, and a switch for `default`'s leniency. Parity with the target engine is then up to the dialect's author; see [SPEC.md § Dialects](SPEC.md#dialects).

## What's supported

Variables and paths (`{{ a.b[0]['k'] }}`), string/integer/boolean/nil literals, `if`/`elsif`/`else`/`unless`, `for … in`, `assign`, `comment`, `raw`, comparisons including `== blank`/`!= blank`, `and`/`or`, whitespace control, and the `default` and `capitalize` filters. [SPEC.md](SPEC.md) is the normative grammar and semantics.

Check whether templates are in the subset:

```bash
go run github.com/hownowstephen/mist/cmd/mistcheck@latest template.liquid
```

`-tags a,b` and `-filters a,b` treat those names as registered custom tags and filters. `-stats` finds every unsupported construct, not just the first, and summarizes how many templates each one blocks. That shows what to add next. `.jsonl` input holds one template per line as a JSON string:

```bash
mistcheck -stats templates.jsonl
```

## Testing

Beyond unit tests, mist is diffed against liquidjs on every template from [Shopify/liquid-spec](https://github.com/Shopify/liquid-spec) and Shopify/liquid's test suite, plus grammar-generated templates (100k new ones nightly) and fuzzing. See [SPEC.md § Verification](SPEC.md#verification).

## License

[MIT](LICENSE)
