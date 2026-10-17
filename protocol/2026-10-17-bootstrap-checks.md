# 2026-10-17: Bootstrap file checks

**Result:** both workflows pass actionlint, compose.yml and the Caddyfile validate, and the board script generates BOARD.md. The Docker build is not testable yet (no Go code).
**Task:** T-0001
**Environment:** Claude cloud sandbox; Docker Compose v2 CLI; actionlint installed from source.

## Steps

```sh
# YAML parses
python3 -c "import yaml; [yaml.safe_load(open(f)) for f in ['.github/workflows/ci.yml','.github/workflows/release.yml','deploy/compose.yml']]"

# GitHub Actions lint
go install github.com/rhysd/actionlint/cmd/actionlint@latest
actionlint -shellcheck= .github/workflows/*.yml

# compose file resolves with the example settings
cp deploy/.env.example /tmp/test.env
docker compose --env-file /tmp/test.env -f deploy/compose.yml config -q

# Caddyfile (no Docker daemon in the sandbox, so built caddy from source)
go install github.com/caddyserver/caddy/v2/cmd/caddy@latest
MUD_DOMAIN=mud.example.org ACME_EMAIL=a@example.org caddy validate --config deploy/Caddyfile --adapter caddyfile

# task board
sh scripts/board.sh > tasks/BOARD.md
sh scripts/board.sh --next-id
```

```text
actionlint: no findings (exit 0)
docker compose config: exit 0
caddy validate: Valid configuration
--next-id: T-0009
```

## Findings

- Shellcheck inside actionlint was turned off (not installed in the sandbox); rerun with it in CI later.
- `docker build` of the Dockerfile is still untested; it needs the Go module from T-0002.
