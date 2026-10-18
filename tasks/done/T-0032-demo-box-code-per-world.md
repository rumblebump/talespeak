---
id: T-0032
title: Demo key box code picked anew in every world
type: feature
priority: low
created: 2026-10-18
depends: [T-0030]
---

## Goal

The demo's key box no longer always opens with 417: every world picks its own three-digit code, and the note under the corridor's stone shows that code (Carl, 2026-10-18).

## Acceptance

- [x] `box_code()` in `content/demo/lib/rooms.lua` picks a code with `math.random` the first time a player needs it and keeps it in `state.world` (with `update`, so the first code stays even if picking ever waits; today the world runs one hook at a time anyway)
- [x] The corridor's note and the guard room's box both use it; no fixed code is left in the pack
- [x] Go tests: the players of one world read the same code, new worlds get different ones, the box works before anyone lifted the stone; the existing demo tests read the code from the note; with fixed dice, codes with leading zeros, which room picks first, a refused world write and a fuzz of the box answer
- [x] Browser tests read the code from the note
- [x] `docs/pack-patterns.md` shows the pattern for pack authors

## Notes

Lua only (core vs Lua rule): `math.random` is already in the sandbox and draws from Go's auto-seeded `math/rand`, so no core change. The code lasts as long as `state.world` does: for now one server run (a restart or deploy is a new world); once T-0018 keeps `state.world` in the database, a world keeps its code across restarts.
