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

- [ ] Message protocol documented in docs/
- [ ] Text, dialog and clickable item renderers
- [ ] Keyboard and mouse both work
- [ ] Plain JavaScript (ES modules, no build step), Alpine.js CSP build vendored in web/vendor/ with version and checksum, own CSS
- [ ] Runs under the strict CSP with no console violations; no external URL anywhere in web/
- [ ] Untrusted text only via x-text or textContent
- [ ] Playwright tests for every renderer (see T-0015)

## Notes

Decided by Carl 2026-10-17: plain JS with Alpine.js and self-written CSS (replaces the TypeScript and esbuild idea).

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
