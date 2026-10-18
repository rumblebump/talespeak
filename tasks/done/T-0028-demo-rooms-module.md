---
id: T-0028
title: Demo helpers in one shared Lua module
type: chore
priority: low
created: 2026-10-18
depends: [T-0025, T-0026]
---

## Goal

The demo's shared helpers (Whisp, the guard's countdown, solved rooms) live once in `content/demo/lib/rooms.lua` instead of a copy in every room script.

## Acceptance

- [x] `content/demo/lib/rooms.lua` holds `whisp`, `whisp_ask`, `keep`, `guard_away`, `set_guard_away`, `solve`, `solved` and `go`; its top level only defines functions (it also runs at world start, for no player)
- [x] Every demo room script starts with `require("rooms")` and has no copy left
- [x] `docs/pack-patterns.md` tells pack authors to put shared helpers in `lib/<name>.lua`
- [x] The demo tests pass unchanged; the helper test checks the module and that no room keeps a copy

## Notes

Follows T-0026 (`require`, core thread). The guide voice (T-0027) is paused by Carl, so `whisp()` stays plain "Whisp: ..." text.
