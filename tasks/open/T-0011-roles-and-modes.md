---
id: T-0011
title: Roles, permissions and edu/public server modes
type: feature
priority: high
created: 2026-10-17
depends: [T-0010]
---

## Goal

Accounts have a global role (admin, GM, player; new accounts are players), each world has its GMs and players, and the server runs in edu mode (invite-only kids, admin-created GMs) or public mode (anyone can create a world and becomes its GM).

## Acceptance

- [ ] Global roles and per-world membership roles in the store
- [ ] Edu mode: kids and teachers only ever join by invite or admin action, whatever the registration setting (T-0013)
- [ ] Public mode: create world -> GM; join as GM or player; GM mode toggle in-game
- [ ] Audit log of every GM action; visible marker when a GM acts
- [ ] Core permissions with role defaults; `perm.define()` and `actor:can()` in Lua
- [ ] Edu safety floor that packs cannot lower (GM sees all chat, no private chat outside the world, kids never get GM or admin), covered by tests
- [ ] Players see a notice that GMs can read chat
- [ ] GM commands reach the room script as a hook, so a GM can skip a turn, change the order or switch a room to free mode through the Lua turn library (`round.next`, `round.turns`, `round.free` in the demo's `lib/round.lua`, T-0019); every change in the audit log
- [ ] GM and system messages get a look that a pack's `from` cannot copy (from the T-0027 security review)

## Notes

Design: "Roles and server modes" in the Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
