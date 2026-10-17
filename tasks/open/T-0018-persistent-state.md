---
id: T-0018
title: Player, world and game state stored in SQLite
type: feature
priority: high
created: 2026-10-17
depends: [T-0004, T-0010]
---

## Goal

Scripts keep progress in the database, not in Lua variables: a player's progress survives a reconnect and a server restart, and a party or class shares world state on purpose, never by accident.

## Acceptance

- [ ] Lua API: `state.player` (per profile and pack), `state.world` (shared by the world's players), `state.game` (per pack, GM-controlled); plain values only (nil, booleans, numbers, strings, tables of them)
- [ ] Stored through the Store interface (SQLite now, Postgres later), written after each hook, read before it; no SQL in game code
- [ ] Limits per key, per player and per world, configurable like the other resource limits (docs/admin-limits.md)
- [ ] Edu mode: strings written into `state.world` or `state.game` pass the chat filter and appear in the GM transcript (typed text shared between players is chat)
- [ ] Changing world state notifies the other players' sessions (for example a door opened by one player)
- [ ] Tests: restart keeps state, two players see world state, player state stays private, limits, fuzzed values

## Notes

Decided by Carl 2026-10-17: script globals are per player and only live as long as the session (T-0004); everything that must last is stored in the database.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
