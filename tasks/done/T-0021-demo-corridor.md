---
id: T-0021
title: Demo corridor and guard room
type: feature
priority: medium
created: 2026-10-18
depends: [T-0004]
---

## Goal

The demo story goes on after the cell: the player walks the dark corridor, finds the code under the X from the wall map, opens the guards' key box and gets out through the guard room's back door, all while the guard is away refilling his bottle.

## Acceptance

- [x] Content only: two new rooms in `content/demo` (`corridor`, `guardroom`) with their Lua scripts; leaving the cell moves the player into the corridor instead of ending the story
- [x] Core primitive from the core thread: `room.go(id)` moves the player to another room of the pack
- [x] Puzzles need no state shared between rooms: the code is something the player reads in the corridor and types in the guard room
- [x] The guard's return still threatens the player in every room; getting caught ends the story in the well, getting out through the back door is the escape ending
- [x] Go walkthrough tests in `internal/game/demo_test.go` and the Playwright demo test cover the whole way out and getting caught in the corridor
- [x] `docs/websocket-protocol.md` demo exchange still matches (`TestDemoMatchesProtocolDoc`)

## Notes

Lua/pack content thread, asked by Carl 2026-10-18 (option 1 of the content plan: grow the demo past the cell).

Each room has its own script instance per player, so each room counts the guard's time away on its own. One shared countdown comes with per-player state (`state.player`, T-0023) or timers (T-0012).

Protocol: [2026-10-18-t-0021-demo-corridor.md](../../protocol/2026-10-18-t-0021-demo-corridor.md).
