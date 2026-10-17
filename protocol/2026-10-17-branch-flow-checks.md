# 2026-10-17 Branch flow, scans and CSP: local checks

Checks for the dev/main branch flow, Trivy and Renovate setup, and the strict CSP in the Caddyfile.

## Environment

Cloud sandbox, Linux amd64, no Docker daemon. actionlint 1.7.12 and caddy built from source; Renovate run with npx (Node 22).

## Commands

```sh
actionlint -color=false -shellcheck="$(command -v shellcheck)"   # shellcheck from pip shellcheck-py
cd deploy && TALESPEAK_DOMAIN=talespeak.example.org ACME_EMAIL=admin@example.org \
  caddy validate --config Caddyfile --adapter caddyfile
TALESPEAK_DOMAIN=talespeak.example.org ACME_EMAIL=admin@example.org \
  caddy adapt --config Caddyfile | grep -o 'connect-src[^;]*'
npx --yes --package renovate@44.166.0 -- renovate-config-validator --strict renovate.json
sh -n scripts/fuzz.sh
sh scripts/board.sh > tasks/BOARD.md
```

```text
actionlint: no findings (exit 0)
caddy validate: Valid configuration
connect-src 'self' wss://talespeak.example.org
renovate-config-validator: Config validated successfully against 1 file(s) (exit 0)
sh -n scripts/fuzz.sh: exit 0
board: 1 doing, 15 open
```

## Findings

- First CI run: lint-ci failed on two shellcheck notes in release.yml (SC2035 unquoted glob in sha256sum, SC2029 intended client-side expansion in ssh). Fixed with `sha256sum -- *` and a disable comment; local actionlint with shellcheck is now clean.

- trivy-action tags older than 0.35.0 were hijacked in March 2026 (GHSA-69fq-xp46-6x23); workflows use 0.35.0 and Renovate pins actions to digests.
- The CSP is not yet tested in a browser; that comes with the Playwright guard (T-0014, T-0015).

## Result

Workflows, Caddyfile and Renovate config are valid locally; CI on the pull request is the next check.
