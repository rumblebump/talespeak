# 2026-10-17 T-0002 engine skeleton: tests and security review

First task run with the task workflow (`.claude/skills/task-workflow/SKILL.md`): implementation, then the `test-engineer` and `security-reviewer` agents on the same diff, then fixes.

## Environment

Cloud sandbox, Linux amd64, Go 1.26.9, no Docker. Dependencies: github.com/ncruces/go-sqlite3 v0.35.6 (MIT), github.com/coder/websocket v1.8.15 (ISC), plus go-sqlite3-wasm (MIT-0), julianday (MIT), golang.org/x/sys (BSD).

## Commands

```sh
gofmt -l .
go vet ./...
go test -short -count=1 ./...
go test -race -count=1 -shuffle=on -cover ./...
./scripts/fuzz.sh 5s
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=local" -o "$SCRATCH/talespeak" ./cmd/talespeak
TALESPEAK_LISTEN=:18081 TALESPEAK_DATA="$SCRATCH/data" "$SCRATCH/talespeak" serve &
curl -sS localhost:18081/healthz
# drift check: change one directive in deploy/Caddyfile, expect the test to fail, restore
go test -count=1 -run TestCaddyfile ./internal/server
```

```text
gofmt: none; go vet: ok; -short: all ok (~0.02 s per package)
-race -shuffle: all ok; coverage cmd/talespeak 85.5%, clientip 97.0%, server 96.6%, store/sqlite 86.8%, ws 97.2%
FuzzMigrationVersion 5 s: ok (47k execs); FuzzMessage 5 s: ok (7k execs, live connection); FuzzIP 5 s: ok
binary 12 MB, {"status":"ok","version":"local"}, data/talespeak.db with -wal and -shm
TestCaddyfileMatchesEngineHeaders: fails on the changed Caddyfile, passes when restored
```

## Agent results

test-engineer (tests only): +1476 lines of tests across 4 packages, fuzz targets FuzzMessage and FuzzMigrationVersion. Bugs found:

| Bug | Fix |
| --- | --- |
| Malformed JSON closed with 1007, not the documented 1003 (the library closes inside wsjson.Read) | Read raw frames and decode ourselves |
| `null` accepted as a message | Only text frames holding one JSON object are accepted |
| Two processes opening a new database at once failed: switching to WAL is not covered by busy_timeout | WAL set after open with a retry loop |
| Idle timeout dropped the connection without a close frame (cancelled read) | Idle timer closes with 1008 and is reset on each message |

security-reviewer (report only): nothing critical or high.

| Severity | Finding | Fix |
| --- | --- | --- |
| Medium | No cap on connections, no message rate limit | 1000 connections (503 beyond), 20 messages burst then 10/s, close 1008 |
| Low | Engine CSP and headers differed from deploy/Caddyfile | Same headers in both, `connect-src 'self'`; test reads the Caddyfile |
| Low | Binary frames and `null` accepted, doc said 1003 | As above, doc updated |
| Low | http.Server without Read/WriteTimeout | 30 s each, cleared for WebSocket connections before the upgrade |
| Low | coder/websocket marked indirect in go.mod | go mod tidy |

security-reviewer re-review of the fixes: all five fixed. One new finding:

| Severity | Finding | Fix |
| --- | --- | --- |
| Low | One client address could take all 1000 connection slots | `internal/clientip` finds the client address (X-Forwarded-For believed only from trusted proxies, `TALESPEAK_TRUSTED_PROXIES`, default loopback and private ranges); at most 100 connections per address, 503 beyond. 100 because a school class behind one NAT address shares it |

Tests for it: `TestPerClientConnectionCap` (ws), `TestIP` with 11 cases including spoofed headers from untrusted peers, `FuzzIP` (clientip).

govulncheck could not run in the sandbox (vuln.go.dev answers 403); CI runs it on pushes to dev and main.

## Result

All acceptance items of T-0002 done; every finding fixed with a test that covers it.
