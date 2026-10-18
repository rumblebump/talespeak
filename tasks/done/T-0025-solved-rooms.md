---
id: T-0025
title: Solved rooms and Whisp's warnings in the demo
type: feature
priority: medium
created: 2026-10-18
depends: [T-0021, T-0023]
---

## Goal

A pack can mark a room as solved for a player: going back is then refused, or Whisp the guide asks first ("Do you really want to get locked in again?"). The demo uses both, and the guard's countdown is one for all rooms.

## Acceptance

- [x] Lua only, no engine change: `solve(id, how, text)`, `solved(id)` and `go(id)` over `state.player` (T-0023); `how` is `ask` or `locked`
- [x] Demo: getting out of the cell solves it with `ask` and Carl's question; taking the key solves the corridor with `locked` and hides its door
- [x] Whisp speaks through one `whisp()` helper, plain "Whisp: ..." text until the engine has a guide voice
- [x] The guard's time away is one countdown in `state.player` that every room counts, so walking between rooms no longer resets it
- [x] Pattern documented for pack authors in `docs/pack-patterns.md`
- [x] Go walkthrough tests and the Playwright demo test cover asking, saying no, saying yes, the locked corridor and the shared countdown

## Notes

Asked by Carl 2026-10-18: "sometimes its necessary to go back, sometimes not.. so in lua, one can define if a room is "solved" and locked (you already have been there and there is no reason to go back, or .. do you really want to get locked in again .. message from whisp.. the guide)".

Each room script carries a copy of the helpers until packs can share a module (`require`, T-0026, coming from the core thread). A guide voice (`say(text, {from = "whisp"})`, shown apart from the story) is coming as T-0027; switching to it is a change in `whisp()` and `whisp_ask()` only.
