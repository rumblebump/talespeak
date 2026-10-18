---
id: T-0034
title: Local accounts, invites, and player and world state that last
type: feature
priority: high
created: 2026-10-18
depends: [T-0004, T-0030, T-0031]
---

## Goal

The first slice of T-0010, T-0013 and T-0018: players sign in with a local account, a world can be invite only, and what scripts keep in `state.player` and `state.world`, and where each player is, survives a reconnect and a server restart.

## Acceptance

- [x] Local accounts: name and password, argon2id hashes, names unique in any letter case; sign-in sessions as a random token in an HttpOnly, SameSite=Strict cookie (Secure over HTTPS), only its hash stored, 30 days, at most 10 per account
- [x] JSON API under `/api/` for the web client: who am I, register, sign in, sign out, join with an invite code, make an invite; same-origin JSON POSTs only
- [x] Settings: `TALESPEAK_MODE` (public, edu), `TALESPEAK_REGISTRATION` (self, off), `TALESPEAK_WORLD_ACCESS` (open, invite); edu mode defaults to registration off and is always invite only, so registering never lets anyone into an edu world
- [x] Invites: random codes, only their hash stored, with a number of uses and an expiry; admins and the world's game masters make player invites, only admins make game-master invites; `talespeak invite` on the command line
- [x] Admins on the command line: `talespeak account add [-admin] NAME`, `account password NAME`, `account role NAME admin|player`; passwords from standard input only
- [x] Rate limits on registering, signing in (per address and per name) and invite codes (per address and per account); at most two password hashes at once
- [x] The WebSocket needs a sign-in, and refuses accounts that may not play the world
- [x] One character per account and world; `player.id` is the character id, which never repeats, also after a restart
- [x] `state.player` and `state.world` are read from SQLite when the player joins and the world starts, and written after every hook call, once, in one transaction; the write counts toward the player's Lua time; a failed write is logged and tried again with the next save; loading checks the same limits as writing
- [x] The character's room is saved on every room change; a player who comes back continues there (the start room when it left the pack)
- [x] Connecting again (another tab or device) ends the older session as if the player left, and closes it with a code the client does not reconnect after
- [x] Web client: sign in or make an account, wait for an invite and type its code, sign out, make an invite, "play here" after playing elsewhere
- [x] Tests: Go (store, accounts API, restart keeps state, takeover, edu and invite rules, limits), Playwright (form, reload continues, second window, invite-only world)

## Notes

Left for later tasks (each listed in its own task file): pack versions, profiles and rulesets (T-0010); timers and room timers stored (T-0012); roles beyond admin and GM, GM tools and the audit log (T-0011); registration settings like class codes, reset codes and email (T-0013); death modes, save points, inventories moving between worlds, finishing a world from the game, configurable state limits and the edu chat filter for shared state (T-0018).

Core vs Lua (Carl 2026-10-18): the engine stores and loads; what the stored values mean is Lua.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6

Done 2026-10-18, protocol: `protocol/2026-10-18-t-0034-accounts-lasting-state.md`. First numbered T-0032 on its branch; renumbered after PR #30 took that number.
