# AGENTS.md

Guidance for coding agents working on mist. [CONTRIBUTING.md](CONTRIBUTING.md) is the full workflow, and [SPEC.md](SPEC.md) is the normative behaviour.

## The one rule

For every template mist accepts, its output must match liquidjs 10.26 (`new Liquid({ lenientIf: true })`, `TZ=UTC`) byte for byte. When mist can't match exactly, it must return `ErrUnsupported` (`bail(...)`) instead of guessing. A change that renders more templates but disagrees with liquidjs anywhere is wrong, so check behaviour against liquidjs, not intuition.

## Map

| Path | What's there |
|---|---|
| `mist.go` | Public API: `Engine`, `Render`, `Append`, `Check`, `RenderChain`, errors |
| `render.go` | The single-pass scan, tags and block frames |
| `expr.go` | Expressions, conditions and paths |
| `value.go` | Values: undefined and nil, property and index lookup, numbers, output |
| `compare.go` | `==`, `<` and friends, `contains`, `blank` and `empty` |
| `space.go` | The three whitespace classes, and where each applies |
| `filters.go` | Filter chains, registered filters, `default`, the memory budget |
| `stdfilters.go` | Built-in filter dispatch (argument counts in each case) and shared conversions |
| `stringfilters.go`, `arrayfilters.go` | String and array filter implementations |
| `date.go` | `date` input parsing and strftime |
| `dialect.go` | `Engine.Dialect` hooks |
| `liquidjs_filters.go` | Every filter liquidjs defines (checked by `parity.mjs`) |
| `testdata/cases.json` | Hand-written cases, each checked against liquidjs |
| `testdata/corpus.json` | Harvested and generated templates with liquidjs's results |
| `scripts/` | `config.mjs` (the shared liquidjs config and environment checks), `parity.mjs`, `oracle.mjs` (liquidjs as oracle), `gen/` (template generator), `bench.mjs` |
| `cmd/mistcheck` | The template checker |

## Commands

```bash
go test ./...                                   # unit tests, cases.json, corpus.json (99% coverage floor in CI)
cd scripts && npm ci && node parity.mjs         # cases.json against liquidjs
go run ./scripts/gen -n 100000 -seed 7 > /tmp/g.json
node scripts/oracle.mjs -o /tmp/o.json /tmp/g.json
MIST_CORPUS=/tmp/o.json go test -run TestCorpus -v .   # differential: must report 0 mismatches
go test -run '^$' -fuzz FuzzRender -fuzztime 60s .
golangci-lint run ./...
```

## When changing behaviour

1. Add cases to `testdata/cases.json` and confirm them with `node scripts/parity.mjs` before relying on them. A bail case (`"err": "unsupported"`) names its `"code"`.
2. Update SPEC.md in the same change, and bump its title's minor version (`v0.N.x`).
3. Extend `scripts/gen` for new grammar or filters, and run a 100k differential with a new seed.
4. Every `bail` takes a code (`Error.Code`), which is API: reuse one that fits, or add a kebab-case one and run `go test -run TestCodes -update-codes .` to record it in `testdata/codes.txt`. A data-dependent bail's code also goes in `runtimeCodes` (`mist_test.go`), or `FuzzRender` fails.
5. Regenerating `testdata/corpus.json` goes in a separate PR stacked on the change.
6. Keep `Append` allocation-free on the `basic` benchmark fixture (`alloc_test.go`).

PR titles start with `feat:`, `fix:`, `chore:`, `docs:` or `refactor:`.
