# Resource limits

The engine limits what scripts, packs and connections may use, so one bad pack or one client cannot take a server down. The defaults suit a small server (1 vCPU, 1 GB). Change them with environment variables in `.env` ([setup-server.md](setup-server.md)); CI deploys take them from `deploy/env/<env>.env`.

An unset or empty variable keeps the default. A value that does not parse or is out of range stops the server at startup with a message naming the variable, so a typo never fails quietly.

| Variable | Default | Allowed | What it limits |
| --- | --- | --- | --- |
| `TALESPEAK_LUA_TIME` | `100ms` | `10ms` to `10s` | How long one call into a pack's Lua may run (a hook until it ends or waits for the player) |
| `TALESPEAK_LUA_CLIENT_RATE` | `250ms` | `1ms` to `10s` | Lua time one client address (IPv6: one /64) may use per second, across all its connections; every typed line and click runs a hook while the world waits |
| `TALESPEAK_LUA_CLIENT_BURST` | `2s` | `10ms` to `1m` | Lua time one client address may use at once; past it the player gets "Too much at once" until the budget refills |
| `TALESPEAK_LUA_CALL_MEMORY` | `32MiB` | `1MiB` to `1GiB` | Memory one call may allocate |
| `TALESPEAK_LUA_STATE_MEMORY` | `64MiB` | `1MiB` to `4GiB` | Memory one world's scripts may keep |
| `TALESPEAK_PACK_IMAGE_MAX` | `2MiB` | `1KiB` to `64MiB` | One image in a pack's `assets/` |
| `TALESPEAK_PACK_IMAGES_TOTAL` | `64MiB` | `1KiB` to `4GiB`, at least the image maximum | All images of one pack (they are kept in memory) |
| `TALESPEAK_MAX_CONNS` | `1000` | 1 to 1000000 | Open WebSocket connections |
| `TALESPEAK_MAX_CONNS_PER_CLIENT` | `100` | 1 to the maximum above | Open connections from one address (IPv6: one /64); when unset and `TALESPEAK_MAX_CONNS` is lower, that value |
| `TALESPEAK_CONNECT_RATE` | `1` | 0.01 to 1000000 per second | New connections per address, after the burst |
| `TALESPEAK_CONNECT_BURST` | `60` | 1 to 1000000 | New connections per address at once |
| `TALESPEAK_MESSAGE_RATE` | `10` | 0.1 to 1000 per second | Messages per connection, after the burst |
| `TALESPEAK_MESSAGE_BURST` | `20` | 1 to 10000 | Messages per connection at once |
| `TALESPEAK_IDLE_TIMEOUT` | `2m` | `45s` to `24h` | Silence before a connection is closed (clients ping every 30 s) |

Durations are Go durations (`250ms`, `2s`, `5m`). Sizes are bytes or a number with `KiB`, `MiB` or `GiB` (`512KiB`, `2MiB`).

**What to change when:**

- A school behind one NAT address with many players: raise `TALESPEAK_MAX_CONNS_PER_CLIENT` and `TALESPEAK_CONNECT_BURST`, and with a heavy pack `TALESPEAK_LUA_CLIENT_RATE` (the whole class shares it).
- Packs with large pictures: raise `TALESPEAK_PACK_IMAGE_MAX`, and keep `TALESPEAK_PACK_IMAGES_TOTAL` within the server's memory.
- Heavy rule packs that hit "script hit its limits": raise `TALESPEAK_LUA_TIME` a little (a slow hook delays the other players of its world).

**Fixed on purpose:** limits that are part of the pack format are the same on every server, so a pack that works on one works on all: at most 9 dialog options, 20 clickable items, title and option lengths, 4000 bytes per `say`, 500 bytes per typed line, 256 KiB per script, 64 KiB per YAML file. They are listed in [lua-sandbox.md](lua-sandbox.md), [content-format.md](content-format.md) and [websocket-protocol.md](websocket-protocol.md).
