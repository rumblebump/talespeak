---
id: T-0031
title: Who is in a room, leaving the world, and room timers
type: feature
priority: high
created: 2026-10-18
depends: [T-0029, T-0030]
---

## Goal

The core primitives turn order (T-0019) and shared items (T-0020) need, so both can be written in Lua: a script knows which players are in its room, hears when a player leaves the world, and can run one timer for the whole room that everyone in it sees.

## Acceptance

- [x] Every hook's `player` has an `id`, unique in the world for as long as it runs, and players of one world get different names until accounts exist (T-0010)
- [x] `room.players()` lists the players in the caller's room, `{id, name}`, in the order they came in
- [x] `on_leave(player)` runs in the room a player is in when they leave the world, even when their address has no Lua time left (it is charged): it can change `state.world`, its output reaches no one, it cannot ask
- [x] Room timers: `timer.start(name, seconds, {room = true, show = label})`, `timer.cancel` and `timer.left` with `{room = true}`. One per name per room, at most 10 per room; everyone in the room sees a shown one, and players see it appear and go as they enter and leave the room. When it runs out, `on_timer(player, name, "room")` runs once, for the player who started it if still in the room, else for the player who came in first (and the next one when a player has no Lua time left); in an empty room it is dropped. A room timer started where `state.world` is read-only stays read-only
- [x] Changes to room timers reach the other players in the room as one message per timer with the latest state, at most every 250 ms per world (the player who changed it sees it at once)
- [x] Tests: Go with a fake clock (players, order, leave, room timers shown, moved, fired, dropped, busy), Playwright through T-0019 and T-0020

## Notes

Done 2026-10-18, protocol: `protocol/2026-10-18-t-0019-t-0020-t-0031-turns-items.md`. Player ids restart at `p1` with the world; T-0018 must change that before `state.world` lasts.

Core vs Lua (Carl 2026-10-18): only what Lua cannot do itself. Turn order, take rules, what leaving means for a game: all Lua (T-0019, T-0020).

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
