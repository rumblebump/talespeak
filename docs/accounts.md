# Accounts, worlds, join codes and what lasts

Players sign in with a local account: a name and a password. After signing in they see the lobby: a card for every world they may play, a box for a join code, and, when they may, a form to create a world from one of the server's packs. What a pack's scripts keep in `state.player` and `state.world`, and where each player is, lasts in the database through reconnects and restarts. Sign-in with school accounts (OIDC, SAML), email and reset codes come later (T-0013).

## Settings

Set in `.env` like the resource limits ([admin-limits.md](admin-limits.md)). An unset or empty value keeps the default; a wrong value stops the server at startup.

| Variable | Values | Default | What it does |
| --- | --- | --- | --- |
| `TALESPEAK_MODE` | `public`, `edu` | `public` | Edu mode is for schools: registration defaults to `off`, every world is private, and only admins and game masters create worlds |
| `TALESPEAK_REGISTRATION` | `self`, `off` | `self`; `off` in edu mode | `self`: anyone can make an account on the sign-in page. `off`: only an admin makes accounts, with the command line |
| `TALESPEAK_WORLD_ACCESS` | `open`, `invite` | `open`; always `invite` in edu mode (`open` is refused) | The server's main world: `open` makes it public, `invite` private (only members play, who joined with a code; admins always can) |
| `TALESPEAK_CREATE_WORLDS` | `everyone`, `gm` | `everyone`; `gm` when `TALESPEAK_WORLD_ACCESS=invite`, so a server kept closed stays closed; always `gm` in edu mode (`everyone` is refused) | Who may create worlds in the lobby: every account, or only admins and the game masters of a world |
| `TALESPEAK_PACK` | a folder | the built-in demo | The pack of the server's main world, which runs its newest installed version |
| `TALESPEAK_PACKS` | a folder | none | Every folder in it with a `pack.yaml` is a pack worlds can be created from, next to the main world's pack and the demo. Several versions of one pack can sit side by side, one folder each ([pack-versions.md](pack-versions.md)); two folders with the same id and version stop the server, and one with the demo's id and version replaces the built-in demo |
| `TALESPEAK_SIGNIN_BURST` | 1 to 1000000 | `40` | Registrations, sign-ins and invite codes one address may try at once (see [admin-limits.md](admin-limits.md)) |

Registering never lets anyone into a private world: a new account sees only the public worlds, and in edu mode none, until it types a join code. In edu mode that holds whatever the other settings say.

The limits on worlds (how many per account and per server, how many loaded at once, how long an empty world stays loaded) are in [admin-limits.md](admin-limits.md).

## Worlds and join codes

A world is one run of a pack, with its own players and state. The server's main world runs the pack in `TALESPEAK_PACK` (the demo without it) and always exists; players create more in the lobby. Every world is

- **public**: listed in every lobby; every signed-in account plays it, or
- **private**: listed only to its members and admins; an account becomes a member with one of its join codes.

Whoever creates a world picks its pack, an optional name (one line, at most 40 characters) and public or private, becomes its game master and gets a join code for up to 30 players, valid for 7 days, shown once on the world's card. The game master makes more codes with the card's **join code** button (for 30 players) or the **invite** button while playing (for one player), and ends the world with **end world**: its players are sent back to the lobby, and its state and its codes are deleted. The main world cannot be ended from the lobby; the main world of a pack the server played before can, by an admin. Game masters make at most 20 codes at once and one a minute after that, and a world keeps at most 100 codes that still work. In edu mode every world is private and listed only to its members and admins, also one that was public before edu mode was switched on.

World names are free text: the server removes control, direction and blank-looking characters and stacked accents, but filters no words yet. In public mode with self-registration every signed-in account sees the names of public worlds; the chat filter (T-0006) will cover them. Each card shows the world's name, its pack and the pack's description, public or private, who created it, how many players it has and how many are online now, since when it runs, and the account's role in it. A world whose pack the server no longer has stays listed but cannot be played. Worlds load when their first player arrives and unload when nobody has played them for `TALESPEAK_WORLD_IDLE` (the main world stays loaded); the players of one address play at most `TALESPEAK_WORLDS_PER_ADDRESS` other worlds at once ([admin-limits.md](admin-limits.md)); unloading ends their timers, which do not last yet (T-0012).

