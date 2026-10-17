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

## CI on PR #1 (first run)

The `image` job ran because the PR adds the Dockerfile, and failed: the build needs `go.mod`/`go.sum`, which do not exist yet.

```sh
gh api repos/rumblebump/talespeak/actions/runs/39044023213/jobs --jq '.jobs[] | "\(.name) \(.conclusion)"'
gh api repos/rumblebump/talespeak/check-runs/117429078552/annotations --jq '.[] | "\(.annotation_level): \(.message)"'
```

```text
changes success / secrets success / image failure / ci-ok failure
failure: buildx failed with: ERROR: failed to build: … "/go.sum": not found
```

Fix: the `changes` job now outputs `has_go` (`go.mod` present), and `go` and `image` only run when it is true.
