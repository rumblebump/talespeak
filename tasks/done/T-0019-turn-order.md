---
id: T-0019
title: Free and turn-based rounds in a room
type: feature
priority: high
created: 2026-10-18
depends: [T-0030, T-0031]
---

## Goal

A room or an encounter can run free (everyone answers and acts at once, for a class quiz or exploring) or in turns (DnD fights: a set order, and the next player acts only once the current one is done).

## Acceptance

Turn order is a game mechanic, so it is written in Lua: `lib/round.lua` in the demo pack, documented in `docs/pack-patterns.md` for pack authors to copy. The engine only gained the generic primitives it needs (Carl 2026-10-18), in T-0031: `room.players()`, `on_leave`, room timers and player ids, on top of `state.world` and `on_change` (T-0030).

- [x] Lua: `round.free(player)` and `round.turns(player, order, {seconds})` for the room's players, with the order from the script (initiative rolls) or the order players came in; `round.done(player)`, `round.current(player)`, `round.next(player)`; hooks `on_turn(player)` and `on_round_end(player, n)`, plus `on_turn_timeout`
- [x] In turns mode, actions from players whose turn it is not are refused by the library (`round.may_act`: "Wait for your turn: traveler 2 is acting."), and players see whose turn it is and the order (`round.status`, the turn's countdown shown to the whole room)
- [x] A player ends their turn with a "done" item or the `done` command (the demo's dice), or the script ends it; a turn can have a deadline (a room timer) after which the turn is skipped, or `on_turn_timeout` decides
- [x] Players leaving or joining mid-round: by default a leaver is skipped (also when they leave the world, through `on_leave`) and a joiner waits for the next round; the script can do otherwise
- [x] Tests: Go (order, done, skip, leave, join, deadline with a fake clock) and Playwright (two players at the dice, one waits)

Moved out, because they need other tasks first: GM commands as a hook, with skip, reorder and free mode in the audit log, to T-0011; continuing the same turn after a restart (the round is already in `state.world`) to T-0018.

## Notes

Asked by Carl 2026-10-18: "let the rounds (or rooms) be chaotic, so everyone can answer, ... or like in dnd fights.. there is a certain order and a player needs to be done so the next one can do his commands."

Today (T-0004) every player has their own instance of each room script, so rooms are free by default; turns need the shared world state of T-0018.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
