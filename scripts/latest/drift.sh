#!/usr/bin/env bash
# Prints the cases.json and corpus.json renders that disagree with the liquidjs installed in
# scripts/, one per line, sorted. drift.txt is its output with this directory's liquidjs;
# the nightly fails when they differ.
set -eu
cd "$(dirname "$0")/../.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
node scripts/parity.mjs | sed -n 's/^FAIL \([^:]*\):.*/cases.json \1/p' > "$tmp/drift"
node scripts/oracle.mjs -o "$tmp/corpus.json" testdata/corpus.json > /dev/null
MIST_CORPUS="$tmp/corpus.json" go test -run '^TestCorpus$' -v . | sed -n 's/^ *corpus_test\.go:[0-9]*: \(.* strict=[a-z]*\)$/corpus.json \1/p' >> "$tmp/drift"
sort -u "$tmp/drift"