The design doc's third kind, invite only without a code (players added by their game master), and player limits per world come with T-0016.

## Admins and invites on the command line

The server binary manages accounts in its data directory (`TALESPEAK_DATA`, or `-data`) while the server runs. Passwords are read from standard input, never from the command line, which other users of the machine can see.

```sh
# In the Docker setup, run the commands inside the engine container:
printf '%s\n' "$PASSWORD" | docker compose exec -T talespeak /talespeak account add -admin carl
printf '%s\n' "$PASSWORD" | docker compose exec -T talespeak /talespeak account password carl   # also signs it out everywhere
docker compose exec -T talespeak /talespeak account role mira admin                               # or player
docker compose exec -T talespeak /talespeak invite -uses 30 -days 7                               # prints a code to the main world for 30 players
docker compose exec -T talespeak /talespeak invite -gm                                            # a code that makes a game master of the main world
docker compose exec -T talespeak /talespeak invite -world 12 -gm                                  # the same for world 12 (the id is in the lobby's address, #world=12)
```

In the browser, admins and the world's game masters make join codes as described above. Only admins make game-master codes (`-gm`, or `"gm": true` in the API). A code is shown once: the server keeps only its hash. Codes are 10 characters like `7KQ2M-XR4WD`; case, spaces and dashes do not matter, and O, I and L are read as 0, 1 and 1. A code names its world, so the lobby's one box takes the codes of every world.

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
| Join codes | address | `TALESPEAK_SIGNIN_BURST` | one every 30 s |
| Join codes | account | 10 | one a minute |
| New worlds | address | `TALESPEAK_SIGNIN_BURST` | one a minute |
| New worlds | account | 3 | one every 10 minutes |

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
| `GET /api/me` | | `{"account": {"name", "role"} or null, "registration": "self" or "off", "mode": "public" or "edu", "create": true if the account may create worlds}` |
| `POST /api/register` | `{"name", "password"}` | The same as `/api/me`, signed in; 409 when the name is taken, 403 when registration is off |
| `POST /api/login` | `{"name", "password"}` | The same, signed in; 401 for a wrong name or password (the same answer and about the same time for both) |
| `POST /api/logout` | `{}` | The same, signed out |
| `GET /api/worlds` | | The lobby: `{"worlds": [card, ...], "packs": [{"id", "title", "description"}, ...]}`, the account's own worlds first, then the others, the newest first in each, at most 200; `packs` is empty when the account may not create worlds |
| `POST /api/worlds` | `{"pack", "title", "visibility": "public" or "private"}` (`title` optional, `visibility` private when left out) | `{"world": card, "invite": {"code", "uses", "expires"}}`; 403 when the account may not create worlds, 409 past its limit, 503 past the server's |
| `POST /api/join` | `{"code"}` | The same as `/api/me` with `"joined": world id`, now a member; 400 for a code that is unknown, used up, expired or of an ended world |
| `POST /api/invites` | `{"world", "uses": 1..100, "days": 1..30, "gm": false}` (all but `world` optional) | `{"code", "uses", "expires", "gm"}`; 403 unless an admin or the world's game master, 404 for a world that does not exist, has ended or that the account may not see (a private world it is not a member of); 429 past 20 codes at once and one a minute, 409 when the world has 100 codes that still work |
| `POST /api/finish` | `{"world"}` | Ends the world and answers the lobby like `GET /api/worlds`; 403 unless an admin or its game master, and for the main world; 404 like `/api/invites` |

A card is `{"id", "title", "pack", "packTitle", "description", "visibility", "gm", "players", "online", "role", "main", "created", "play", "invite", "finish"}`: `gm` is who created it (none for the main world), `players` how many characters it has, `online` how many play now, `role` the account's role in it (`gm`, `player` or none), `main` true for the main world, and `play`, `invite` and `finish` whether the account may play it, make its codes and end it.

Errors are `{"error": "a sentence the player can read"}`. The WebSocket at `/ws?world=ID` needs the sign-in cookie: without one the upgrade gets 401, a world that does not exist, has ended or that the account may not see 404, and an account that may see but not play the world (its pack is not installed) 403.
