# Accounts, worlds, join codes and what lasts

Players sign in with a local account: a name and a password. After signing in they see the lobby: a card for every world they may play, a box for a join code, and, when they may, a form to create a world from one of the server's packs. What a pack's scripts keep in `state.player` and `state.world`, and where each player is, lasts in the database through reconnects and restarts. Forgotten passwords are reset with one-time codes that admins and teachers make, or, when the server sends mail, with a link by mail ([admin-email.md](admin-email.md)). Sign-in with school accounts (OIDC, SAML) comes later; the code has a `Provider` interface for it (`internal/account/provider.go`).

## Settings

Set in `.env` like the resource limits ([admin-limits.md](admin-limits.md)). An unset or empty value keeps the default; a wrong value stops the server at startup.

| Variable | Values | Default | What it does |
| --- | --- | --- | --- |
| `TALESPEAK_MODE` | `public`, `edu` | `public` | Edu mode is for schools: registration defaults to `off`, every world is private, and only admins and game masters create worlds |
| `TALESPEAK_REGISTRATION` | `self`, `off`, `sso` | `self`; `off` in edu mode | `self`: anyone can make an account on the sign-in page. `off`: only an admin makes accounts, with the command line. `sso`: accounts come from the school's or company's single sign-on (OIDC or SAML, a later task); until that exists it works like `off` |
| `TALESPEAK_CLASS_CODE` | 6 to 64 letters and digits (spaces and `-` allowed) | none | With `TALESPEAK_REGISTRATION=self`: registering also needs this code, so only those a teacher or admin told it can make accounts (case, spaces and dashes do not matter). It lets nobody into a world. A secret like a password: keep it in `.env` (`$CLASS_CODE` in docs) |
| `TALESPEAK_EMAIL_REGISTRATION`, `TALESPEAK_PUBLIC_URL`, `TALESPEAK_SMTP_*` | | off | Mail for reset links and email addresses ([admin-email.md](admin-email.md)) |
| `TALESPEAK_SIGNIN_RECHECK` | 10s to 1h | `1m` | How often an open game checks its sign-in again ([admin-limits.md](admin-limits.md)) |
| `TALESPEAK_WORLD_ACCESS` | `open`, `invite` | `open`; always `invite` in edu mode (`open` is refused) | The server's main world: `open` makes it public, `invite` private (only members play, who joined with a code; admins always can) |
| `TALESPEAK_CREATE_WORLDS` | `everyone`, `gm` | `everyone`; `gm` when `TALESPEAK_WORLD_ACCESS=invite`, so a server kept closed stays closed; always `gm` in edu mode (`everyone` is refused) | Who may create worlds in the lobby: every account, or only admins and the game masters of a world |
| `TALESPEAK_PACK` | a folder | the built-in demo | The pack of the server's main world, which runs its newest installed version |
| `TALESPEAK_PACKS` | a folder | none | Every folder in it with a `pack.yaml` is a pack worlds can be created from, next to the main world's pack and the demo. Several versions of one pack can sit side by side, one folder each ([pack-versions.md](pack-versions.md)); two folders with the same id and version stop the server, and one with the demo's id and version replaces the built-in demo |
| `TALESPEAK_SIGNIN_BURST` | 1 to 1000000 | `40` | Registrations, sign-ins and invite codes one address may try at once (see [admin-limits.md](admin-limits.md)) |

Registering never lets anyone into a private world: a new account sees only the public worlds, and in edu mode none, until it types a join code. In edu mode that holds whatever the other settings say, with a class code too: a kid waits for a game master's join code, and a teacher who registers is a plain account until an admin makes them a game master of a world (a game master code, or `POST /api/members`); only then may they create worlds, make reset codes and add an email address.

The limits on worlds (how many per account and per server, how many loaded at once, how long an empty world stays loaded) are in [admin-limits.md](admin-limits.md).

## Worlds and join codes

A world is one run of a pack, with its own players and state. The server's main world runs the pack in `TALESPEAK_PACK` (the demo without it) and always exists; players create more in the lobby. Every world is

- **public**: listed in every lobby; every signed-in account plays it, or
- **private**: listed only to its members and admins; an account becomes a member with one of its join codes.

Whoever creates a world picks its pack, an optional name (one line, at most 40 characters) and public or private, becomes its game master and gets a join code for up to 30 players, valid for 7 days, shown once on the world's card. The game master makes more codes with the card's **join code** button (for 30 players) or the **invite** button while playing (for one player), and ends the world with **end world**: its players are sent back to the lobby, and its state and its codes are deleted. The main world cannot be ended from the lobby; the main world of a pack the server played before can, by an admin. Game masters make at most 20 codes at once and one a minute after that, and a world keeps at most 100 codes that still work. In edu mode every world is private and listed only to its members and admins, also one that was public before edu mode was switched on.

