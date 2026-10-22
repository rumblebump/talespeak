# 2026-10-22: The Clockmaker's Silence, a co-op case pack (T-0036)

**Result:** a second story pack, `content/clockmaker`, for 2 to 4 players in five levels with save spots, written in Lua on `state.world`; shipped in the image for the lobby. All checks green; both review agents' findings fixed.
**Task:** T-0036
**Environment:** local container, Go 1.26.10, branch `claude/project-thread-8nfiqj` on top of dev `bd1b1de`; Node 22 for the black-box check. Docker is not available in this container, so the image itself was not built here (CI builds it).

## Steps

```sh
# format, vet, all Go tests with the race detector
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

```text
(gofmt: no files)
ok  	github.com/rumblebump/talespeak/cmd/talespeak	9.587s
ok  	github.com/rumblebump/talespeak/content	1.633s
ok  	github.com/rumblebump/talespeak/internal/account	23.438s
ok  	github.com/rumblebump/talespeak/internal/clientip	1.049s
ok  	github.com/rumblebump/talespeak/internal/game	62.784s
ok  	github.com/rumblebump/talespeak/internal/hub	3.630s
ok  	github.com/rumblebump/talespeak/internal/pack	3.626s
ok  	github.com/rumblebump/talespeak/internal/script	15.954s
ok  	github.com/rumblebump/talespeak/internal/server	1.198s
ok  	github.com/rumblebump/talespeak/internal/store/sqlite	24.954s
ok  	github.com/rumblebump/talespeak/internal/ws	2.908s
ok  	github.com/rumblebump/talespeak/scripts	4.069s
```

```sh
# the pack's tests many times, in any order (secrets are random per world)
go test ./internal/game/ -run Clockmaker -count=20 -shuffle=on
go test -cover -count=1 ./internal/game/
```

```text
ok  	github.com/rumblebump/talespeak/internal/game	27.205s
ok  	github.com/rumblebump/talespeak/internal/game	7.900s	coverage: 96.6% of statements
```

```sh
# the map pictures (Python standard library only)
python3 -I scripts/clockmaker-maps.py content/clockmaker/assets
```

```text
30 files, map-<a|b|c>-<pieces>-<piece>.png, 124K
```

Black-box check: the binary with the pack in a `TALESPEAK_PACKS` folder, as the image has it, on a fresh data directory, driven with fetch and Node's WebSocket.

```sh
TALESPEAK_LISTEN=127.0.0.1:18799 TALESPEAK_DATA=$DATA TALESPEAK_PACKS=$PACKS bin/talespeak serve
node bb.mjs   # register ann and bob, create a public clockmaker world, both connect, ann starts the case
```

```text
talespeak started ... pack="demo 1.0.0" packs=2 world=1 ...
register ann, bob: 200 200
packs in the lobby: demo The damp cell; clockmaker The Clockmaker's Silence
ann creates a clockmaker world: 200 world 2 clockmaker
ann sees: room:study | Wiggins: Mr Holmes sent for us, Irregulars! Read his telegram once everyone is here.
ann's telegram dialog: Start the case with 2 Irregulars (ann, bob)? Wait if someone is still coming.
bob hears: The case begins for 2 Irregulars. Search the room: each of you can find one strip of Holmes's word.
```

## Review agents

**test-engineer** added `internal/game/clockmaker_edge_test.go`: the lead falling back when the first player is away, a fifth and sixth player, knocking for a freed seat, the telegram freeing seats, a player left behind in another room, leaving while locked in the workshop, holding the lantern, standing on the lift, waiting at a save spot or mid-vote, a question closed by a move, odd answers (empty, 500 bytes, `07.25`, reversed crate codes), two players racing for the crate, the end for latecomers, rejoining after being left behind (SQLite), coming back while the lead's "go on without" dialog is open (SQLite), and a full world. It found five bugs, all fixed:

- Level 1 after everyone left: the rewind dropped the strip count, so the first player read the whole word. Fixed: the rewind keeps it.
- Safe, strips, map pieces, club card, evidence cards were marked taken before they were given; with a full world the item was lost and level 3 could get stuck. Fixed: give first, then mark (no question waits in between, so no other hook can run).
- The telegram said the case began when the world could not keep it. Fixed.
- After the case was solved, the casebook still offered to play the level again. Fixed.
- Smaller: a second correct crate answer got no reply; answers without letters or digits echoed an empty code or word. Fixed.

**security-reviewer** found nothing critical, high or medium, and four low findings, all fixed:

- Seats were never given back, so four accounts that looked in once could lock a world. Fixed: going on without someone, playing a level again without them, and starting the case all free the seats of players who are not there; a player outside can knock to join.
- After a restart with players connected, a stale `online` entry could block the save spot for good. Fixed: the save spot decides by who is in its room (`room.players()`), and without the lead there the first player there decides. Documented in docs/pack-patterns.md; `world.players()` is asked for as a core primitive.
- The save spot's "go on without them" acted on a list read before the dialog. Fixed: read again after it, and the casebook checks the lead again.
- Files copied into `/packs` kept the checkout's modes (a 077 umask would stop the container). Fixed: copied and `chmod -R u=rwX,go=rX` in the build stage.

Checked and fine: world state is bounded (at most 13 keys: 4 members), typed text never reaches other players or a key, names are validated account names shown as text, `from` names are story speakers, the pack has no URLs, the worst case uses 28 of 50 writes per hook.

## Findings

- Players talk outside the game (Carl's decision); no chat between players is added.
- Open, as a core task (thread "GM role and online players in Lua"): `player.gm` so the world's GM leads (`lib/case.lua` reads it already), and `world.players()`.
- The image build (Dockerfile) is checked by CI, not here.
