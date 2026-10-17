---
id: T-0003
title: Lua runtime, YAML room and dialog coroutine
type: feature
priority: high
created: 2026-10-17
depends: [T-0002]
---

## Goal

One room defined in YAML runs a Lua on_enter hook, and ui.dialog() pauses the script until the player clicks an option.

## Acceptance

- [x] gopher-lua sandbox with time and memory limits
- [x] Room YAML loader
- [x] ui.dialog coroutine round-trip over WebSocket (a blocking hook goroutine instead of a coroutine yield; see docs/lua-sandbox.md)
- [x] Protocol entry for the manual test: protocol/2026-10-17-t-0003-lua-runtime.md

## Notes

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
