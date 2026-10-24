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

- [x] Global roles and per-world membership roles in the store (T-0034)
- [ ] Edu mode: kids and teachers only ever join by invite or admin action, whatever the registration setting (T-0013)
- [x] Public mode: create world -> GM; join as GM or player; GM mode toggle in-game
- [x] Audit log of every GM action; visible marker when a GM acts
- [x] Core permissions with role defaults; `perm.define()` and `player:can()` in Lua
- [x] Edu safety floor that packs cannot lower (kids never get GM or admin), covered by tests; the chat parts (GM sees all chat, no private chat outside the world, the notice that GMs can read chat) moved to T-0006, where chat comes
- [x] Lobby UI for a world's members (`GET`/`POST /api/members`)
- [x] GM commands reach the room script as a hook, so a GM can skip a turn, change the order or switch a room to free mode through the Lua turn library (`round.next`, `round.turns`, `round.free` in the demo's `lib/round.lua`, T-0019); every change in the audit log
- [x] GM and system messages get a look that a pack's `from` cannot copy (from the T-0027 security review)
- [x] Changing or removing an account's role in a world ends its live sessions there, so `player.gm` and the `gm` of `room.players`/`world.players` (read at connect, T-0036) never stay true for a demoted GM (from the T-0036 security review)
- [x] Slash commands: `/help` for everyone; for GMs `/players`, `/rooms`, `/go`, `/say`, `/reset room`, `/audit`; room commands via `commands` and `on_command`
- [x] Admins page through more than 200 worlds in the lobby
- [x] Web client (Carl 2026-10-23): chat-like layout with the input at the bottom and the story following its end, the last story lines kept in the browser (only for the tab's life in edu mode, gone on sign-out), no typing hints, slash commands

## Notes

T-0034 (2026-10-18) added the global roles admin and player, the world roles gm and player (`world_members`), `TALESPEAK_MODE` (public, edu; edu worlds are always invite only), and invite codes: admins and game masters invite players, only admins make game masters. GM tools, the audit log, permissions in Lua and creating worlds in public mode are still open here.

T-0035 (2026-10-22): creating worlds in the lobby (`TALESPEAK_CREATE_WORLDS`: everyone in public mode, `gm` when `TALESPEAK_WORLD_ACCESS=invite` and always in edu); the creator becomes the world's GM, makes join codes and ends the world. Admins see and end every world from the lobby, but have no paging or search there yet past 200 worlds.

Design: "Roles and server modes" in the Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6

2026-10-23 (first part, PR into dev): slash commands and the GM tools above (docs/gm-tools.md), the `notice` message for the engine and GMs, the audit log (`audit_log`, kept past the world's end, 1000 entries per world, an action it cannot record does not happen), `POST /api/members` for admins, sessions that end and reconnect when their role changes, lobby paging, the demo's GM turn commands (`/skip`, `/turns`, `/free` in `lib/round.lua`, demo 1.1.0) and `on_reset` in its rooms, and Carl's client changes. Still open here: `perm.define()` and `actor:can()`, the edu safety floor with its tests, the notice that GMs can read chat (with chat, T-0006), a GM mode toggle in-game, joining as GM in public mode, and a lobby UI for `POST /api/members`.

2026-10-24 (second part, Carl's design calls 1 to 3): GM mode like sudo (`session.member` is the role, `session.gm` the mode; **play as GM** / **play as player** on the card, `as=player` on the WebSocket, `/gm [on|off]`, audited and marked to the room); `perm.define` and `player:can` (`internal/script/perm.go`; the engine's permissions only in GM mode, packs cannot define them); `GET /api/members` and the lobby's members list (`web/static/members.js`); world GMs take players out and, outside edu mode, make players GMs (also by GM join codes); only admins change a GM or add by name; the edu floor tests (`internal/account/edu_floor_test.go`). The chat parts of the floor moved to T-0006. Left here: the edu registration item, which T-0013 covers.
