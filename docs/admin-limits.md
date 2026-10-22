# Resource limits

The engine limits what scripts, packs and connections may use, so one bad pack or one client cannot take a server down. The defaults suit a small server (1 vCPU, 1 GB). Change them with environment variables in `.env` ([setup-server.md](setup-server.md)); CI deploys take them from `deploy/env/<env>.env`.

An unset or empty variable keeps the default. A value that does not parse or is out of range stops the server at startup with a message naming the variable, so a typo never fails quietly.

| Variable | Default | Allowed | What it limits |
| --- | --- | --- | --- |
| `TALESPEAK_LUA_TIME` | `100ms` | `10ms` to `10s` | How long one call into a pack's Lua may run (a hook until it ends or waits for the player) |
| `TALESPEAK_LUA_CLIENT_RATE` | `250ms` | `1ms` to `10s` | Lua time one client address (IPv6: one /48) may use per second in one world, across all its connections there; every typed line and click runs a hook while the world waits |
| `TALESPEAK_LUA_CLIENT_BURST` | `2s` | `10ms` to `1m` | Lua time one client address may use at once; past it the player gets "Too much at once" until the budget refills |
| `TALESPEAK_LUA_CALL_MEMORY` | `32MiB` | `1MiB` to `1GiB` | Memory one call may allocate |
| `TALESPEAK_LUA_PLAYER_MEMORY` | `4MiB` | `64KiB` to `1GiB` | Memory one player's script state in one room may keep; past it that player's state in that room starts over ("script hit its limits"); when unset and `TALESPEAK_LUA_STATE_MEMORY` is lower, that value |
| `TALESPEAK_LUA_CLIENT_MEMORY` | `64MiB` | `64KiB` to `4GiB`, at most the world limit | Memory the players of one address (IPv6: one /48) may keep together in one world, so a few addresses with many connections cannot fill it; past it more players from that address are told to try later; when unset and `TALESPEAK_LUA_STATE_MEMORY` is lower, that value |
| `TALESPEAK_LUA_STATE_MEMORY` | `256MiB` | `1MiB` to `4GiB`, at least the player limit | Memory all players' script state in one world may keep together; past it new players are told the world is full until others leave |
| `TALESPEAK_PACK_IMAGE_MAX` | `2MiB` | `1KiB` to `64MiB` | One image in a pack's `assets/` |
| `TALESPEAK_PACK_IMAGES_TOTAL` | `64MiB` | `1KiB` to `4GiB`, at least the image maximum | All images of one pack (they are kept in memory) |
| `TALESPEAK_MAX_CONNS` | `1000` | 1 to 1000000 | Open WebSocket connections |
| `TALESPEAK_MAX_CONNS_PER_CLIENT` | `100` | 1 to the maximum above | Open connections from one address (IPv6: one /64); when unset and `TALESPEAK_MAX_CONNS` is lower, that value |
| `TALESPEAK_CONNECT_RATE` | `1` | 0.01 to 1000000 per second | New connections per address, after the burst |
| `TALESPEAK_CONNECT_BURST` | `60` | 1 to 1000000 | New connections per address at once |
| `TALESPEAK_MESSAGE_RATE` | `10` | 0.1 to 1000 per second | Messages per connection, after the burst |
| `TALESPEAK_MESSAGE_BURST` | `20` | 1 to 10000 | Messages per connection at once |
| `TALESPEAK_IDLE_TIMEOUT` | `2m` | `45s` to `24h` | Silence before a connection is closed (clients ping every 30 s) |
| `TALESPEAK_WORLDS_PER_ACCOUNT` | `3` | 1 to 1000 | Unfinished worlds one account may have created (admins have no limit); the lobby also lets one account create 3 at once, then one every 10 minutes, and one address one a minute after `TALESPEAK_SIGNIN_BURST` |
| `TALESPEAK_MAX_WORLDS` | `1000` | 1 to 1000000 | Unfinished worlds on the server, the main world included; past it nobody creates one until a world ends |
| `TALESPEAK_LOADED_WORLDS` | `20` | 1 to 10000 | Worlds loaded at once. A world loads when its first player arrives; when the limit is reached, the world empty the longest unloads for it, and with none empty the player is told to try again in a few minutes. The main world counts against neither this nor the next limit, and stays loaded once it is |
| `TALESPEAK_WORLDS_PER_ADDRESS` | `4` | 1 to 10000 | Worlds besides the main one that the players of one address (an IPv6 /48) play at once. Each world counts the Lua time and memory budgets below on its own, so this caps what one network can use and keeps one network from holding every loaded world. A school behind one address whose classes play more worlds at once needs it higher |
| `TALESPEAK_WORLD_IDLE` | `5m` | `10s` to `24h` | How long a world stays loaded after its last player left. Unloading ends its timers, as a restart does (T-0012) |
| `TALESPEAK_SIGNIN_BURST` | `40` | 1 to 1000000 | Accounts one address may register, sign-ins it may try and invite codes it may enter at once; after that one registration every 30 s, one sign-in every 5 s and one code every 30 s. Each account name also gets 10 sign-in tries at once, then one a minute ([accounts.md](accounts.md)) |

