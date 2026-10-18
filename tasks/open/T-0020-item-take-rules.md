---
id: T-0020
title: Room items that players share, split or use up
type: feature
priority: medium
created: 2026-10-18
depends: [T-0012, T-0018]
---

## Goal

A pack decides what happens when several players in a world take the same item: gold is split, everyone gets their own torch, only one player gets the crown, a chest holds three potions, or a blessing wears off after a while.

## Acceptance

The take rules are a game mechanic written in Lua, not engine code (Carl 2026-10-18: the core is only the glue between assets, web and UI; mechanics go in Lua, and the core only gains primitives such as timers and persistence). So this task is a Lua library plus the few core primitives it needs.

Core primitives (Go), only what Lua cannot do itself:
- [ ] Shared Lua modules for a pack or ruleset (for example `lib/items.lua`, loaded with `require`), so every room uses the same library
- [ ] An atomic update of world state (T-0018), for example `state.world.update(key, function(old) return new end)`, so two players taking a `once` item at the same moment cannot both get it and a count never goes below zero
- [ ] A way for a script to refresh what other players in the room see (their clickable items, a line of text) when one player changes shared state
- [ ] Move counting and expiry come from T-0012 timers and the hooks the engine already calls; nothing item-specific in Go

Lua mechanic (a library in the demo pack or a starter ruleset, documented for pack authors):
- [ ] Take rules per item: `split` (gold shared among the players present, remainder stays by default), `each` (every character once), `once` (first taker only), `count = N` (until none are left)
- [ ] Temporary items and effects (a blessing) that dissolve after a time (timer) or a number of moves, with a hook when they do
- [ ] `items.take(player, id)` returns what the player got or why not; the pack says it in its own words
- [ ] Each player's clickable items follow the rule: gone for everyone, or gone only for the player who took it
- [ ] Tests: Go for the primitives (atomic update under concurrent takes, require, refresh); Lua library tests through the game package; Playwright with two players and one `once` item

## Notes

Asked by Carl 2026-10-18: "items in the room, they can be shared (like gold, split), or everyone gets one (or like a temporary blessing, which dissolves after a few minutes, hours or a few moves) or only one can take it. (or if there are more of it, every one can "take" till the count is depleted)"

Today (T-0004) every player has their own instance of each room script, so all items behave like `each` and nothing lasts past the session.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
