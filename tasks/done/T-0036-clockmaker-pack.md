---
id: T-0036
title: The Clockmaker's Silence, a co-op case pack with save spots
type: feature
priority: medium
created: 2026-10-22
depends: [T-0034, T-0035]
---

## Goal

A second story pack, `content/clockmaker`: a short Sherlock Holmes style escape game for 2 to 4 players who have to work together, in five levels with save spots. When the whole party reaches the next level it is saved with what everyone carries; when everyone leaves, the next time they join they continue from the last save spot. Pack content only (Lua mechanics), no Go changes.

## Acceptance

- [x] Story agreed with Carl (2026-10-22): London 1891, the players are Baker Street Irregulars, a vanished clockmaker and the Queen's stolen jubilee watch; five levels (221B Baker Street, the clock shop, the Thames warehouse, the Diogenes Club, Scotland Yard)
- [x] Co-op puzzles where each player sees only their own part: strips of a torn word, levers turned at the same moment, a spring-locked workshop and the clocks outside, a torn map picture (one piece per player), the hat pegs of the club, evidence cards and a unanimous accusation
- [x] Puzzles adapt to the party size, 1 to 4 (made for 2 to 4; one player can play alone); a fifth player waits outside
- [x] Save spot at the end of each level: the party moves on once every active player waits there; satchels are saved
- [x] Everyone leaves in the middle of a level: on return (also after a restart) the level starts again from its save spot, satchels as they were there; one player leaving alone loses nothing
- [x] The lead (the world's GM once scripts can tell, until then the first player) decides to go on without players who are not there, or to play the level again with only those here; what those players carried goes to the lead, and their seats are freed for players waiting outside
- [x] Players talk outside the game (Carl 2026-10-22); no in-game chat
- [x] Shipped in the container image (`/packs`, `TALESPEAK_PACKS`), so the lobby on staging offers it
- [x] Go tests play the whole case with 1 to 4 players, the save spot after a restart, going on without a player and playing a level again

## Notes

Core primitives asked for in a separate core task (thread "GM role and online players in Lua"): `player.gm` in hooks, so the GM decides instead of the first player (`lib/case.lua` reads `player.gm` already), and `world.players()`, so a restart while players are connected cannot leave stale entries in the pack's own online list (then the level continues where it was instead of starting again from the save spot).

The torn maps are drawn by `scripts/clockmaker-maps.py` (Python standard library only). Images are served at a guessable path, so a determined player can open the other pieces: fine for a game, not a secret.

Protocol: [2026-10-22-t-0036-clockmaker-pack.md](../../protocol/2026-10-22-t-0036-clockmaker-pack.md)

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
