# The Clockmaker's Silence

A co-op case for 2 to 4 players (one can play alone), about an hour long. London, 1891: the players are Baker Street Irregulars. Mr Abernethy the clockmaker has vanished, and the Queen's jubilee watch is gone from his shop.

Every player sees only their own clues, so the players have to talk to each other: on a call, or sitting together. There is no chat in the game.

| Level | Rooms | Working together |
| --- | --- | --- |
| 1. 221B Baker Street | study | The desk's word is torn into one strip per player; the levers that open the secret cupboard have to be turned at the same moment, one per player |
| 2. The clock shop | lane, shop, workshop | The workshop locks behind the first player in: the note inside names the clocks, the clocks are outside |
| 3. The Thames at night | wharf, warehouse | A torn map, one piece per player (pictures); only the lantern holder lights the warehouse; the lift needs two |
| 4. The Diogenes Club | club | Each player learns another player's hat peg. Try it without speaking! |
| 5. Scotland Yard | yard | Each player holds some evidence cards; everyone must accuse the same suspect |

The door or cab at the end of each level is a **save spot**: when every player waits there, the level is done, and everyone's satchel is saved. When everyone leaves the game in the middle of a level, the level starts again from its save spot the next time. The lead (the world's game master, else the first player online) can go on without players who left. The mechanics are in `lib/case.lua` and its parts `lib/case_*.lua`; how they work is described in `docs/pack-patterns.md`.

The map pictures are drawn by `scripts/clockmaker-maps.py`.
