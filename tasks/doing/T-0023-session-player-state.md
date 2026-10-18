---
id: T-0023
title: state.player for the session, first slice of T-0018
type: feature
priority: high
created: 2026-10-18
depends: [T-0022]
---

## Goal

A player's progress follows them from room to room: what they found in the cell is known in the corridor. Packs use the T-0018 API now; T-0018 later stores the same values in the database without packs changing.

## Acceptance

- [ ] `state.player.get(key)` and `state.player.set(key, value)` with the T-0018 value rules: nil, booleans, finite numbers, strings and tables of them, copied on set and get
- [ ] Limits from T-0018: key 64 bytes, value 16 KiB, depth 8, 256 KiB and 500 keys per player, 50 writes per call; a refused write returns nil and the reason
- [ ] Shared by every room of one player, never by other players; kept for the session only
- [ ] Counts toward the memory limits of the player's address and the world, and is freed when the player leaves
- [ ] Tests in the script and game packages

## Notes

Asked by the content thread so the demo's rooms can share what the player did. `state.world` and `state.game`, the database and restart survival stay in T-0018.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
