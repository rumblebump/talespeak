---
id: T-0044
title: Pack editor, part 3 - own blocks, templates, kits and the map
type: feature
priority: low
created: 2026-10-23
depends: [T-0043]
---

## Goal

Authors make their own blocks and templates and share them as kit packs, and see the pack as a map of rooms.

## Acceptance

- [ ] "Make block" turns a stack of blocks into an annotated Lua function in the pack's `lib/`, usable without the editor
- [ ] Templates (a block with holes and defaults, such as a locked door with a code) stored in `editor/blocks/`
- [ ] Kit packs (only `lib/` and templates) import into other packs; demo `round.lua` and `items.lua` annotated as the first kit
- [ ] New-world templates for teachers (escape room, treasure hunt, quiz walk)
- [ ] A room map: rooms as nodes, exits as lines, hand-written SVG with Alpine
- [ ] Guided lessons: the demo escape rebuilt step by step, one idea per step

## Notes

Open: author and license fields for kit packs, and a place to share them.
