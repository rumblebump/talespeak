---
id: T-0022
title: Scripts move players between rooms
type: feature
priority: high
created: 2026-10-18
depends: [T-0003, T-0004]
---

## Goal

A pack can have more than one room: a script sends the player to another room of the pack, and that room takes over.

## Acceptance

- [x] Lua `room.go(id)` inside a hook moves the player once the hook ends: the clickable items become the new room's, and its `on_enter` runs (a room without a script is described)
- [x] An unknown id is a script error; waiting in `ui.dialog`, `ui.select` or `ui.ask` after `room.go` in the same hook is a script error, and a failing hook moves no one. A new room that cannot start (world or network full, Lua time used up) leaves the player where they were
- [x] The last `room.go` of a hook wins; `room.go` to the current room runs its `on_enter` again
- [x] The player keeps their instances of the last 4 rooms entered, so coming back to one finds its globals (the security review showed that keeping every room lets one player fill an address's memory share)
- [x] `player.room` (the current room id) in every hook's player argument
- [x] One player message moves through at most 4 rooms, so rooms cannot send a player round in circles or hold the world for long
- [x] After a move the client gets the new room's items, also when its `on_enter` does not describe it
- [x] Tests: Go (script and game packages) and Playwright (the test pack's cellar)

## Notes

Protocol: protocol/2026-10-18-t-0022-room-go.md.

Asked by the content thread for the demo (cell, corridor, guard room), which Carl chose 2026-10-18. A core primitive under Carl's rule: the engine moves players, packs decide when. Saving the position across reconnects stays with T-0018.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
