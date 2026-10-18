---
id: T-0004
title: Terminal-style web client renderer
type: feature
priority: high
created: 2026-10-17
depends: [T-0002]
---

## Goal

The browser client renders text, dialogs and clickable items sent as structured messages, and works at phone width.

## Acceptance

- [x] Message protocol documented in docs/
- [x] Text, dialog and clickable item renderers
- [x] Typed input (keywords) reaching a Lua `on_input` hook; clicks on items reaching `on_click`
- [x] Multiple choice (`ui.select`), several options picked at once
- [x] Pack images (`assets/`, served by the engine) shown as a popup with `ui.image`
- [x] Read aloud with the browser's local voices (speechSynthesis), off by default
- [x] Keyboard and mouse both work; usable at phone width; light and dark theme, large text option
- [x] Plain JavaScript (ES modules, no build step), Alpine.js CSP build vendored in web/static/vendor/ with version and checksum, own CSS
- [x] Runs under the strict CSP with no console violations; no external URL anywhere in web/
- [x] Untrusted text only via x-text or textContent
- [x] Playwright tests for every renderer (see T-0015)

## Notes

Scope widened by Carl 2026-10-17: typed input, multiple choice, read aloud and pack images. Speech to text is T-0017.


Decided by Carl 2026-10-17: plain JS with Alpine.js and self-written CSS (replaces the TypeScript and esbuild idea).

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
