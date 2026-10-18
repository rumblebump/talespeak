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

- [ ] `timer.start()`, `timer.cancel()`, `timer.on_expire()` in Lua; `ui.dialog(..., { timeout, default })`
- [ ] Timers stored in SQLite with absolute deadlines; one scheduler fires due timers; restart resumes them
- [ ] Real-time and in-game-time timers; pausing a world pauses its timers
- [ ] GM can pause, extend and cancel; every change in the audit log
- [ ] Per-profile extra-time factor; edu worlds can set a minimum deadline
- [ ] Client shows the countdown from the server deadline only
- [ ] Tests with a fake clock (Go) and `page.clock` (Playwright), no sleeping

## Notes

Since T-0034 a world's `state.world` and `state.player` survive a restart, but player timers, room timers (T-0031) and question timeouts do not: the turn library's deadline (`lib/round.lua`) is lost on restart.

Section "Timers" in the design doc. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
