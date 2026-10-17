# 2026-10-17 Placeholder server and deploy script: local checks

Checks for the placeholder binary (one page, /healthz) and scripts/deploy.sh before the first real staging deploy.

## Environment

Cloud sandbox, Linux amd64, Go 1.26.9 (auto toolchain), no Docker daemon, no SSH access to any server. shellcheck from pip shellcheck-py.

## Commands

```sh
gofmt -l .
go vet ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=local" -o "$SCRATCH/talespeak" ./cmd/talespeak
TALESPEAK_LISTEN=:18080 "$SCRATCH/talespeak" serve &
curl -sS -i localhost:18080/healthz
TALESPEAK_LISTEN=:18080 "$SCRATCH/talespeak" healthcheck && echo healthy
./scripts/fuzz.sh 1s
shellcheck -S warning scripts/deploy.sh scripts/fuzz.sh deploy/remote-up.sh
actionlint -shellcheck="$(command -v shellcheck)"
caddy validate --config deploy/Caddyfile --adapter caddyfile   # TALESPEAK_DOMAIN=stage.ts.spitz.is
# deploy.sh against stub ssh, scp, docker and curl in $STUBS (they act on a temp HOME):
PATH="$STUBS:$PATH" DEPLOY_SSH_TARGET=deploy@stage GH_OWNER=rumblebump GHCR_USER=bot GHCR_TOKEN="$FAKE_TOKEN" \
  scripts/deploy.sh staging main main-abc123
PATH="$STUBS:$PATH" DEPLOY_SSH_TARGET=x GH_OWNER=o GHCR_USER=b GHCR_TOKEN="$FAKE_TOKEN" scripts/deploy.sh dev edge edge-1
```

```text
gofmt: no files; go vet: ok; go test -race: ok (cmd/talespeak, internal/server)
HTTP/1.1 200 OK, Content-Security-Policy set, {"status":"ok","version":"local"}
healthcheck: healthy
fuzz.sh: no fuzz targets yet, exit 0
shellcheck: no warnings in the new scripts; actionlint: clean; caddy: Valid configuration
deploy.sh staging: files copied to ~/talespeak (.env mode 600), .env has domain, kids mode false, GH_OWNER, TALESPEAK_VERSION=main
  docker: config, login (token read from stdin), pull, up --wait, ps, image prune, logout
  deploy: healthy, {"status":"ok","version":"main-abc123"}
deploy.sh dev: "TALESPEAK_DOMAIN is empty in deploy/env/dev.env", exit 1
```

## Findings

- govulncheck could not reach vuln.go.dev from the sandbox (403); it runs in CI.
- The real SSH, ghcr.io pull and Let's Encrypt certificate are only tested by the first staging deploy.

## Result

Placeholder server, health check and deploy flow work locally against stubs.
