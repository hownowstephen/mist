# mistcheck

`mistcheck` reports whether templates are within what mist renders, and if not, what stops them.

```bash
go run github.com/hownowstephen/mist/cmd/mistcheck@latest template.liquid other.liquid
```

Each file holds one template, except `.jsonl` files, which hold one per line as a JSON string; with no files it reads one template from stdin. By default it prints the first construct each out-of-reach template uses and exits 1 if there are any. Release archives include prebuilt binaries.

| Flag | Effect |
|---|---|
| `-tags a,b` | Treat these names as registered custom tags. |
| `-filters a,b` | Treat these names as registered custom filters. |
| `-pass-unknown-filters` | Set `Engine.PassUnknownFilters`. |
| `-stats` | Find every unsupported construct in each template, not just the first, then summarize how many templates each one blocks and which sets of blockers most often appear together. |

```bash
mistcheck -stats -tags my_tag templates.jsonl
```
