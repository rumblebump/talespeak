# 2026-10-18: T-0023 state.player for the session

**Result:** all T-0023 acceptance items done. Go tests pass with -race, all 64 Playwright tests pass, and the security re-review is noted below.
**Task:** T-0023
**Environment:** cloud sandbox, Linux amd64, Go 1.26.9, Node 22, Chromium from Playwright 1.56.1. Branch `t-0023-player-state` from `t-0022-room-go`, with `dev` merged in after #13 and #14. No new dependencies.

The content thread asked for progress that follows a player from room to room (the cell, corridor and guard room demo). `state.player` is the first slice of T-0018. It has the same calls and limits, but for now it lasts only for the session. It is a core primitive in the sense of Carl's rule: the engine keeps values, and packs decide what they mean. After the implementation, the `test-engineer` and `security-reviewer` agents worked in parallel. Their findings were fixed, and the security reviewer checked the fixes.

## Steps

```sh
gofmt -l .
go vet ./...
go test -race -count=1 ./...
go test -count=1 -cover ./internal/game ./internal/script
go test -run '^$' -fuzz '^FuzzStateValue$' -fuzztime 10s ./internal/script/
./scripts/check-web.sh
CGO_ENABLED=0 go build -trimpath -o bin/talespeak ./cmd/talespeak
cd e2e && npx playwright test
./scripts/board.sh > tasks/BOARD.md
```

```text
gofmt: none; go vet: ok
-race: all packages ok
coverage: game 97.0%, script 95.4%
FuzzStateValue: ok
check-web: ok
Playwright: 64 passed
```

govulncheck could not run: vuln.go.dev is blocked from the sandbox (HTTP 403). It runs in CI.

## What changed

- `internal/script/state.go`:
  - `state.player.get(key)` and `state.player.set(key, value)`.
  - Values are kept as a binary encoding, and limits are measured on it. T-0018 stores the same bytes, so a value that fits now fits later.
  - Limits: keys of 64 bytes, values of 16 KiB, tables nested 8 deep, 256 KiB and 500 keys per player, 50 writes per hook (its dialogs included).
  - A refused write returns nil and the reason.
  - One `set` looks at no more than 65536 table slots, empty ones included.
- `internal/script/script.go`: `Host.PlayerState`, `Thread.writes`, and `Runtime.Reserve`, so `state.player` counts toward the world's `StateMemory` together with the room instances.
- `internal/game/game.go`: each session has a `State`. Its writes are reserved against the address's share (`clientKept`) and the world (`Runtime.Reserve`), and `Close` frees them.
- `docs/lua-sandbox.md`: the two calls, the encoded sizes, and the limits row.
- `tasks/open/T-0024-lua-table-memory.md`: older gaps the reviews found in how Lua tables are counted.

## Findings

security-reviewer (report only): 0 critical, 1 high, 3 medium, 1 low.

| Severity | Finding | Fix |
| --- | --- | --- |
| High | One `set` of a table that reused a sparse table 1600 times ran for 4.2 s under the world lock. gopher-lua skips empty slots in Go, where the 100 ms call limit cannot stop it | A `set` looks at no more than 65536 table slots in all, empty ones and removed keys included (read with `reflect`) |
| Medium | Kept values used about 11 times more heap than they were charged | Values are kept encoded, and the charge is the key plus the encoding |
| Medium | The size was not T-0018's encoded size, so packs could break when T-0018 lands | The encoding is the one T-0018 will store; `docs/lua-sandbox.md` gives its sizes |
| Medium | `state.player` had its own world pool beside the room instances, so a world could keep twice `StateMemory` | `Runtime.Reserve` counts it in the same total that `ErrFull` checks |
| Low | Writes were counted per call (reset at each dialog answer), and what happens to writes in a failed hook was not written down | 50 writes per hook; a value set before a hook fails stays set, like a global (documented) |

Out of scope, filed as T-0024: `retained()` does not count empty array slots or removed keys, and `t[6e7] = true` makes gopher-lua allocate about 1 GiB in one step. Both are older than this change.

test-engineer (tests only) wrote `internal/script/state_edge_test.go`, `internal/game/state_edge_test.go` and `FuzzStateValue`. Four of the tests failed on the first version and pass after the fixes above: the second world pool, heap use 10.8 times the charge, a sparse table accepted at 11 bytes, and 50 writes per dialog.

security-reviewer re-review: the five findings hold. New:

| Severity | Finding | Fix |
| --- | --- | --- |
| Medium | `Runtime.Reserve` could measure the whole world from inside a hook, on the caller's time and memory budgets: one `set` from an innocent player failed with "too much memory" in a full world | Inside a hook, `Reserve` compares the cheap upper bound only, and the check after the call measures. A write can then be refused once while unmeasured memory is counted, and the next one works. `TestReserveInHookComparesTheBound` |
| Medium | That measurement missed the running hook's locals, so an Instance could keep several times `PlayerMemory` while it was counted only for its globals | The same fix: no measurement happens during a call |
| Low | The decoder trusted integer keys, so a damaged encoding (T-0018 will read it from the database) could make one `get` fill a 1 GiB array | The decoder counts the table slots it makes against the same 65536 and refuses larger array keys before gopher-lua grows the array. `TestDecodeRefusesLargeArrays` |
