---
id: T-0027
title: A guide voice the client shows apart from the story
type: feature
priority: medium
created: 2026-10-18
depends: [T-0004]
---

## Goal

A pack's guide (Whisp in the demo) speaks apart from the story: the player sees who says a line, and read aloud uses another voice. The content thread asked for it so the demo no longer writes "Whisp: ..." into the story text. It is a UI primitive; who the guide is and what it says stay in the pack's Lua.

## Acceptance

- [x] `say(text, {from = "Whisp"})`, and `from` on `ui.dialog`, `ui.select` and `ui.ask`: 1 to 40 bytes on one line
- [x] The protocol sends `from` on `text`, `dialog`, `select` and `ask` messages
- [x] The client shows the name with the line or question, styled apart from the story, and reads it with a second local voice of the language, or the same voice pitched up
- [x] The demo uses it: `whisp` and `whisp_ask` in `content/demo/lib/rooms.lua` (moved there by T-0028)
- [x] Tests: Go for the Lua arguments, Playwright for the display and the voice
- [x] Docs: `docs/lua-sandbox.md`, `docs/websocket-protocol.md`, `docs/pack-patterns.md`

Carl 2026-10-18: built without the review agents this time; the next big feature gets the full review.

Design: the Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
