---
id: T-0046
title: Playtest worlds for the pack editor
type: feature
priority: medium
created: 2026-10-23
depends: [T-0040]
---

## Goal

An author can try a draft pack from the editor in a private playtest world on the server, starting from any room with a chosen head start (items, state), without the pack being installed. Core glue: a kind of world plus a start config; what "items" mean stays pack Lua.

## Acceptance

- [ ] An admin setting (`TALESPEAK_*` env var) turns playtests on or off, **off by default**: draft Lua from non-admins runs on the server, so the admin decides. When off, a draft cannot be played at all (the API refuses, the editor hides Play). Documented in docs/admin-limits.md or docs/setup-server.md
- [ ] A playtest world is private to its author: never in the lobby, no join codes or invites, exactly one player, never multiplayer
- [ ] It is thrown away when the author leaves or after an idle time; nothing it does is saved to the author's characters or to any real world, and its pack is never added to the catalog
- [ ] The draft goes through the same checks and limits as an upload (T-0040), plus its own tighter limits: one playtest per account, a cap on playtests per server, the normal Lua time and memory budgets
- [ ] A debug start config, `editor/playtest.yaml` in the draft (strict YAML, size-capped), sets the start room and preset `state.player` and `state.world` values; the engine applies them before the first `on_enter`. Items are pack Lua (clockmaker bags, demo `items.lua`), so a pack-defined hook (for example `on_playtest(player, config)`) receives the config's free `vars` table to hand out items or set flags its own way
- [ ] Docs: the config format in docs/content-format.md, the hook in docs/lua-sandbox.md, the API in docs/accounts.md
- [ ] Tests (setting off refuses, never listed, second player refused, state presets, bad config), test-engineer and security-reviewer runs, protocol entry

## Notes

Carl 2026-10-23: "For testing a private editor, a private server world may be created, but its never exposed in the list or can be played multiplayer. this might also expose the server to risks from lua? make it setable. if not enabled then it just can not be played." And: starting a debug world sets a room debug state config (yaml?) that feeds the playtest with the required head start vars.

Open: who may playtest when it is on (any signed-in account, or only those who may create worlds; in edu mode admins and GMs only?).
