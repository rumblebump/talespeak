---
id: T-0030
title: state.world shared by a world's players, slice of T-0018
type: feature
priority: high
created: 2026-10-18
depends: [T-0023, T-0029]
---

## Goal

Players of one world share state on purpose: a door one player opens is open for everyone, and the others see it happen. Turn order (T-0019) and shared items (T-0020) are written in Lua on top of it.

## Acceptance

- [ ] `state.world.get(key)` and `state.world.set(key, value)` with the value rules of `state.player`; limits from T-0018: 4 MiB and 5000 keys per world, counted toward the world's script memory
- [ ] `state.world.update(key, fn)`: fn gets the current value and returns the new one, with no other hook running in between and no question asked inside fn, so two players taking a `once` item cannot both get it
- [ ] After a hook changes world state, the other players' current rooms get `on_change(player, keys)`, run as pushed output, charged to each player's own Lua time, coalesced while they wait. `on_change` cannot change world state (no ping-pong between players)
- [ ] Writes count toward the 50 writes per hook
- [ ] World state lasts as long as the world runs
- [ ] Tests: two players see one value, update under many players, notifications, limits

## Notes

Slice of T-0018 for the primitives T-0019 and T-0020 need. The database, restart survival, `state.game`, the edu chat filter for strings in world state and deleting world state when a world is finished stay in T-0018.

Core vs Lua (Carl 2026-10-18): the engine stores and announces changes; what a change means is Lua.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
