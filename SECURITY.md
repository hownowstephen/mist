# Security policy

## Supported versions

The latest minor release receives fixes. mist is pre-1.0, so a fix ships as a new release rather than a backport.

## Reporting a vulnerability

Report it privately through [GitHub's vulnerability reporting](https://github.com/hownowstephen/mist/security/advisories/new), not in a public issue.

In scope: anything a template or its data can do to the process rendering it, such as panics, hangs, unbounded memory or CPU (mist bounds loops at 1,000,000 iterations and ranges at 100,000 items), and output that differs from liquidjs where mist doesn't bail.

## Verifying releases

Each release's `checksums.txt` is signed keylessly by the release workflow (`checksums.txt.sigstore.json`), and its archives carry a build provenance attestation (also attached as `mist_<version>.intoto.jsonl`).

```bash
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/hownowstephen/mist/\.github/workflows/release\.yml@refs/tags/v'
gh attestation verify mist_<version>_linux_amd64.tar.gz -R hownowstephen/mist
```
