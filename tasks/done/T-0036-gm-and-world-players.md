---
id: T-0036
title: player.gm and world.players() for packs
type: feature
priority: high
created: 2026-10-22
depends: [T-0031, T-0034]
---

## Goal

A pack knows which players are game masters of the world and who is online in the whole world, so it can let the GM decide whether a smaller party carries on and scale a room to the party's size (Sherlock co-op pack, Carl 2026-10-22). Core primitives only; what a game does with them is Lua.

## Acceptance

- [x] In every hook, `player.gm` is true when the player is a game master of this world (`world_members.role` is `gm`) and false otherwise; admins who are not members are not GMs
- [x] `room.players()` entries carry `gm` too
- [x] `world.players()` lists every player online in the world, across rooms, `{id, name, room, gm}`, in the order they joined; a player leaving (in `on_leave`) is no longer listed
- [x] It lists only who is connected now, so it stays right after a restart or crash, where `on_leave` does not run
- [x] Documented in `docs/lua-sandbox.md`, with the sandbox surface test updated
- [x] Tests: script level with fake hosts, game level with the fake clock and with SQLite across a restart, account level for the sign-in

## Notes

The role is read when the player connects: a player made GM while playing is one from their next connection.

GM tools and the audit log stay in T-0011, which now also ends the sessions of an account whose role in a world changes (from the security review).

Done 2026-10-22, protocol: `protocol/2026-10-22-t-0036-gm-and-world-players.md`.
