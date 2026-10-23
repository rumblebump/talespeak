---
id: T-0012
title: Timers and decision deadlines
type: feature
priority: high
created: 2026-10-17
depends: [T-0003]
---

## Goal

Packs can put time pressure on a scene ("escape before the guards arrive in 1 hour", "decide in 5 minutes"), and timers survive reloads and server restarts.

## Acceptance

- [x] `timer.start()`, `timer.cancel()`, `timer.on_expire()` in Lua; `ui.dialog(..., { timeout, default })` (T-0029: `on_timer`, and the default is the script's `or 2`)
- [x] Timers stored in SQLite with absolute deadlines; one scheduler fires due timers; restart resumes them (each timer is a clock `after` of its world, no polling scheduler)
- [ ] Real-time and in-game-time timers; pausing a world pauses its timers
- [ ] GM can pause, extend and cancel; every change in the audit log
- [ ] Per-profile extra-time factor; edu worlds can set a minimum deadline
- [x] Client shows the countdown from the server deadline only
- [x] Tests with a fake clock (Go) and `page.clock` (Playwright), no sleeping (the reload test reads the countdown the engine sends, no clock needed)

## Notes

Done 2026-10-23 (lasting timers): player timers and question deadlines in `player_timers` per character, room timers in `room_timers` per world (migration 0006), written with the save of the hook that changed them (`internal/game/lasting.go`, `internal/store/sqlite/timers.go`). Room timers come back when the world loads, a player's when they join; what ran out meanwhile fires at once (after `on_enter`). A room timer that runs out in an empty room now waits for the next player who comes in, instead of being dropped, so a deadline that passed while the server was down still counts. A paused hook cannot outlive a restart, so a question's deadline lasts by identity: the same room asking the same question (kind, title, options) again gets only the time left. Docs: lua-sandbox.md "Timers that last".

Still open here: in-game-time timers and pausing a world's timers; GM pause, extend and cancel with audit entries; the per-profile extra-time factor and an edu minimum deadline. These need a design decision first (what a paused world shows, which GM command words).

Since T-0034 a world's `state.world` and `state.player` survive a restart, but player timers, room timers (T-0031) and question timeouts do not: the turn library's deadline (`lib/round.lua`) is lost on restart.

Section "Timers" in the design doc. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
