# 2026-10-18: Demo corridor and guard room

**Result:** the demo goes on after the cell: corridor, guard room and the escape through the back door; all checks green.
**Task:** T-0021
**Environment:** local cloud container, 4 cores, Go 1.26.9, branch `claude/project-thread-77nxy0` on dev 4e7a84a (room.go from T-0022, PR #13); Playwright 1.56.1 with Chromium.

## Steps

```sh
# formatting, vet and all Go tests with the race detector
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

```text
(gofmt: no files)
ok  github.com/rumblebump/talespeak/internal/game   32.9s
... every package ok
```

```sh
# browser tests against the real binary, including the new demo walk
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e && npx playwright test
```

```text
✓ demo: guard, straw, wall map, riddle, corridor and escape (chromium, phone)
✓ demo: caught on the stairs after leaving the cell (chromium, phone)
64 passed (29.5s)
```

## Findings

- **test-engineer agent:** added tests for typing the whole way out, every corridor and guard room keyword, box codes (`digits()`), repeats, coming back to the cell and corridor, and two players apart. It found typed lines that went to the wrong room: "open the cell door" in the corridor went to the guard room, "go back to the guard room" to the cell, "open the backdoor" in the guard room out to the corridor, and the cell had no word to leave again. Fixed: words are matched whole (`spaced`/`word`) and checked from the most to the least specific; `TestDemoWordsGoWhereMeant` and `TestDemoWordsInRooms` cover them. Also fixed: the corridor no longer says "water is still splashing" when the guard is about to come back. Added the Playwright test for getting caught after the cell.
- **security-reviewer agent:** no findings. Typed text echoed by the scripts is capped and cleaned by the engine and goes only to the player; every `room.go` target is fixed; 3 rooms stay under the 4 kept instances; the pack has no URLs, YAML tags or web files (T-0014 lint). It also saw `TestUndoneMovesKeepNoInstance` fail under `-race` on the T-0022 branch; the core thread fixed it in PR #14 before this branch was tested again.
- govulncheck could not run in this container (the proxy refuses vuln.go.dev); CI runs it.
- Known limit: each room counts the guard's time on its own, so walking between rooms resets his return. One shared countdown comes with `state.player` (T-0023) or timers (T-0012).
