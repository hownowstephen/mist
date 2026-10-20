# Contributing

mist's contract is simple: for any template it accepts, its output matches [liquidjs](https://liquidjs.com) 10.26 byte for byte, and for anything else it returns `ErrUnsupported`. Every change keeps that contract: match liquidjs exactly, or bail.

## Setup

- Go 1.25 or newer (`go.mod`'s minimum; CI tests the last three releases).
- Node at the exact version in `.node-version`, for the liquidjs oracle: `cd scripts && npm ci`. Another Node release can carry another ICU, which changes case mapping and date output.

`scripts/config.mjs` is the parity config that `parity.mjs`, `oracle.mjs` and `bench.mjs` share. It refuses to run unless the process locale is en-US (liquidjs takes its `locale` default from it), and fails if liquidjs's resolved options differ from `scripts/liquidjs-options.json`. After a deliberate change to either, rewrite the snapshot with `node scripts/config.mjs -w`. CI also regenerates `testdata/jscase.json`, `testdata/jsdate.json`, `testdata/jschar.json` and `testdata/corpus.json` and fails if any differs from the committed file.

`scripts/latest` pins the newest liquidjs, which Dependabot keeps current, with its own options snapshot. `scripts/latest/drift.txt` lists the `cases.json` and `corpus.json` renders that differ under it, and the nightly fails, opening an issue, when that list changes. PRs that touch `scripts/latest`, such as Dependabot's bumps, run the same check, so a bump that changes the list needs `drift.txt` rewritten in the same PR. After reviewing a change, rewrite `drift.txt` with the newest liquidjs installed in `scripts/`:

```bash
cd scripts && cp latest/package.json latest/package-lock.json latest/liquidjs-options.json . && npm ci && cd ..
scripts/latest/drift.sh > scripts/latest/drift.txt
git checkout scripts/package.json scripts/package-lock.json scripts/liquidjs-options.json && (cd scripts && npm ci)
```

```bash
go test ./...                 # unit tests, cases.json, the committed corpus
node scripts/parity.mjs       # cases.json against liquidjs
```

## Making a change

[SPEC.md](SPEC.md) is normative. A change in behaviour is a change to it, made in the same PR:

1. Add cases to `testdata/cases.json` and confirm them against liquidjs with `node scripts/parity.mjs`. Mark cases where mist bails with `"err": "unsupported"` and the `"code"` it bails with, which `TestCases` checks. For a change to what `Limits.Memory` charges, add cases with `"mem"`, what liquidjs charges its `memoryLimit` (`parity.mjs` checks it), and `"mistMem"` where mist charges more. Each case's `"spec"` lists the SPEC.md rule anchors (`r-…`) it tests, and a new rule gets an `<a id="r-…"></a>` anchor; `TestSpecLinks` fails on a rule or bail code no case covers, unless `codes_test.go`'s allowlist says why none can.
2. Update SPEC.md's grammar and semantics. A spec change bumps the minor version, and the SPEC title names that line (`v0.15.x`).
3. If the grammar changed, extend `scripts/gen` so the generated differentials cover it.
4. Run `go test ./...` (CI enforces a 99% coverage floor), a 100k generated differential and a few minutes each of `go test -fuzz FuzzRender` and `-fuzz FuzzInvariants` ([SPEC.md § Verification](SPEC.md#verification) has the commands).
5. Every bail takes a code (`Error.Code`), which callers rely on: reuse one that fits, or add a kebab-case one and record it with `go test -run TestCodes -update-codes .`. If a new bail depends on data rather than syntax, add its code to `runtimeCodes` in `mist_test.go`, or `FuzzRender` reports that `Check` accepted a template `Render` bailed on.

`FuzzParity` fuzzes templates and data against liquidjs itself, through a long-running `scripts/parity-worker.mjs`, so the fuzzer minimises any mismatch it finds. Wherever mist renders, liquidjs must give the same output, and wherever mist returns `ErrUndefined`, liquidjs must fail. It needs Node and `npm ci` in `scripts`, so it only runs when asked; the nightly runs it for a few minutes:

```bash
MIST_FUZZ_PARITY=1 go test -run '^$' -fuzz FuzzParity -fuzztime 10m .
```

A crasher in `testdata/fuzz/FuzzParity` replays with `MIST_FUZZ_PARITY=1 go test -run FuzzParity/NAME .`; once it's understood, add it to `testdata/cases.json`.

Regenerating `testdata/corpus.json` goes in a separate PR stacked on the change, so the code diff stays reviewable. `testdata/corpus.accepted` lists the corpus renders mist doesn't bail on, and `TestCorpus` fails if one starts bailing; that PR, or any that deliberately changes which renders bail, rewrites it with `go test -run TestCorpus -update-accepted .`. The same flag rewrites `testdata/corpus.fragile`, the accepted renders where liquidjs's output differs from Ruby Liquid's (the corpus's `ruby` field). Those are where a future liquidjs is most likely to change, so a PR that adds to it is accepting templates that may need a bail later. `TestCorpus -v` lists them, and logs any change to the set without failing.

### Adding a filter or tag

On top of the steps above, a built-in filter touches:

- `stdfilters.go`: a `builtinFilter` case that checks its argument count with `takes(lo, hi)` and returns. Longer implementations go in `stringfilters.go` or `arrayfilters.go`.
- `filters.go`: `spend`, if liquidjs's `memoryLimit` charges it more than its input and arguments, or if its output can be much larger than its input.
- `liquidjs_filters.go`: only if liquidjs added the filter. `parity.mjs` checks this list.
- SPEC.md: its row under Semantics, and any data-dependent bails under Runtime bails, each with an `r-…` anchor that its `cases.json` cases cite in `spec`.
- `docs/supported.md`'s filter table and `scripts/gen`'s `filters()`. `TestBuiltinFiltersListed` fails if either is missing a `builtinFilter` name.

A built-in tag touches:

- `render.go`: a case in `tag`. A block tag also needs a `frameKind`, any `frame` fields it uses (with a comment saying which kind uses them), its end tag, and the `else`/`elsif` guards if they apply. `liquid` decides which tags its lines can hold.
- SPEC.md: `tagbody` in the grammar, the structural rules for block tags, and a row under Semantics with an `r-…` anchor that its `cases.json` cases cite in `spec`.
- `docs/supported.md`'s tag table, and `scripts/gen`'s `block` (`TestBuiltinFiltersListed` doesn't cover tags).

## Performance

`Append` must not allocate on the basic fixture; CI enforces that. The benchmark fixtures in `testdata/bench` (template, data and liquidjs's output) are what `BenchmarkFixture` renders and `scripts/bench.mjs` times liquidjs on; after changing one, rewrite its output with `node scripts/bench.mjs -w`. To compare locally:

```bash
go test -run '^$' -bench Fixture -benchmem -count 6 . > /tmp/go.txt
node scripts/bench.mjs -go /tmp/go.txt
```

The Benchmarks workflow posts the same table nightly and on release tags, and a benchstat comparison against the base branch on every PR. Neither fails the build; the comparison warns if mist falls below 4× faster than liquidjs rendering a pre-parsed template.

## Pull requests

Titles start with `feat:`, `fix:` or `chore:` (or `docs:`, `refactor:`); release notes are grouped by them. CI runs the tests on the last three Go releases, golangci-lint, govulncheck, liquidjs parity, a generated differential, and actionlint and zizmor on the workflows.

## Releases

Maintainers tag `vX.Y.Z` on `main` with an annotated tag whose message body summarizes the release for users: what changed and whether upgrading needs anything. It heads the release notes, above the commits grouped by type. The release workflow refuses a tag that isn't on `main` or doesn't pass the tests, then builds `mistcheck`, signs the checksums, attaches build provenance, publishes the release, and warms the Go module proxy.
