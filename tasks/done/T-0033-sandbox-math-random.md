---
id: T-0033
title: Sandbox math.random without a process-wide seed
type: chore
priority: low
created: 2026-10-18
depends: []
---

## Goal

No pack can make another world's random numbers predictable: `math.randomseed` is off in the sandbox and `math.random` draws from a source no Lua can seed.

## Acceptance

- [x] `math` in the sandbox is an allowlist like `string` and `table`; `math.randomseed` is left out or raises an error
- [x] `math.random` is wrapped around `math/rand/v2` (no global Seed), with bad arguments a script error, not a Go panic
- [x] `docs/lua-sandbox.md` and the sandbox surface test change with it

## Notes

Found by the security review of T-0032 (low, older than T-0032): gopher-lua's `math.randomseed` calls Go's deprecated global `rand.Seed`. With go 1.26 in go.mod that is a no-op (`randseednop=1`), but a server started with `GODEBUG=randseednop=0` would let one pack fix the sequence for every world (box codes, dice). Core primitive work, not pack content.

Done 2026-10-24: `internal/script/random.go` builds the sandbox's `math` from an allowlist without `randomseed`; `math.random` uses `math/rand/v2` (arguments floored, NaN, empty intervals and values past ±2^53 are script errors). Tests: `random_test.go`, the surface test in `offline_test.go`.
