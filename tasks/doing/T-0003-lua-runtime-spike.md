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

- [ ] gopher-lua sandbox with time and memory limits
- [ ] Room YAML loader
- [ ] ui.dialog coroutine round-trip over WebSocket
- [ ] Protocol entry for the manual test

## Notes

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
