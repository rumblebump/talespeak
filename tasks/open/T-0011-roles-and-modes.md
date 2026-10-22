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
- [ ] Changing or removing an account's role in a world ends its live sessions there, so `player.gm` and the `gm` of `room.players`/`world.players` (read at connect, T-0036) never stay true for a demoted GM (from the T-0036 security review)

## Notes

T-0034 (2026-10-18) added the global roles admin and player, the world roles gm and player (`world_members`), `TALESPEAK_MODE` (public, edu; edu worlds are always invite only), and invite codes: admins and game masters invite players, only admins make game masters. GM tools, the audit log, permissions in Lua and creating worlds in public mode are still open here.

T-0035 (2026-10-22): creating worlds in the lobby (`TALESPEAK_CREATE_WORLDS`: everyone in public mode, `gm` when `TALESPEAK_WORLD_ACCESS=invite` and always in edu); the creator becomes the world's GM, makes join codes and ends the world. Admins see and end every world from the lobby, but have no paging or search there yet past 200 worlds.

Design: "Roles and server modes" in the Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
