# 2026-10-23: Timers that last through reloads and restarts (T-0012, part 1)

**Result:** player timers, room timers and question deadlines are kept in SQLite with absolute deadlines and come back after a reload, an unload or a restart. All checks green; both agents' findings fixed (one low documented).
**Task:** T-0012
**Environment:** local container, Go 1.26.10, Playwright 1.56.1 (the container's Chromium through an uncommitted local config), branch `claude/project-thread-vg0awl` on top of dev `e2f718b`.

## What changed

- Migration `0006_timers.sql`: `player_timers` (per character: `timer` and `question` rows) and `room_timers` (per world, with the starter's character). `store.Save.Timers` writes them in the hook's transaction; `PlayerTimers`, `RoomTimers` read them; `FinishWorld` deletes them.
- `internal/game/lasting.go`: timer changes are noted (`World.timerSaves`, at most `maxTimerSaves`) and written with the next save. Room timers are restored as the world loads (last, under `w.mu`), a player's as they join, with their shown countdowns in the first output. Stored rows are checked (name, label, room, count, deadline at most a day ahead) and bad ones deleted.
- Room timers that run out in an empty room wait (`due`) and fire for the next player who comes in, after `on_enter`, instead of being dropped.
- Question deadlines last by identity (room + hash of kind, title, options): the same question asked again gets the time left. Answer, timeout, move and `/reset` forget it; at most 10 per character; deadlines more than a day past are dropped on join.

## Steps

```sh
gofmt -l .
go vet ./...
scripts/check-codemap.sh
go test -race -count=1 ./...
./scripts/fuzz.sh 10s
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e && npx playwright test -c playwright.local.config.js   # local config only sets the Chromium path
```

```text
(gofmt: no files; vet clean; check-codemap exit 0)
ok  	github.com/rumblebump/talespeak/cmd/talespeak	9.365s
ok  	github.com/rumblebump/talespeak/internal/game	69.767s
ok  	github.com/rumblebump/talespeak/internal/script	13.447s
ok  	github.com/rumblebump/talespeak/internal/store/sqlite	29.915s
... every package ok
playwright: 105 passed, 3 skipped (1.1m)
fuzz.sh 10s: 26 targets, exit 0, no finding
```

## Test engineer

Added `internal/game/lasting_edge_test.go` (failed saves keep and later write timer changes, a replaced session keeps timers and the open question's time, invalid stored rows dropped and deleted, the question cap, old deadlines, the bound on unsaved changes, waiting room timers fired, replaced, cancelled and reset, no store notes nothing, `FuzzRestoreTimers`) and `TestTimersSaveIsAtomic` in `internal/store/sqlite/timers_test.go`. Coverage: internal/game 97.2 %, internal/store/sqlite 86.2 %.

Bugs it found, both fixed:

- A world whose script failed to start after its room timers were restored kept those clock timers for up to a day. Fixed: restore last, after every script compiled.
- `/reset room` kept the deadline of a question cut off before the reset. Fixed: `forgetRoomQuestions` in `restart`.

## Security reviewer

- **High, fixed:** `NewWorld` restored room timers without `w.mu`; an overdue timer fired at once on its own goroutine and raced the restore loop (`concurrent map writes` could crash the server). Now restored last, under `w.mu`.
- **Medium, fixed:** `World.timerSaves` grew without bound while saves failed. Now capped at `maxTimerSaves` (10000), the oldest change dropped and logged.
- **Low, fixed:** the question cap evicted a deadline still running, so cutting off 10 other questions reset one. Now only a deadline that passed makes room.
- **Low, fixed:** deadlines that passed long ago timed out a fresh ask days later. Now dropped on join after a day.
- **Low, not changed:** a waiting room timer counts toward the room's 10 and `RoomTimerLeft` is negative for it. Lua already sees 0 (`timer.left` clamps), entering the room wakes them, and the count is bounded; documented in docs/lua-sandbox.md.
- govulncheck could not run (the proxy refuses vuln.go.dev).

`TestRoomGoWhileCanceled` (internal/script, untouched by this change) failed once in a full `-race` run under load; it passed in every later run, `-count=30` included.

## Findings

- What still belongs to T-0012: in-game time and pausing a world, GM pause/extend/cancel with audit entries, the per-profile extra-time factor and an edu minimum deadline. The task stays in `tasks/doing/`.