World names are free text: the server removes control, direction and blank-looking characters and stacked accents, but filters no words yet. In public mode with self-registration every signed-in account sees the names of public worlds; the chat filter (T-0006) will cover them. Each card shows the world's name, its pack and the pack's description, public or private, who created it, how many players it has and how many are online now, since when it runs, and the account's role in it. A world whose pack the server no longer has stays listed but cannot be played. Worlds load when their first player arrives and unload when nobody has played them for `TALESPEAK_WORLD_IDLE` (the main world stays loaded); the players of one address play at most `TALESPEAK_WORLDS_PER_ADDRESS` other worlds at once ([admin-limits.md](admin-limits.md)); unloading pauses nothing: timers keep their deadlines in the database and come back when the world loads again (T-0012).

The design doc's third kind, invite only without a code (players added by their game master), and player limits per world come with T-0016.

## Admins and invites on the command line

The server binary manages accounts in its data directory (`TALESPEAK_DATA`, or `-data`) while the server runs. Passwords are read from standard input, never from the command line, which other users of the machine can see.

```sh
# In the Docker setup, run the commands inside the engine container:
printf '%s\n' "$PASSWORD" | docker compose exec -T talespeak /talespeak account add -admin carl
printf '%s\n' "$PASSWORD" | docker compose exec -T talespeak /talespeak account password carl   # also signs it out everywhere
docker compose exec -T talespeak /talespeak account resetcode mira                                # prints a one-time reset code for mira
docker compose exec -T talespeak /talespeak account role mira admin                               # or player
docker compose exec -T talespeak /talespeak invite -uses 30 -days 7                               # prints a code to the main world for 30 players
docker compose exec -T talespeak /talespeak invite -gm                                            # a code that makes a game master of the main world
docker compose exec -T talespeak /talespeak invite -world 12 -gm                                  # the same for world 12 (the id is in the lobby's address, #world=12)
docker compose exec -T talespeak /talespeak profile list ann                                     # ann's characters on rulesets, with their ids
docker compose exec -T talespeak /talespeak profile undo 12                                      # puts profile 12 back as it was before its last ruleset migration ([rulesets.md](rulesets.md))
```

In the browser, admins and the world's game masters make join codes as described above. Only admins make game-master codes (`-gm`, or `"gm": true` in the API), and outside edu mode the world's game masters too. A code is shown once: the server keeps only its hash. Codes are 10 characters like `7KQ2M-XR4WD`; case, spaces and dashes do not matter, and O, I and L are read as 0, 1 and 1. A code names its world, so the lobby's one box takes the codes of every world.

## Passwords, reset codes and email

- **Reset codes** work without mail. Admins make them for any account, game masters in edu mode for the plain players of their own worlds (never for game masters or admins), in the lobby under **your account**, and admins also with `talespeak account resetcode NAME`. A code is 10 characters like an invite code, works once and for 24 hours, and a new one replaces the account's last. The player chooses **I have a reset code** on the sign-in page. Outside edu mode game masters make none: a public server's game master knows their players only by name.
- **Reset links** come by mail when the server sends mail and the account has a confirmed address: **forgot password?** on the sign-in page. A link (`https://.../#reset=CODE`, 20 characters) works once and for one hour; its code can also be typed as a reset code. The answer never tells whether the account exists or has an address.
- **Changing the password** in the lobby needs the current one and counts toward the sign-in limits for wrong passwords.
- **Email addresses** are optional and only for resets and signing in: given at registration (public mode) or under **your account**, kept on the account only once the mailed link (`#verify=CODE`, one day) is opened, unique across accounts. In edu mode only admins and game masters have one. Addresses are never logged.
- A reset, a password change (in the lobby or with `account password`) signs the account out everywhere; the lobby keeps this browser signed in.

## Sign-ins and open games

A game's WebSocket checks the sign-in when it connects. After that:

- signing out ends the games of that sign-in (other devices of the account play on), and a reset or a password change in the lobby ends all of the account's games at once: the account service tells the WebSocket server (`ws.Server.SignOut`), which closes them with code 4002;
- every `TALESPEAK_SIGNIN_RECHECK` (1 minute) each game checks its sign-in again, for what the server does not hear of: a password changed with the command line, an expired sign-in, an account taken out of a world. A game whose sign-in no longer lets it play the world closes with 4002; a busy database is not taken as an answer.

