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

## What's supported

Variables and paths (`{{ a.b[0]['k'] }}`), string/integer/boolean/nil literals, `if`/`elsif`/`else`/`unless`, `for … in`, `assign`, `comment`, `raw`, comparisons, `and`/`or`, and whitespace control. No filters yet. [SPEC.md](SPEC.md) is the normative grammar and semantics.

Check whether templates are in the subset:

```bash
go run github.com/hownowstephen/mist/cmd/mistcheck@latest template.liquid
```

## Testing

Beyond unit tests, mist is diffed against liquidjs on every template from [Shopify/liquid-spec](https://github.com/Shopify/liquid-spec) and Shopify/liquid's test suite, plus grammar-generated templates (100k new ones nightly) and fuzzing. See [SPEC.md § Verification](SPEC.md#verification).

## License

[MIT](LICENSE)
