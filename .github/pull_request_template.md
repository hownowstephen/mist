<!-- Title: feat:, fix:, chore:, docs: or refactor: -->

## What and why

## Verification

- [ ] `go test ./...`
- [ ] New or changed behaviour has cases in `testdata/cases.json`, and `node scripts/parity.mjs` passes
- [ ] SPEC.md updated in this PR (and its version line bumped) if behaviour changed
- [ ] `scripts/gen` covers new grammar; a 100k generated differential has no mismatches
- [ ] Corpus regeneration, if needed, is in a stacked PR
