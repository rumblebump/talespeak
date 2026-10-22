# 2026-10-22: player.gm and world.players() (T-0036)

**Result:** every hook's `player` has `gm` (from `world_members.role` at sign-in), `room.players()` entries carry `gm`, and the new `world.players()` lists every online player of the world with id, name, room and gm in join order. All checks green.
**Task:** T-0036
**Environment:** local container, Go 1.26.10, branch `claude/project-thread-tfjhnc` on top of dev `bd1b1de`; Playwright with Chromium (desktop and phone projects).

## Steps

```sh
# format, vet, all Go tests with the race detector
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

```text
(gofmt: no files)
ok  	github.com/rumblebump/talespeak/cmd/talespeak	7.859s
ok  	github.com/rumblebump/talespeak/content	1.219s
ok  	github.com/rumblebump/talespeak/internal/account	20.416s
ok  	github.com/rumblebump/talespeak/internal/clientip	1.019s
ok  	github.com/rumblebump/talespeak/internal/game	46.285s
ok  	github.com/rumblebump/talespeak/internal/hub	3.308s
ok  	github.com/rumblebump/talespeak/internal/pack	3.292s
ok  	github.com/rumblebump/talespeak/internal/script	13.078s
ok  	github.com/rumblebump/talespeak/internal/server	1.086s
ok  	github.com/rumblebump/talespeak/internal/store/sqlite	23.191s
ok  	github.com/rumblebump/talespeak/internal/ws	2.552s
ok  	github.com/rumblebump/talespeak/scripts	3.579s
```

```sh
# party fuzz target, extended to check world.players after every step
go test -run '^$' -fuzz '^FuzzPartyOps$' -fuzztime 10s ./internal/game
# browser tests against the built server
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e && npm ci && npx playwright test
```

```text
fuzz: elapsed: 10s, execs: ~8500, new interesting: 0
ok  	github.com/rumblebump/talespeak/internal/game
  3 skipped
  87 passed (45.1s)
```

## Test engineer

Added `internal/game/roster_edge_test.go` (joiner listed in its own on_enter; several leavers one after another; gm in a room timer's on_timer; world.players while the world ends and after it closes; a reconnect replaces the gm of the older session in both directions; concurrent joins, leaves and calls under -race), `internal/hub/gm_test.go` (Who.GM reaches player.gm through the hub), `internal/script/world_players_test.go` (lists, fresh tables, read-only library, gm default) and a world.players check in `FuzzPartyOps`. No code bugs. Coverage: game 96.6 %, script 97.3 %, hub 98.0 %, account 95.6 %.

It noted that ending a world from the lobby (`World.End`) does run `on_leave` for every player, in no set order; the docs said otherwise and now say so.

## Security reviewer

No critical, high or medium findings. Two low ones, both handled:

- `gm` is read at connect and never re-read. Harmless today (no code lowers a role), but T-0011 adds demoting: its acceptance list now says a role change ends the account's live sessions in that world.
- `world.players()` shows pack Lua every player's name and room, GMs marked. Same world only, packs are admin-installed; `docs/lua-sandbox.md` now asks packs to show rooms and GM presence only when the game needs it.

Checked and fine: gm cannot be forged by a client or a script (set only in `account.Auth` from `world_members`), admins who are not members are not GMs, cost of `world.players` (one sort under the world lock, allocations counted by the call memory watcher), concurrency, sandbox surface (`world` is a read-only library), no web, workflow, dependency, SQL or file changes, no secrets.

## Findings

- Packs should count the party with `world.players()`, not with `on_enter`/`on_leave` in `state.world`: on_leave never runs on a restart or crash.
- Follow-up in T-0011 (role changes end live sessions).
