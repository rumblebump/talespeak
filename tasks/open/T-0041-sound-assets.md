---
id: T-0041
title: Sound assets and ui.sound
type: feature
priority: medium
created: 2026-10-23
depends: [T-0040]
---

## Goal

Packs can play short sounds and background music from their own mp3 and ogg assets. Core primitive (Lua cannot play audio); what plays when stays pack Lua.

## Acceptance

- [ ] `ui.sound(name, {loop, volume})` and a way to stop music, documented in docs/lua-sandbox.md; a new WebSocket message in docs/websocket-protocol.md
- [ ] The client plays only the pack's own assets (same origin, CSP), never before the player's first click, with mute and volume settings kept in the browser
- [ ] Limits per message and per hook so a script cannot flood a player with sounds
- [ ] Demo or test-pack uses it once; e2e test, test-engineer and security-reviewer runs, protocol entry

## Notes

Sizes and types come from T-0040. Kids mode: no autoplay, mute always reachable.
