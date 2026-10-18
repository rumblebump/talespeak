---
id: T-0019
title: Free and turn-based rounds in a room
type: feature
priority: high
created: 2026-10-18
depends: [T-0004, T-0012, T-0018]
---

## Goal

A room or an encounter can run free (everyone answers and acts at once, for a class quiz or exploring) or in turns (DnD fights: a set order, and the next player acts only once the current one is done).

## Acceptance

- [ ] Lua: `round.free()` and `round.turns(order)` for the room's players, with order from the script (for example initiative rolls); `round.done(player)`, `round.current()`, `round.next()`; hooks `on_turn(player)` and `on_round_end()`
- [ ] In turns mode, input, clicks and answers from players whose turn it is not get an `error` ("Wait for your turn: Mira is acting."); the client shows whose turn it is and the order
- [ ] A player ends their turn with a "done" button or the `done` command, or the script ends it; a turn can have a deadline (T-0012 timers) after which the script decides (skip, default action)
- [ ] Players leaving or joining mid-round: the script decides; by default a leaver is skipped and a joiner waits for the next round
- [ ] The GM can skip a turn, change the order, or switch the room to free mode; every change in the audit log
- [ ] Round and turn state stored with the world state (T-0018), so a restart continues the same turn
- [ ] Tests: Go (order, done, skip, leave, deadline with a fake clock) and Playwright (two players, one waits)

## Notes

Asked by Carl 2026-10-18: "let the rounds (or rooms) be chaotic, so everyone can answer, ... or like in dnd fights.. there is a certain order and a player needs to be done so the next one can do his commands."

Today (T-0004) every player has their own instance of each room script, so rooms are free by default; turns need the shared world state of T-0018.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
