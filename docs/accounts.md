# Accounts, invites and what lasts

Players sign in with a local account: a name and a password. A server runs one world of its pack, and the world can be open to every account or invite only. What a pack's scripts keep in `state.player` and `state.world`, and where each player is, lasts in the database through reconnects and restarts. Sign-in with school accounts (OIDC, SAML), email and reset codes come later (T-0013).

## Settings

Set in `.env` like the resource limits ([admin-limits.md](admin-limits.md)). An unset or empty value keeps the default; a wrong value stops the server at startup.

| Variable | Values | Default | What it does |
| --- | --- | --- | --- |
| `TALESPEAK_MODE` | `public`, `edu` | `public` | Edu mode is for schools: registration defaults to `off`, and the world is always invite only |
| `TALESPEAK_REGISTRATION` | `self`, `off` | `self`; `off` in edu mode | `self`: anyone can make an account on the sign-in page. `off`: only an admin makes accounts, with the command line |
| `TALESPEAK_WORLD_ACCESS` | `open`, `invite` | `open`; always `invite` in edu mode (`open` is refused) | `open`: every signed-in account plays. `invite`: only members play, who joined with an invite code; admins always can |
| `TALESPEAK_SIGNIN_BURST` | 1 to 1000000 | `40` | Registrations, sign-ins and invite codes one address may try at once (see [admin-limits.md](admin-limits.md)) |

Registering never lets anyone into an invite-only world: a new account waits on the sign-in page for an invite code. In edu mode that holds whatever the other settings say.

## Admins and invites on the command line

The server binary manages accounts in its data directory (`TALESPEAK_DATA`, or `-data`) while the server runs. Passwords are read from standard input, never from the command line, which other users of the machine can see.

```sh
# In the Docker setup, run the commands inside the engine container:
printf '%s\n' "$PASSWORD" | docker compose exec -T talespeak /talespeak account add -admin carl
printf '%s\n' "$PASSWORD" | docker compose exec -T talespeak /talespeak account password carl   # also signs it out everywhere
docker compose exec -T talespeak /talespeak account role mira admin                               # or player
docker compose exec -T talespeak /talespeak invite -uses 30 -days 7                               # prints a code for 30 players
docker compose exec -T talespeak /talespeak invite -gm                                            # a code that makes a game master
```

In the browser, admins and the world's game masters get an **invite** button, which shows a code for one player, valid for 7 days. Only admins make game-master codes (`-gm`, or `"gm": true` in the API). A code is shown once: the server keeps only its hash. Codes are 10 characters like `7KQ2M-XR4WD`; case, spaces and dashes do not matter, and O, I and L are read as 0, 1 and 1.

## Names and passwords

- Names are 3 to 20 letters, digits, `-` and `_`, starting with a letter. They are unique in any letter case and are what other players see (`player.name` in Lua).
- Passwords are 8 to 128 bytes. They are stored as argon2id hashes (19 MiB, 2 passes), and at most two are hashed at once, so a burst of sign-ins cannot take the server's memory.
- A sign-in lasts 30 days in a cookie: HttpOnly and SameSite=Strict; over HTTPS (directly or through a trusted proxy's `X-Forwarded-Proto`) it is `__Host-talespeak_session` and Secure, so no other subdomain can set it; over plain HTTP (local play) `talespeak_session`. The server keeps only a hash of its token, and at most 10 sign-ins per account; an eleventh signs out the oldest.

## Rate limits

| What | Per | At once | Then |
| --- | --- | --- | --- |
| Registering | address | `TALESPEAK_SIGNIN_BURST` | one every 30 s |
| Signing in | address | `TALESPEAK_SIGNIN_BURST` | one every 5 s |
| Wrong passwords | account name and address | 10 | one a minute |
| Wrong passwords | account name | 100 | 100 an hour |
| Invite codes | address | `TALESPEAK_SIGNIN_BURST` | one every 30 s |
| Invite codes | account | 10 | one a minute |

A refused try gets HTTP 429 ("Too many tries"). Only wrong passwords count toward the per-name limits, and the strict one counts per name and address, so one guesser cannot lock an account out (players behind the same address still share it). A looser limit of 100 wrong passwords an hour per name covers guessing from many addresses; someone with many addresses can use it up and block sign-ins to that account, a correct password included, until it refills (about 36 seconds per try). That is the price of a real limit on guessing: letting correct passwords through would let a guesser go on until one is. Addresses are counted as for the other limits: IPv6 by its /64 ([websocket-protocol.md](websocket-protocol.md)). At most two passwords are hashed at once and eight more wait; past that a sign-in gets 503 at once.

The address is only as good as `TALESPEAK_TRUSTED_PROXIES` ([admin-limits.md](admin-limits.md)): when the engine port is reachable without Caddy in front, a device in a trusted range can send any `X-Forwarded-For` and get fresh limits for every request, so narrow it to the proxy's own address.

## What lasts

A world is one run of a pack, kept in the database with an id that is never used again. Each account gets one character in it, whose id is `player.id` in Lua (`"p" ..` its number): it never repeats, also after a restart, so a new player cannot inherit an old one's items or turns.

- `state.player` and `state.world` are read when the player joins and when the world starts, and written after every hook call: once, in one transaction, with every key the call changed. The write counts toward the player's Lua time. A write that fails is logged; the world plays on and the next save tries the keys again.
- The character's room is saved on every room change. A player who comes back continues there; when the pack no longer has that room, in the start room. Script globals and the instances of rooms start over, as they always do for a new session.
- Connecting again from another tab or device ends the older session as if the player left (`on_leave` runs), and the older page shows "You are playing in another window" with a **play here** button.
- Timers, room timers and questions waiting for an answer do not last yet (T-0012).

The data lives in `talespeak.db` in the data directory (Docker volume `talespeak_data`). To back it up, stop the server and copy the file together with `talespeak.db-wal` and `talespeak.db-shm` if they are there.

## API

The web client uses a small JSON API on the same origin. POSTs must send `Content-Type: application/json` and, from a browser, come from the same site (`Origin`, `Sec-Fetch-Site`); others get 403 or 415. Bodies are at most 4 KiB.

| Request | Body | Answer |
| --- | --- | --- |
| `GET /api/me` | | `{"account": {"name", "role"} or null, "registration": "self" or "off", "world": {"pack", "access", "role", "play", "invite"}}` |
| `POST /api/register` | `{"name", "password"}` | The same as `/api/me`, signed in; 409 when the name is taken, 403 when registration is off |
| `POST /api/login` | `{"name", "password"}` | The same, signed in; 401 for a wrong name or password (the same answer and about the same time for both) |
| `POST /api/logout` | `{}` | The same, signed out |
| `POST /api/join` | `{"code"}` | The same, now a member; 400 for a code that is unknown, used up or expired |
| `POST /api/invites` | `{"uses": 1..100, "days": 1..30, "gm": false}` (all optional) | `{"code", "uses", "expires", "gm"}`; 403 unless an admin or the world's game master |

Errors are `{"error": "a sentence the player can read"}`. The WebSocket at `/ws` needs the sign-in cookie: without one the upgrade gets 401, and an account that may not play the world gets 403.