Durations are Go durations (`250ms`, `2s`, `5m`). Sizes are bytes or a number with `KiB`, `MiB` or `GiB` (`512KiB`, `2MiB`).

**What to change when:**

- A school behind one NAT address with many players: raise `TALESPEAK_MAX_CONNS_PER_CLIENT`, `TALESPEAK_CONNECT_BURST` and, for classes larger than 40, `TALESPEAK_SIGNIN_BURST`, and with a heavy pack `TALESPEAK_LUA_CLIENT_RATE` (the whole class shares it). When the classes behind that address play more than 4 worlds at once, raise `TALESPEAK_WORLDS_PER_ADDRESS` too.
- Many worlds on a small server: every loaded world keeps its pack's compiled scripts and up to `TALESPEAK_LUA_STATE_MEMORY` of script state, so the worst case is `TALESPEAK_LOADED_WORLDS` × that. With the defaults a world with a few players keeps a few MiB; lower `TALESPEAK_LOADED_WORLDS` or `TALESPEAK_LUA_STATE_MEMORY` on a 1 GB box with heavy packs, and `TALESPEAK_WORLD_IDLE` to free memory sooner. Pack images are kept once per pack, not per world.
- Packs with large pictures: raise `TALESPEAK_PACK_IMAGE_MAX`, and keep `TALESPEAK_PACK_IMAGES_TOTAL` within the server's memory.
- Heavy rule packs that hit "script hit its limits": raise `TALESPEAK_LUA_TIME` a little (a slow hook delays the other players of its world), or `TALESPEAK_LUA_PLAYER_MEMORY` for packs with large word lists.
- "This world is full right now": every player runs their own copy of each room script they visit, so a world keeps about players × rooms visited (at most the last 4 per player) × what a script keeps. Raise `TALESPEAK_LUA_STATE_MEMORY` within the server's memory. With a 10000-word list, one player keeps about 1.2 MiB per room, so the defaults allow about 55 players per address and 220 per world.
- "Too many players from your network": a large school behind one address with a heavy pack; raise `TALESPEAK_LUA_CLIENT_MEMORY`. A player keeps the script state of the last 4 rooms they entered, so a pack with a word list in several rooms costs up to 4 times as much per player.
- The engine port reachable from a LAN without Caddy in front: set `TALESPEAK_TRUSTED_PROXIES` to the proxy's own address, or to `::1/128` when nothing proxies. By default the private ranges are trusted, so a device on the same network could send any `X-Forwarded-For` and get a fresh address for every per-client limit.
- More than one domain, or the engine port reachable without Caddy: `TALESPEAK_HOSTS` (comma-separated host names, no scheme or port) lists the names the engine answers. A request for any other `Host` gets 421, so a page on another domain cannot point its name at the server (DNS rebinding) and read it. IP addresses and `localhost` always work, for health checks. `deploy/compose.yml` sets it to `TALESPEAK_DOMAIN`; unset or empty, every name is answered.

**Fixed on purpose:** limits that are part of the pack format are the same on every server, so a pack that works on one works on all: at most 9 dialog options, 20 clickable items, title and option lengths, 4000 bytes per `say`, 500 bytes per typed line, 256 KiB per script, 64 KiB per YAML file. They are listed in [lua-sandbox.md](lua-sandbox.md), [content-format.md](content-format.md) and [websocket-protocol.md](websocket-protocol.md).
