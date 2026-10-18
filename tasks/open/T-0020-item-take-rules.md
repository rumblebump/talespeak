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

- [ ] Take rule per item in the room YAML, readable and changeable from Lua:
  - `split`: an amount (gold) shared among the players present when it is taken; the script decides what happens to a remainder, by default it stays in the room
  - `each`: every character can take one, once
  - `once`: only the first player who takes it gets it; for the others it is gone
  - `count: N`: anyone can take one until the N are gone
- [ ] Temporary items and effects (a blessing): they last for a time (real or in-game, T-0012 timers) or a number of moves, then dissolve, and the script gets a hook when they do
- [ ] Lua: `items.take(player, id)` applies the rule and returns what the player got (or nil and why), plus hooks for the pack to say it in its own words
- [ ] Taking is atomic for the whole world: two players clicking a `once` item at the same moment, only one gets it; a `count` never goes below zero
- [ ] What is left in a room and who took what is world state (T-0018), so it survives a restart; taken items go into the character's inventory (T-0018)
- [ ] The clickable items each player sees follow the rule (an item gone for everyone disappears for everyone; an `each` item disappears only for the player who took it)
- [ ] Tests: Go (every rule, simultaneous takes, expiry by time with a fake clock and by moves, restart) and Playwright (two players, one `once` item)

## Notes

Asked by Carl 2026-10-18: "items in the room, they can be shared (like gold, split), or everyone gets one (or like a temporary blessing, which dissolves after a few minutes, hours or a few moves) or only one can take it. (or if there are more of it, every one can "take" till the count is depleted)"

Today (T-0004) every player has their own instance of each room script, so all items behave like `each` and nothing lasts past the session.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
