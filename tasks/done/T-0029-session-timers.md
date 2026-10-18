---
id: T-0029
title: Timers and question timeouts for the session, first slice of T-0012
type: feature
priority: high
created: 2026-10-18
depends: [T-0003]
---

## Goal

Packs can put time pressure on a scene ("the guards arrive in 5 minutes", "decide in 30 seconds"), and the server can send output the player did not ask for. Turns (T-0019) and items that wear off (T-0020) build on it.

## Acceptance

- [x] Core: server push. A session can queue messages at any time (a timer ran out); the connection sends them in order with the replies, and a client that falls too far behind is disconnected
- [x] Lua: `timer.start(name, seconds [, {show = label}])`, `timer.cancel(name)`, `timer.left(name)`; `on_timer(player, name)` runs in the player's current room when a timer runs out. At most 10 timers per player, 1 second to 1 day
- [x] Lua: `ui.dialog`, `ui.select` and `ui.ask` take `timeout = seconds`; once it passes the question closes and returns nil, and the script decides what happens
- [x] A hook run by a timer while a question is open cannot ask another; `room.go` from it closes the open question
- [x] Timer hooks count toward the player's Lua time; when it is used up they run a little later
- [x] Client: shown timers count down from the time left the server sent; a question with a timeout shows its countdown; a `close` message closes a question
- [x] Tests with a fake clock (Go, no sleeping), a WebSocket push test, and a Playwright test

## Notes

Slice of T-0012. Timers last for the session: storing them with absolute deadlines in SQLite, restart, in-game time, pausing worlds, GM controls, extra-time factors and edu minimum deadlines stay in T-0012.

Core vs Lua (Carl 2026-10-18): timers are a core primitive; what happens when one runs out is Lua.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
