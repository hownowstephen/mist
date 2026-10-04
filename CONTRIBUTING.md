# Contributing

mist's contract is simple: for any template it accepts, its output matches [liquidjs](https://liquidjs.com) 10.26 byte for byte, and for anything else it returns `ErrUnsupported`. Every change keeps that contract: match liquidjs exactly, or bail.

## Setup

- Go 1.24 or newer (`go.mod`'s minimum; CI tests the last three releases).
- Node 22, for the liquidjs oracle: `cd scripts && npm ci`.

```bash
go test ./...                 # unit tests, cases.json, the committed corpus
node scripts/parity.mjs       # cases.json against liquidjs
```

## Making a change

[SPEC.md](SPEC.md) is normative. A change in behaviour is a change to it, made in the same PR:

1. Add cases to `testdata/cases.json` and confirm them against liquidjs with `node scripts/parity.mjs`. Mark cases where mist bails with `"err": "unsupported"`.
2. Update SPEC.md's grammar and semantics. A spec change bumps the minor version, and the SPEC title names that line (`v0.15.x`).
3. If the grammar changed, extend `scripts/gen` so the generated differentials cover it.
4. Run `go test ./...` (CI enforces a 99% coverage floor), a 100k generated differential and a few minutes of `go test -fuzz FuzzRender` ([SPEC.md § Verification](SPEC.md#verification) has the commands).
5. If a new bail depends on data rather than syntax, add its message to `runtimeBail` in `mist_test.go`, or `FuzzRender` reports that `Check` accepted a template `Render` bailed on.

Regenerating `testdata/corpus.json` goes in a separate PR stacked on the change, so the code diff stays reviewable.

## Pull requests

Titles start with `feat:`, `fix:` or `chore:` (or `docs:`, `refactor:`); release notes are grouped by them. CI runs the tests on the last three Go releases, golangci-lint, govulncheck, liquidjs parity, a generated differential, and actionlint and zizmor on the workflows.

## Releases

Maintainers tag `vX.Y.Z` on `main`. The release workflow refuses a tag that isn't on `main` or doesn't pass the tests, then builds `mistcheck`, signs the checksums, attaches build provenance, publishes the release, and warms the Go module proxy.
