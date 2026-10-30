---
id: T-0043
title: Pack editor, part 2 - blocks that write Lua
type: feature
priority: medium
created: 2026-10-23
depends: [T-0042, T-0046]
---

## Goal

Authors (teachers, kids from about 8) script rooms with visual blocks that compile to plain Lua, and see that Lua next to the blocks, so the editor also teaches the basics of programming.

## Acceptance

- [ ] Blockly (Apache-2.0, has a Lua generator) vendored and pinned in `web/vendor.json`, run without its injected styles so the CSP stays strict; license and size checked first
- [ ] Block palette from annotated Lua (`---@block`, `---@param` with types string, number, room, item, asset, player): a kit `lib/kit.lua` with the safe patterns of docs/pack-patterns.md (say, ask, go, remember for the player, everyone in the world knows, timers, a world secret), plus every annotated function in the pack's `lib/`
- [ ] One event block per hook (`on_enter`, `on_click`, `on_input`, `on_timer`, ...); rooms, things and assets chosen from dropdowns, never typed
- [ ] Block layouts saved in `editor/workspace.json` (`.easytalespak`); each generated script has a header with a hash of its code, and a hand-edited script shows as a Lua code block instead of being overwritten
- [ ] The generated Lua is shown live next to the blocks; a hook can switch to code
- [ ] A Play button that runs the draft in a playtest world on the server (T-0046; worlds never run in the browser), with a form for the playtest start (room, items, state) saved as `editor/playtest.yaml`. When the server has playtests turned off, the button is hidden and the editor says why
- [ ] Tests for the generator output against the sandbox, e2e, test-engineer and security-reviewer runs

## Notes

Play (Carl 2026-10-23): a private playtest world on the server, never listed and never multiplayer, behind an admin setting; off means no Play. The server side is T-0046.
