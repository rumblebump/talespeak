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
- [x] Real-time and in-game-time timers; pausing a world pauses its timers (`{game = true}`, `world.time()`, `world.paused()`)
- [x] GM can pause, extend and cancel; every change in the audit log (`/pause`, `/resume`, `/timers`, `/extend N TIME`, `/cancel N`)
- [x] Per-profile extra-time factor; edu worlds can set a minimum deadline (per world member until T-0010 has profiles: `/extratime NAME FACTOR`; `/mintime` per world, any mode)
- [x] Client shows the countdown from the server deadline only
- [x] Tests with a fake clock (Go) and `page.clock` (Playwright), no sleeping (the reload test reads the countdown the engine sends, no clock needed)

## Notes

Done 2026-10-23 (lasting timers): player timers and question deadlines in `player_timers` per character, room timers in `room_timers` per world (migration 0006), written with the save of the hook that changed them (`internal/game/lasting.go`, `internal/store/sqlite/timers.go`). Room timers come back when the world loads, a player's when they join; what ran out meanwhile fires at once (after `on_enter`). A room timer that runs out in an empty room now waits for the next player who comes in, instead of being dropped, so a deadline that passed while the server was down still counts. A paused hook cannot outlive a restart, so a question's deadline lasts by identity: the same room asking the same question (kind, title, options) again gets only the time left. Docs: lua-sandbox.md "Timers that last".

Done 2026-10-27 (design calls 4 to 8 of the "Design calls for the open tasks" doc): every timer counts on the **world clock** (the wall clock less the time the world was paused) or the **game clock** (runs unpaused with a player online), `internal/game/clock.go`. Deadlines are times on their clock, so `/pause` stops every timer without rewriting one, also those of players who are away, and the store keeps them as they are (`worlds.paused_at`, `paused_ms`, `game_ms`, `min_time_ms`, timers' `game` column; migration 0007). A timer is armed (`w.after`) only while its clock runs; its `fire` checks `w.ready`, so a callback that comes early or during a pause arms it again instead of firing. While paused, players' lines, clicks, answers and room commands get "The game is paused."; GMs in GM mode act. GM commands in `gmtime.go` and `timertools.go`, audited before they act; the client shows a paused line and freezes its countdowns (`web/static/pause.js`, the `pause` message, `question:<id>` timer messages). Extra time: `world_members.extra_time` (1 to 3), stretches question timeouts and shown timers that start afterwards (a room timer: the largest factor in the room); `/mintime` raises shorter ones; secret timers keep their time. Tests: `internal/game/clock_test.go`, `clock_store_test.go`, `timertools_test.go`, `gmtime_test.go` (fuzz `FuzzParseDuration`), `internal/store/sqlite/clock_test.go`, `internal/script/clock_test.go`, `e2e/tests/pause.spec.js`.

Left open: `/timers` lists only the timers in memory (rooms, players online); an away player's timers wait in the store unlisted. The extra-time factor moves to profiles with T-0010. The lobby's members list shows and sets extra time in the merging session's work (the store has `Member.ExtraTime` and `SetExtraTime`).

Section "Timers" in the design doc. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
