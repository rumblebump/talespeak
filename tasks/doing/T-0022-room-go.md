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

- [ ] Lua `room.go(id)` inside a hook moves the player once the hook ends: the clickable items become the new room's, and its `on_enter` runs (a room without a script is described)
- [ ] An unknown id is a script error; waiting in `ui.dialog`, `ui.select` or `ui.ask` after `room.go` in the same hook is a script error, and a failing hook moves no one
- [ ] The last `room.go` of a hook wins; `room.go` to the current room runs its `on_enter` again
- [ ] The player keeps their instance of each room's script for the session, so coming back finds its globals
- [ ] `player.room` (the current room id) in every hook's player argument
- [ ] One player message moves through at most 10 rooms, so rooms cannot send a player round in circles
- [ ] After a move the client gets the new room's items, also when its `on_enter` does not describe it
- [ ] Tests: Go (script and game packages) and Playwright (the test pack's cellar)

## Notes

Asked by the content thread for the demo (cell, corridor, guard room), which Carl chose 2026-10-18. A core primitive under Carl's rule: the engine moves players, packs decide when. Saving the position across reconnects stays with T-0018.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
