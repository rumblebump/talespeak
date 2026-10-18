---
id: T-0024
title: Count the hidden memory of Lua tables
type: security
priority: high
created: 2026-10-18
depends: []
---

## Goal

A room script cannot keep or allocate memory that the limits do not see. The T-0023 reviews found three gaps in how gopher-lua tables are counted. These gaps are older than T-0023 and live in `internal/script/memory.go` and gopher-lua itself, not in `state.player`.

## Findings

- `retained()` walks tables with `ForEach`, which skips empty array slots. After `t[n] = true t[n] = nil`, gopher-lua keeps n slots of 16 bytes each, but they count as almost nothing. Over many calls, one Instance can keep far more than `PlayerMemory`.
- Removed hash keys stay in gopher-lua's key list and index map (see the "TODO tb.keys and tb.k2i should also be removed" in gopher-lua's `table.go`). They hold their strings, and `retained()` never sees them.
- `t[6e7] = true` makes gopher-lua append about 6e7 nil slots (about 1 GiB) in one Go loop. The call's memory watcher only stops the call after that.

## Acceptance

- [ ] `retained()` counts the array part's length and the key list's length (`tableSlots` in `state.go` already reads both lengths)
- [ ] A write to an array index far past the array's end is refused or goes to the hash part. This needs a patched or forked gopher-lua, or a check in the sandbox before the set; decide which, and keep `CGO_ENABLED=0`
- [ ] Tests for each gap, run with `-race`
- [ ] `docs/lua-sandbox.md` says what counts
