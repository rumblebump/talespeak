# 2026-10-29: room events replayed on reconnect, no player text to others (T-0046)

**Result:** `room.event` tells the other players in a room what happened, and each room's last 10 events are kept in memory and sent again to a player who connects; the client shows each once. Players never chat: words a player typed are refused in everything shared (`state.world`, room events, room timer labels, `world.finish`). Game masters message a room (`/sayroom`) or the world (`/say`), never kept. Pack voices cannot be staff or players. All checks are green.
**Task:** T-0046
**Environment:** local container, Go 1.26.10, branch `claude/project-thread-s5y3wo` on top of dev `304cb99`; Playwright with the preinstalled Chromium (desktop and phone projects).

## Steps

```sh
# format, vet, all Go tests with the race detector, the code map, the client checks
gofmt -l .
go vet ./...
go test -race -count=1 ./...
scripts/check-codemap.sh
scripts/check-web.sh
```

```text
(gofmt: no files)
ok  	github.com/rumblebump/talespeak/internal/game	...
ok  	github.com/rumblebump/talespeak/internal/script	...
(every package ok)
ok alpine-csp 3.17.4 web/static/vendor/alpine-csp.js
```

```sh
# browser tests against a fresh build (config with launchOptions.executablePath /opt/pw-browsers/chromium)
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e && npx playwright test
```

```text
  3 skipped
  123 passed (2.9m)
```

```sh
# fuzz targets, briefly: input handling changed
scripts/fuzz.sh
```

## Test engineer

Added `internal/game/typed_test.go`, `internal/game/events_edge_test.go`, more cases in `internal/script/events_test.go` and `internal/account/names_test.go`, fuzz targets `FuzzTyped` and `FuzzReservedName` (10 s each, nothing found). Coverage: `internal/game` 96.0%, `internal/script` 97.4%, `internal/account` 90.6%.

Bugs it found, with failing tests (`typed_bugs_test.go`, `events_bugs_test.go`), all fixed:

- `ReservedName` missed staff names in mixed case (`GMCarl`, `ADMINBOB`, `SERVERbot`, `AdMin Bob`): words are now also split where a word in capitals starts and at every change of case, and checked in any case.
- `words` split words at combining marks (`Jörg` typed with a separate umlaut, Hindi): marks belong to their word now.
- The player limit dropped the first player to type, not the least recent: the order follows the latest typing, also per word.

Observations taken up: numbers were not checked (fixed, below); short digit groups got through (fixed, below); the typed words' memory (fixed, below). Left as is: a word a GM put into `state.world` blocks its later writes once a player types it (rare, and refusing is the safe side); two runs of a world in the same millisecond share an id prefix (only with fake clocks).

## Security review

No critical findings.

- **High, fixed:** the guard forgot after an unload or restart while answers a pack stored lasted. Now, when a player joins, the strings of their `state.player` and `state.profile` count as typed by them; and a player's typed words are forgotten when they leave, since nothing of theirs is in a script any more, only in that state.
- **Medium, fixed:** short tokens, invisible characters and marks. Format characters (zero-width space, soft hyphen) are dropped before splitting, marks are part of words, words of 2 letters count (3 with a digit, so dice rolls and `p3` stay free), and every 6 digits in a row of a line's digits joined are remembered (`06 12 34 56 78`).
- **Medium, fixed:** numbers in `state.world` were not checked: whole numbers of 3 digits or more are checked as text.
- **Medium, fixed:** `room.event` from `on_change` multiplied by the players: it returns nil and the reason there, as `state.world` writes do.
- **Low, fixed:** typed words kept the whole line alive (`strings.Clone`), at most 500 players remembered, forgotten on leave.
- **Low, fixed:** `ReservedName` bypasses: run-together and plural forms (`gmcarl` aside: a small `gm` start is too common in real names like Gmelin), German (`Lehrer`, `Spielleiter`), `Dungeon Master`, `DM`, fullwidth and accented forms, Latin mixed with Cyrillic or Greek.
- **Low, fixed:** `voice` compared only exact names of players online: it now drops a `from` that is, or starts with, the name of any player who joined this run.
- **Low, not fixed:** a pack line read aloud can sound like a game master's ("Game master X says: ..."). The look of a GM line (box and GM badge) is what no pack line gets; a sound of its own for GM lines is a client follow-up.
- **Low, not fixed:** the pack vocabulary is wide (all Lua, comments included), so first names that are also code words ("Max") are not remembered. They are names, which other players see anyway; vocabulary from string literals only would need a Lua lexer. A kid could also block a pack's dynamic numbers by typing many of them (griefing only the sharing of those numbers).
- **Low, not fixed:** room timer names (`[a-z0-9_-]`, in the timer's id, not shown) are not checked; packs and tests use typed words as timer names, and the label, which players see, is checked.

`govulncheck` could not be downloaded in the sandbox.
