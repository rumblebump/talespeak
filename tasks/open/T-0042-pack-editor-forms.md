---
id: T-0042
title: Pack editor, part 1 - import, forms and export
type: feature
priority: medium
created: 2026-10-23
depends: [T-0040]
---

## Goal

A pack editor page served by the engine edits a pack fully offline in the browser: import a `.talespak` or `.easytalespak`, edit pack and rooms in forms, add images and sounds, edit Lua as text, and download the result. Enough to edit the demo and clockmaker by hand. Plain JS, Alpine CSP build, own CSS, no server calls except the limits.

## Acceptance

- [ ] Its own page (`/editor`), separate from the game client, under the same CSP; no account needed to edit
- [ ] Import and export by file picker and download only; zip read and write in plain JS (store-only or `CompressionStream`), no new library unless checked first
- [ ] Forms for `pack.yaml` and every room (title, description, script, items); asset picker by drag and drop; Lua as plain text
- [ ] Reads `GET /api/pack-limits` and refuses, while editing, what the server would refuse: sizes, counts, types, names, the offline lint. A test keeps the JS rules in sync with `internal/pack`
- [ ] Assets kept once by hash with `files.meta` (T-0040 layout)
- [ ] e2e test: import the demo, change a room, export, the server loads the result
- [ ] Words a teacher understands (room, thing, picture), not engine words

## Notes

Audience is teachers and kids (Carl 2026-10-23): it must be easy, appealing and clear. Step 1 of three (story, blocks, Lua); later a room map view (T-0044). Plan doc: https://claude.ai/artifact/LKWWAHzptm1eM8KGMbXts9.
