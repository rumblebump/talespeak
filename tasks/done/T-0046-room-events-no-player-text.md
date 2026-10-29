---
id: T-0046
title: Room events replayed on reconnect, no player text to other players
type: feature
priority: high
created: 2026-10-29
depends: [T-0011, T-0018]
---

## Goal

A player who connects again reads what happened in their room meanwhile. Players never chat: what one player types never reaches another, only game masters send messages to a room or the world, and no pack can make a line look like a game master's, the server's or a player's.

## Acceptance

- [x] Core primitive `room.event(text [, {all = true}])`: the other players in the room read it at once (the player whose hook runs too with `all`); the world keeps the last 10 events of each room in memory only, and a player who connects gets them again (`event` with `replay`), the client showing each only once
- [x] `/reset room` forgets the room's events
- [x] No chat between players: typed lines, typed answers and room command arguments only run the player's own hooks
- [x] What players type never reaches other players: `state.world` writes, room events, room timer labels and `world.finish` texts that hold a word a player typed lately are refused (nil and the reason); the pack's own words, players' names and a game master's words in GM mode are not refused
- [x] Game masters send messages with `/say TEXT` (the world) and `/sayroom ROOM TEXT` (a room); they are audited, never kept by the engine and not kept in the browser's story log either
- [x] Packs cannot fake a sender: `from` names that read as the game master, an admin, a teacher or the server are a script error; a `from` that is a player's name in the world is dropped from the line; self-registered accounts cannot take such names
- [x] Demo clearing tells its news with `room.event` (demo 1.3.0)
- [x] Tests (Go and Playwright), docs, the task files this replaces (T-0018's chat ring and edu filter, T-0006)

## Notes

Carl 2026-10-29: "forget about the chat feature in general": a ring of the latest messages of what happened in the room (someone opened something, did something), at most 10, replayed when a player reconnects; users never chat (in edu they sit next to each other, or use Discord or the like); only GMs send messages into a room or a world with a command, and those are temporary, not in the ring; kids cannot send their private data, as answers are defined and only trigger actions (open the door, give the player this, convince the king to give gold); faking a sender must not be possible.

Core vs Lua: the engine gives the primitive (`room.event`, the ring, the replay) and the rules at the boundary between players (the typed-words check, speaker names). What a room tells as an event is the pack's.

Done 2026-10-29: protocol/2026-10-29-t-0046-room-events-no-chat.md. After the reviews, the strings of a player's `state.player` and `state.profile` count as typed by them when they join (an answer a pack kept cannot be shared after a restart), a player's typed words are forgotten when they leave, split phone numbers (every 6 digits in a row) and whole numbers count, and `room.event` returns nil in `on_change`. Left for later: a sound of its own for a game master's line read aloud; a narrower pack vocabulary (string literals only).
