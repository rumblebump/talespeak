# 2026-10-18: T-0032 the demo's box code, new in every world

**Result:** all T-0032 acceptance items done. Go tests pass with -race; the demo and party Playwright tests pass.
**Task:** T-0032
**Environment:** cloud sandbox, Linux amd64, Go 1.26.9, Chromium from Playwright 1.56.1. Branch `claude/project-thread-i8xjj8` from `dev`. No new dependencies, no Go production code changed.

Carl asked whether the demo's PIN puzzle (the key box in the guard room, always 417) could get a new code in every new world.

## What changed

- `content/demo/lib/rooms.lua`: `box_code()` picks `string.format("%03d", math.random(0, 999))` the first time a player needs it and keeps it in `state.world` with `update` (the first code stays); a refused write says the "cannot remember" line and returns nil. `spelled()` turns "417" into "4 1 7" (Lua patterns are off in the sandbox, so `string.sub`).
- `corridor.lua` shows the note from `box_code()`; `guardroom.lua` compares the box answer with it. The fixed `CODE = "417"` is gone.
- `docs/pack-patterns.md`: "A secret every world picks".
- Tests: the demo tests read the code from the note (`noteCode`, `boxCode`, `otherCode`), plus `TestDemoBoxCodePerWorld` and `TestDemoBoxBeforeStone`; e2e reads the note too.

`math.random` was already in the sandbox. gopher-lua draws from Go's global `math/rand`, which is seeded randomly at start, so no core primitive was missing.

## Steps

```sh
gofmt -l .
go vet ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e && npx playwright test tests/demo.spec.js tests/party.spec.js
./scripts/board.sh > tasks/BOARD.md
```

```text
gofmt: none; go vet: ok
-race: all packages ok (game 41.3s, script 13.7s)
Playwright: 8 passed, 2 skipped (party tests run on desktop only)
```

## Test engineer

Added `internal/game/demo_code_test.go`: a copy of the demo (in memory) whose `box_code` draws a fixed code, to check codes with leading zeros (000, 007, 070, 100, 999), which room picks first, 12 players at once, a refused `state.world` write (`refuseWorld`), and `FuzzDemoBoxAnswer` (10 s, 17,234 execs, pass). Mutations `%03d` -> `%d` and ignoring a refused update are caught. `return new` instead of `v or new` is not catchable today: the world runs one hook at a time, so the `update` is extra safety. Coverage: game 97.2%, script 96.5%. No bugs found.

## Security reviewer

No critical, high or medium findings. One low finding, older than this change: the sandbox exposes `math.randomseed`, which calls Go's deprecated global `rand.Seed`. With go 1.26 it is a no-op (`randseednop=1`); a server started with `GODEBUG=randseednop=0` would let one pack fix every world's random numbers. Not fixed here (core work, outside this content change): opened as T-0033. govulncheck could not reach vuln.go.dev from the sandbox; no dependency changed.
