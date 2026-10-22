---
id: T-0035
title: World lobby, creating worlds and join codes
type: feature
priority: high
created: 2026-10-22
depends: [T-0034]
---

## Goal

After signing in, a player sees a card for every world they may play (public ones and the ones they are a member of), can create a world by picking a pack, and gets a join code that others type to join it. One server runs many worlds, loaded when a player arrives and unloaded when idle.

## Acceptance

- [ ] Several worlds in one process: loaded on the first player, unloaded when idle, a cap on loaded worlds; the server's main world (the pack in `TALESPEAK_PACK` or the demo) works as before
- [ ] Pack catalog: the demo, `TALESPEAK_PACK` and every pack folder in `TALESPEAK_PACKS`; pack.yaml gets an optional `description`
- [ ] Worlds have a title, a visibility (public: listed, anyone plays; private: members only, join with a code), a creator and a start time in SQLite
- [ ] `GET /api/worlds`: cards with pack, title, GM, players, online now, visibility, the account's role and what it may do; `POST /api/worlds` creates a world, makes the creator its GM and returns a join code; `POST /api/finish` ends a world (its GM or an admin)
- [ ] Join codes are the invite codes of T-0034, now naming their world: `POST /api/join` joins the code's world; `POST /api/invites` and `talespeak invite` take a world
- [ ] Who may create: everyone in public mode (`TALESPEAK_CREATE_WORLDS=everyone`), only admins and game masters with `gm` and always in edu mode; edu worlds are always private and listed only to members
- [ ] Limits: worlds per account, worlds per server, loaded worlds, and a rate limit on creating
- [ ] The WebSocket names its world (`/ws?world=ID`) and is refused for worlds the account may not play
- [ ] Web client: lobby with world cards, new-world form, join-code box, a way back to the lobby; a reload stays in the world
- [ ] Docs: accounts.md, admin-limits.md, websocket-protocol.md, content-format.md
- [ ] Tests: Go (store, API, hub loading and unloading, edu rules, limits), Playwright (lobby, create, join with a code, invite-only server)

## Notes

Carl 2026-10-22: after sign-in, list all public and invited worlds, as a card per world with its metadata; creating a world by choosing a pack gives a code others use to join.

The design doc ("Roles and server modes") has three visibilities: public, invite only and secret. Invite only and secret both work through codes here, so they are one "private" for now; player limits, replacing or expiring a world's codes and the listing search stay in T-0016.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