The page then asks the server what is left: the sign-in page, the lobby, or the same world with a new role.

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
| Join codes, reset codes typed, confirmation links | address | `TALESPEAK_SIGNIN_BURST` | one every 30 s |
| Join codes | account | 10 | one a minute |
| New worlds | address | `TALESPEAK_SIGNIN_BURST` | one a minute |
| New worlds | account | 3 | one every 10 minutes |
| Join codes and reset codes made | account | 20 | one a minute |
| Forgot password | address (IPv6 /48) | 10 | one a minute |
| Mails | account, and recipient address | 3 | one every 20 minutes (forgot password: the same answer, no mail) |
| Wrong current passwords (changing it) | account name and address, and name | 10, 100 | shared with wrong sign-in passwords |
| Picking characters | account | 20 | one every 3 s |

A refused try gets HTTP 429 ("Too many tries"). Only wrong passwords count toward the per-name limits, and the strict one counts per name and address, so one guesser cannot lock an account out (players behind the same address still share it). A looser limit of 100 wrong passwords an hour per name covers guessing from many addresses; someone with many addresses can use it up and block sign-ins to that account, a correct password included, until it refills (about 36 seconds per try). That is the price of a real limit on guessing: letting correct passwords through would let a guesser go on until one is. Addresses are counted as for the other limits: IPv6 by its /64 ([websocket-protocol.md](websocket-protocol.md)). At most two passwords are hashed at once and eight more wait; past that a sign-in gets 503 at once.

The address is only as good as `TALESPEAK_TRUSTED_PROXIES` ([admin-limits.md](admin-limits.md)): when the engine port is reachable without Caddy in front, a device in a trusted range can send any `X-Forwarded-For` and get fresh limits for every request, so narrow it to the proxy's own address.

## What lasts

A world is one run of a pack, kept in the database with an id that is never used again. Each account gets one character in it, whose id is `player.id` in Lua (`"p" ..` its number): it never repeats, also after a restart, so a new player cannot inherit an old one's items or turns.

- `state.player` and `state.world` are read when the player joins and when the world starts, and written after every hook call: once, in one transaction, with every key the call changed. The write counts toward the player's Lua time. A write that fails is logged; the world plays on and the next save tries the keys again.
- The character's room is saved on every room change. A player who comes back continues there; when the pack no longer has that room, in the start room. Script globals and the instances of rooms start over, as they always do for a new session.
- Connecting again from another tab or device ends the older session as if the player left (`on_leave` runs), and the older page shows "You are playing in another window" with a **play here** button.
- Timers last (T-0012): a player's timers and the deadlines of questions they were asked are saved with their character, a room's timers with the world, all with absolute deadlines. A player coming back sees their countdowns with the time left; what ran out meanwhile runs at once ([lua-sandbox.md](lua-sandbox.md)). A question still open when the player left closes; when the room asks it again, only the time left counts.
- Characters on rulesets last across worlds (T-0010): in a world of a story on a ruleset, the account's character plays one of its **profiles**, whose sheet, progress and exports belong to the account and outlive the world ([rulesets.md](rulesets.md)).

The data lives in `talespeak.db` in the data directory (Docker volume `talespeak_data`). To back it up, stop the server and copy the file together with `talespeak.db-wal` and `talespeak.db-shm` if they are there.

## API

The web client uses a small JSON API on the same origin. POSTs must send `Content-Type: application/json` and, from a browser, come from the same site (`Origin`, `Sec-Fetch-Site`); others get 403 or 415. Bodies are at most 4 KiB.

