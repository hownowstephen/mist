# mist

[![CI](https://github.com/hownowstephen/mist/actions/workflows/ci.yml/badge.svg)](https://github.com/hownowstephen/mist/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/hownowstephen/mist.svg)](https://pkg.go.dev/github.com/hownowstephen/mist)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/hownowstephen/mist/badge)](https://scorecard.dev/viewer/?uri=github.com/hownowstephen/mist)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/15202/badge)](https://www.bestpractices.dev/projects/15202)

A single-pass Go renderer for most of [Liquid](https://shopify.github.io/liquid/), fast enough to try before falling back to a full engine.

mist evaluates as it scans, with no parse tree and no allocations on the hot path: about 10–80× faster than [liquidjs](https://liquidjs.com) on its benchmark templates. For every template it accepts, its output matches liquidjs 10 (`lenientIf: true`) byte for byte. Anything it can't render exactly returns `ErrUnsupported`, so you render that template with your full engine instead.

Use it in front of liquidjs (or an engine that behaves like it) when you render many templates and most of them are ordinary. It isn't a complete Liquid engine on its own.

## Install

```bash
go get github.com/hownowstephen/mist
```

Requires Go 1.25 or newer.

## Quickstart

```go
out, err := mist.Render(tpl, vars, strict)
switch {
case errors.Is(err, mist.ErrUnsupported):
	out, err = fullEngine.Render(tpl, vars, strict) // outside what mist renders exactly
case errors.Is(err, mist.ErrUndefined):
	// strict mode, undefined variable; the full engine fails too
}
```

`vars` is plain JSON-shaped data (`map[string]any`). `mist.Append` renders into a reusable buffer without allocating.

## Will my templates render?

```bash
go run github.com/hownowstephen/mist/cmd/mistcheck@latest template.liquid
```

It reports the first construct each template uses that mist doesn't render; `-stats` summarizes a whole set. See [docs/mistcheck.md](docs/mistcheck.md).

## Docs

| | |
|---|---|
| [docs/supported.md](docs/supported.md) | What renders, by tag, expression and filter, and what falls back |
| [docs/extending.md](docs/extending.md) | `Engine`: custom tags and filters, chains of templates, dialects for other Liquid engines |
| [docs/mistcheck.md](docs/mistcheck.md) | The template checker |
| [SPEC.md](SPEC.md) | The normative grammar and semantics, and how parity is verified |
| [pkg.go.dev](https://pkg.go.dev/github.com/hownowstephen/mist) | API reference with runnable examples |
| [CONTRIBUTING.md](CONTRIBUTING.md) · [AGENTS.md](AGENTS.md) | Working on mist, for people and coding agents |
| [SECURITY.md](SECURITY.md) | Reporting vulnerabilities, verifying releases |

Releases and their notes are on the [releases page](https://github.com/hownowstephen/mist/releases). Report bugs, including any output that differs from liquidjs, in [issues](https://github.com/hownowstephen/mist/issues).

## License

[MIT](LICENSE)
