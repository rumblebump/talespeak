# 2026-10-23: First full review, after the file splits (T-0038)

**Result:** every split is a pure move; the test engineer found no code bugs, the security reviewer no critical, high or medium findings. Five low findings fixed, one flaky test opened as T-0039. All Go and browser tests pass.
**Task:** T-0038
**Environment:** local container, Go 1.26.10, Node 22, Playwright 1.56.1 with Chromium (desktop and phone projects). Reviewed commit `d8d28d8` (branch `claude/project-thread-sri2f3`) against `75ec2f8`, the commit before the splits; the fixes below follow it.

## Scope

The first full pass as CODING.md describes it: both agents read every split file in full, not only the diff.

- `internal/game`: game, budget, session, input, host, commands, gmtools
- `internal/script`: script, host, thread, memory, sandbox, safelib, ui, words
- `internal/account`: account, api, me, login, join
- `web/static`: app, lobby, play, dialog, view, clean, api
- `content/clockmaker/lib/case*.lua`
- the test splits `internal/game/demo*_test.go`, `internal/ws/{ws,shutdown,limits}_test.go`

## Steps

```sh
# moves only: top-level declarations with their doc comments, old vs new (go/ast),
# and sorted lines of every old file against its new files
git show 75ec2f8:internal/game/game.go   # and every other split file
# the usual checks
gofmt -l .
go vet ./...
go test -race -count=1 ./...
./scripts/check-codemap.sh
./scripts/check-web.sh
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e && npx playwright test
```

```text
declarations identical: game 174/174, script 163/163, account 105/105; tests: game 503/503, ws 104/104
(gofmt: no files, vet clean, check-codemap and check-web pass)
ok  	github.com/rumblebump/talespeak/cmd/talespeak	9.627s
ok  	github.com/rumblebump/talespeak/content	1.527s
ok  	github.com/rumblebump/talespeak/internal/account	28.491s
ok  	github.com/rumblebump/talespeak/internal/clientip	1.026s
ok  	github.com/rumblebump/talespeak/internal/game	68.560s
ok  	github.com/rumblebump/talespeak/internal/hub	3.828s
ok  	github.com/rumblebump/talespeak/internal/pack	3.810s
ok  	github.com/rumblebump/talespeak/internal/script	16.304s
ok  	github.com/rumblebump/talespeak/internal/server	1.084s
ok  	github.com/rumblebump/talespeak/internal/store	1.146s
ok  	github.com/rumblebump/talespeak/internal/store/sqlite	29.942s
ok  	github.com/rumblebump/talespeak/internal/ws	2.878s
ok  	github.com/rumblebump/talespeak/scripts	15.073s
  3 skipped
  103 passed (1.0m)
```

Coverage: cmd/talespeak 93.9%, content 75.0%, account 95.0%, clientip 94.7%, game 96.9%, hub 98.1%, pack 98.3%, script 97.3%, server 94.8%, store 85.7%, store/sqlite 85.8%, ws 97.8%.

## Findings

**Test engineer** (`.claude/agents/test-engineer.md`): no code bugs. New tests:

- `internal/game/clockmaker_parts_test.go`: the install list of `case.lua` matches `lib/case_*.lua`, no `case.x` defined twice or missing, no part uses a `case.lua` local; a probe in all 9 rooms checks every name at world start and in each room; a broken install fails `NewWorld`.
- `TestRunCoreEveryCommand` in `internal/game/commands_edge_test.go`: every core command has its own case, an unknown name is refused.
- `e2e/tests/parts.spec.js`: the merged client has every part's methods and live getters; the client cuts long server fields; in edu mode no story log reaches localStorage.

| # | Severity | Finding (security reviewer) | What happened |
| --- | --- | --- | --- |
| 1 | low | `storylog.js` picked localStorage for any value of `edu` but true, so a broken `isEdu` getter in another part would keep kids' logs | Fixed: localStorage only for `edu === false`; e2e test |
| 2 | low | `runCore` sent every unknown core command name to `/audit`; the GM check sits in `commands.go`, apart from the GM commands | Fixed: unknown names refused, note at the top of `gmtools.go`; test |
| 3 | low | Functions that need `w.mu` held do not say so, now in files apart from `Handle` | Fixed: a sentence at the top of `input.go`, `host.go`, `gmtools.go`, and on `charge` |
| 4 | low | The `check-web.sh` fetch exception allowed a nested `static/api.js` and code after the allowed call; `XMLHttpRequest`, `sendBeacon` and `new WebSocket(` were not flagged | Fixed: the exceptions are the exact lines in `web/static/api.js` and `web/static/net.js`; the three are flagged; tests in `scripts/check_web_test.go` |
| 5 | low | The client did not cut every server field to the engine's limits | Fixed: item id 64, label 100, dialog title and options 200, caption 200, story lines 4000 (`MAX_TEXT`) |

Also fixed: CODING.md named `script.go` for `Host`/`Party` (now `internal/script/host.go`).

Open:

- T-0039: `TestRealClockWorldChanges` failed once under a loaded `go test -cover ./...` (real timers, 2 s deadline per player); 13 reruns passed. Older than T-0038.
- Design question for Carl: `isEdu` in `web/static/lobby.js` counts a missing `mode` in `/api/me` as not edu. The server always sends it, so nothing changes now.
- `govulncheck` could not run here (vuln.go.dev blocked by the proxy); CI runs it.