| Request | Body | Answer |
| --- | --- | --- |
| `GET /api/me` | | `{"account": {"name", "role", "email", "addEmail", "resetCodes"} or null, "registration": "self", "off" or "sso", "mode": "public" or "edu", "create": true if the account may create worlds, "classCode": true if registering needs it, "email": true if the server sends mail}`. `email` in `account` is its confirmed address, `addEmail` whether it may have one, `resetCodes` whether it makes reset codes. Answers that did something may add `"notice"`, a sentence to show |
| `POST /api/register` | `{"name", "password", "code", "email"}` (`code` with a class code, `email` optional) | The same as `/api/me`, signed in; 409 when the name is taken, 403 when registration is off or `sso`, or for a wrong class code (which counts as a try), 400 for an email address with mail off or a bad one, 403 for one in edu mode |
| `POST /api/login` | `{"name", "password"}` (`name` may be a confirmed email address) | The same, signed in; 401 for a wrong name or password (the same answer and about the same time for both) |
| `POST /api/logout` | `{}` | The same, signed out; the games of this sign-in end |
| `POST /api/password` | `{"password", "new"}` | The same as `/api/me`, still signed in here, signed out everywhere else; 401 for a wrong current password |
| `POST /api/resetcode` | `{"name"}` | `{"name", "code", "expires"}`; 403 unless an admin (any account; 404 for an unknown name) or in edu mode a game master (only plain players of their worlds; every other name gets the same 403) |
| `POST /api/reset` | `{"code", "password"}` | A reset code or a mailed link's code sets the password: the same as `/api/me`, signed in, every other sign-in and game of the account ended; 400 for a code that is unknown, used or expired |
| `POST /api/forgot` | `{"name"}` (a name or a confirmed address) | `{"message"}`, the same for every name; mails a reset link when the account has a confirmed address; 403 with mail off, 429 past the limit |
| `POST /api/email` | `{"email"}` (`""` removes it) | The same as `/api/me`; mails a confirmation link; 400 with mail off or for a bad address, 403 for an edu player, 429 past the mail limits, 503 when the outbox is full |
| `POST /api/verify` | `{"code"}` | Confirms the address of a mailed link, signed in or not: the same as `/api/me`; 400 for a bad link, 409 when another account has the address |
| `GET /api/worlds`, `GET /api/worlds?page=N` | | The lobby: `{"worlds": [card, ...], "packs": [{"id", "title", "description"}, ...], "page": N, "more": true}`, the account's own worlds first, then the others, the newest first in each, 200 a page; `more` is set when page N+1 has more (the client shows **more worlds**); N is 1 to 1000, 1 when left out, 400 otherwise; `packs` is empty when the account may not create worlds |
| `POST /api/worlds` | `{"pack", "title", "visibility": "public" or "private"}` (`title` optional, `visibility` private when left out) | `{"world": card, "invite": {"code", "uses", "expires"}}`; 403 when the account may not create worlds, 409 past its limit, 503 past the server's |
| `POST /api/join` | `{"code"}` | The same as `/api/me` with `"joined": world id`, now a member; 400 for a code that is unknown, used up, expired or of an ended world. When a game master's code makes a player who plays the world now its GM, their game connects again as GM |
| `POST /api/invites` | `{"world", "uses": 1..100, "days": 1..30, "gm": false}` (all but `world` optional) | `{"code", "uses", "expires", "gm"}`; 403 unless an admin or the world's game master, 404 for a world that does not exist, has ended or that the account may not see (a private world it is not a member of); 429 past 20 codes at once and one a minute, 409 when the world has 100 codes that still work |
| `POST /api/finish` | `{"world"}` | Ends the world and answers the lobby like `GET /api/worlds`; 403 unless an admin or its game master, and for the main world; 404 like `/api/invites` |
| `GET /api/members?world=ID` | | `{"members": [{"name", "role", "change"}], "makeGM", "add"}`: the world's members, game masters first, at most 500; `change` when the asking account may change that member, `makeGM` when it may make members GMs, `add` when it may add accounts by name. 403 unless an admin or the world's game master, 404 like `/api/invites` |
| `POST /api/members` | `{"world", "name", "role": "gm", "player" or "none"}` | Makes the account named `name` a game master or a player of the world, or (`none`) no member any more. Admins change anyone and add accounts by name. A world's game master changes only its players: takes them out (`none`) and, outside edu mode, makes them GMs; never another GM or themselves, and adds nobody by name (that is what join codes are for); GMs make 20 changes at once and one a minute after. `{"name", "role"}`; 403 for anything else, 404 for an unknown account or a world as for `/api/invites`. The change is in the world's audit log ([gm-tools.md](gm-tools.md)), and if the account plays the world now, its game ends with "Your role in this world has changed." and connects again with the new role, so `player.gm` is never stale (or, when it may no longer play, goes back to the lobby) |
| `POST /api/profiles` | `{"world", "profile", "name"}` | Picks the account's profile `profile` to play the world, or with `profile` 0 makes a new one named `name`, and answers the lobby like `GET /api/worlds`; 409 with the reason when it does not fit, 400 for a world that needs no character, 403 and 404 like `/ws` ([rulesets.md](rulesets.md)) |

A card is `{"id", "title", "pack", "packTitle", "description", "visibility", "gm", "players", "online", "role", "main", "created", "play", "invite", "finish", "profiles"}`: `gm` is who created it (none for the main world), `players` how many characters it has, `online` how many play now, `role` the account's role in it (`gm`, `player` or none), `main` true for the main world, and `play`, `invite` and `finish` whether the account may play it, make its codes and end it. `profiles` is there only for a world of a story on a ruleset: `{"ruleset": "Tiny Rules 1.0.0", "bound": the option the account picked, "options": [option, ...], "new": true if a new profile may start it, "locked": "Finish Forest Start first."}` (`bound` and `locked` left out when there are none), an option being `{"id", "name", "version", "ok", "upgrade": the version it moves up to, "reason": why it does not fit}`.

Errors are `{"error": "a sentence the player can read"}`. The WebSocket at `/ws?world=ID` (`&as=player` for a game master who plays as a player) needs the sign-in cookie: without one the upgrade gets 401, a world that does not exist, has ended or that the account may not see 404, and an account that may see but not play the world (its pack is not installed) 403. An open game closes with code 4002 when its sign-in ends (see Sign-ins and open games).
