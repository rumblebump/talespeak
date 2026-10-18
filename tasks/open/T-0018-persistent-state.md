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

- [ ] Lua API: `state.player` (per character in this world), `state.world` (shared by the world's players), `state.game` (per pack, GM-controlled), each with `get(key)` and `set(key, value)`; plain values only (nil, booleans, numbers, strings, tables of them). Only what a script sets explicitly is stored: globals are never saved automatically
- [ ] World state and the players' state in it are deleted when the world is finished
- [x] Character position (room) is saved on every room change; a player who comes back continues where they left
- [ ] Death setting per world, default from the pack, set by the GM when the world is created: dead stays dead, revive (by a spell or another script action), or respawn at the last save point; save points are set by scripts
- [ ] Inventory stored per character, ruleset version and world; on finishing, it can move to another world whose pack accepts that ruleset (the compatibility rules of T-0010)
- [ ] No chat log: the engine keeps only the last 10 chat messages per world in memory, for players who join, and never writes chat to the database (edu GM transcript excepted, T-0011)
- [x] Stored through the Store interface (SQLite now, Postgres later), written after each hook, read before it; no SQL in game code
- [ ] Hard limits so no one can fill the database, configurable like the other resource limits (docs/admin-limits.md). Starting defaults, small on purpose: key 64 bytes, value 16 KiB encoded, table depth 8; per character 256 KiB and 500 keys; per world 4 MiB and 5000 keys; per game 1 MiB; at most 50 writes per hook call. A write past a limit fails in the script (`set` returns nil and an error), never in the engine, and the GM log shows it
- [x] Writes count toward the client's Lua budget (T-0004), and a world writes to the database at most once per hook call, batched, so many players cannot flood the disk
- [ ] Edu mode: strings written into `state.world` or `state.game` pass the chat filter and appear in the GM transcript (typed text shared between players is chat)
- [x] Changing world state notifies the other players' sessions (for example a door opened by one player)
- [ ] A restart continues the same round and turn: the turn library keeps them in `state.world` (`round_<room>`, T-0019), and room timers (T-0031) are stored with the other timers (T-0012)
- [x] Player ids (`player.id`, T-0031) never repeat across restarts once `state.world` lasts, or a new `p1` would inherit the old `p1`'s items and turns (from the T-0031 security review); with accounts they become character ids
- [ ] Tests: restart keeps state, two players see world state, player state stays private, limits, fuzzed values

## Notes

Done in T-0034 (2026-10-18): `state.player` and `state.world` stored in SQLite and saved after every hook, the room saved on every move, character ids as `player.id`, restart and privacy tests (`internal/game/store_test.go`). Still open here: `state.game`; finishing a world from the game (the store's `FinishWorld` deletes the state already, nothing calls it yet); death modes and save points; inventories per ruleset version and moving them; the edu chat filter on shared strings; the state limits as settings; the round's turn deadline after a restart (room timers, T-0012).

Core vs Lua (Carl 2026-10-18): the engine stores state, positions and inventories and moves inventories between worlds; what death, revival, save points or inventory rules mean in a game is Lua (a ruleset library), which reads and writes that state. The world's death setting is a stored value the script reads.

Carl 2026-10-18 (death, inventory, position, no chat log, world state deleted when the world ends): see the acceptance items above. Pack authors write state with explicit calls rather than saved globals, because globals hold functions and temporary values and would make every variable shared or lasting by accident.

Decided by Carl 2026-10-17: script globals are per player and only live as long as the session (T-0004); everything that must last is stored in the database.

From the T-0004 security re-review: every player runs each room's top level and keeps its own copy, so a 10000-word list costs about 1.2 MiB per player and room. Read-only data shared by all players (word lists, tables loaded once per world, frozen) would make a player cost a few KiB and remove the need for the per-address memory share as a capacity limit.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
