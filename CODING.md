# Coding in Talespeak

Read this before reading code: it says which file does what, so a change only opens the files it needs. Function lists are not kept here; `grep -n '^func' <file>` (or `go doc ./internal/<pkg>`) gives them. Details of the protocol, the Lua API and the settings live in [docs/](docs/).

`scripts/check-codemap.sh` (CI job `codemap`) fails when a source file is missing here, when a path here no longer exists, or when a file is too long. Update this file in the same PR as the code.

## Request path

```
browser (web/static)  --HTTP-->  internal/server  --/api-->  internal/account  --> internal/store (SQLite)
        |                                         --/packs/->  internal/pack (images)
        +--WebSocket /ws-->  internal/ws  -->  internal/hub  -->  internal/game.World  -->  internal/script (Lua)
                                                  (one per world)    (one session per player)   runs content/<pack>
```

1. `internal/server` serves the embedded client, `/healthz`, and routes `/ws`, `/packs/` and `/api/`, with the security headers and the host check.
2. `internal/account` answers `/api/` (sign-in, lobby, join codes) and tells `internal/ws` who a connection is and which world it plays (`ws.Who`).
3. `internal/ws` accepts the WebSocket, applies connection and message limits, and hands each JSON message to the `ws.Session` the game gave it.
4. `internal/hub` is that game: it loads the world named in `ws.Who` (from the store and the pack catalog), unloads idle worlds, and caps worlds and players.
5. `internal/game` turns a message into a Lua hook call on the player's instance of the room script, and the hook's output into messages. Timers and other players' changes are pushed (`ws.Pusher`).
6. `internal/script` runs the Lua in its sandbox with time and memory budgets; `internal/pack` loaded the pack's YAML, Lua and images from `content/`.

## File map

### cmd/talespeak: the binary

| File | What it does |
| --- | --- |
| `cmd/talespeak/main.go` | Commands (`serve`, `account`, `invite`, `mail`, `profile`, `healthcheck`), wiring of every package in `serve`, env helpers |
| `cmd/talespeak/config.go` | Resource limits from `TALESPEAK_*` env vars (`loadLimits`), byte sizes; list in docs/admin-limits.md |
| `cmd/talespeak/accounts.go` | Server modes and who may register, play and create worlds (`access`, the class code); the installed packs and the main world (`packs`); `account` subcommands (`resetcode` too) |
| `cmd/talespeak/mail.go` | Mail settings (`loadMail`: `TALESPEAK_EMAIL_REGISTRATION`, `TALESPEAK_SMTP_*`, `TALESPEAK_PUBLIC_URL`) and `talespeak mail test ADDRESS` |
| `cmd/talespeak/profiles.go` | Wiring of rulesets and profiles (`ps.ruleset`, `profiles`) and the `profile list`/`profile undo` subcommands |

### internal/server: HTTP

| File | What it does |
| --- | --- |
| `internal/server/server.go` | `New`: routes, `/healthz`, security headers; `ContentSecurityPolicy` must match `deploy/Caddyfile` (a test checks) |
| `internal/server/hosts.go` | `ParseHosts` (`TALESPEAK_HOSTS`) and the 421 answer to other Host headers (DNS rebinding) |

### internal/clientip: who the client is

| File | What it does |
| --- | --- |
| `internal/clientip/clientip.go` | `Resolver`: client IP and HTTPS behind trusted proxies; `Key` and `Wide` group addresses (/64, /48) for limits |

### internal/account: sign-in, lobby, invites

| File | What it does |
| --- | --- |
| `internal/account/password.go` | Package doc; argon2id `Hash` and `Verify` |
| `internal/account/account.go` | Settings, `Config`, `Service`, `New`, `Handler` (all `/api/` routes) |
| `internal/account/api.go` | API plumbing: `apiError` and `fail`, the `post` wrapper (same origin, JSON, size, timeout), `writeJSON`, `decode` |
| `internal/account/me.go` | `GET /api/me`, the signed-in account, and `Auth` for the WebSocket (`ws.Who`, with its `Login`) |
| `internal/account/login.go` | Register (class code, optional email), login through the providers, logout (ends that sign-in's games), the session cookie, token hashes and `loginID` |
| `internal/account/provider.go` | `Provider`, the sign-in interface OIDC and SAML will implement; `Local`, the only provider so far (name or confirmed email and password) |
| `internal/account/reset.go` | Reset codes (`POST /api/resetcode`: admins for anyone, edu GMs for their worlds' plain players), `POST /api/reset`, `POST /api/forgot` (mailed link, one answer for every name), `POST /api/password`; each ends the account's games (`Config.SignOut`) |
| `internal/account/email.go` | `POST /api/email` and `POST /api/verify`: confirmed addresses (never edu players), the texts of the mails |
| `internal/account/join.go` | `POST /api/join` with a code, `POST /api/invites` for admins and GMs |
| `internal/account/worlds.go` | The lobby (`GET /api/worlds`, cards, packs with their settings), who may play, see, invite, finish and create worlds, creating and ending worlds |
| `internal/account/settings.go` | A new world's settings (T-0018): the lobby's `Setting`, checking `POST /api/worlds` picks and writing them as the world's first `state.game` |
| `internal/account/names.go` | `ValidName`, `ValidPassword`, `CreateInvite` |
| `internal/account/members.go` | `GET` and `POST /api/members`: a world's members, who may change whose role (admins, GMs for their players, the edu floor), live players reconnect with the new role |
| `internal/account/profiles.go` | Profiles in the lobby: the card's `profiles` (`addProfiles`) and `POST /api/profiles` picking or making a character for a world |
| `internal/account/limit.go` | Token-bucket `limiter` per key for sign-in and API rates |

### internal/store: persistence

| File | What it does |
| --- | --- |
| `internal/store/store.go` | The interfaces game code uses (`Store`, `Accounts`, `Worlds`, `Packs`, `Profiles`) and their records; `ErrNotFound`, `ErrNameTaken` |
| `internal/store/mail.go` | `Emails`, `Tokens` (one-time codes, only their hashes) and `Outbox` (mail waiting, `MaxOutbox`) |
| `internal/store/profiles.go` | The `Profiles` interface and its records: `Profile`, `Progress`, `Bind`, `ProgressSave`; `ErrProfiles`, `ErrProfileChanged` |
| `internal/store/sqlite/sqlite.go` | SQLite backend (pure Go): `Open`, WAL, migrations from `internal/store/sqlite/migrations/`, settings |
| `internal/store/sqlite/accounts.go` | Accounts, passwords, roles, login tokens |
| `internal/store/sqlite/worlds.go` | Worlds, pack versions, invites, characters, saved `state.player`/`state.world`/`state.game` (`Save`), the GM audit log, `FinishWorld` |
| `internal/store/sqlite/gamestate.go` | `state.game` per world (`game_state`, T-0018): loading it and the rows `Save` writes |
| `internal/store/sqlite/members.go` | World members and their roles (`world_members`): `Member`, `AddMember`, `RemoveMember`, `Members` |
| `internal/store/sqlite/mail.go` | Confirmed email addresses, `account_tokens`, `mail_outbox` (a mail goes with its code), `GMOf` |
| `internal/store/sqlite/timers.go` | Timers that last (T-0012): player timers and question deadlines (`player_timers`), room timers (`room_timers`), written by `Save` |
| `internal/store/sqlite/profiles.go` | Profiles (T-0010): sheets, progress and exports, binding a character, migration snapshots and undo; `putProfile`, written by `Save` only while the profile's generation matches |
| `internal/store/sqlite/clock.go` | A world's time (T-0012): pause, game clock and `/mintime` on `worlds` (`WorldClock`, written by `Save`), each member's extra time (`ExtraTime`, `SetExtraTime`) |

A schema change is a new numbered file in `internal/store/sqlite/migrations/`; never edit an applied one.

### internal/ws: the WebSocket endpoint

| File | What it does |
| --- | --- |
| `internal/ws/ws.go` | `Message` (every field of the protocol), `Who`, the `Game`/`Session`/`Pusher`/`Replacer` interfaces, `Server`, origin check, connection and message rate limits, push loop, `Shutdown` |
| `internal/ws/options.go` | `Option` and the `With...` options of a `Server`: limits, client address, sign-in, game |
| `internal/ws/signin.go` | The sign-in behind each connection: `SignOut` closes a sign-in's connections at once, `WithRecheck` checks it again every minute; close code `StatusSignedOut` |

### internal/mail: sending mail

| File | What it does |
| --- | --- |
| `internal/mail/mail.go` | Package doc; `Config` (`Check`), `Send` with net/smtp (STARTTLS or TLS, never plain), `ValidAddress`, the mail's headers |
| `internal/mail/outbox.go` | `Outbox`: sends what waits in the store outside any request (`Kick`, `Run`, `Flush`), retries with backoff, never logs an address |
| `internal/mail/mailtest/mailtest.go` | A fake SMTP server in the test process (STARTTLS, TLS, AUTH PLAIN, failures) for tests of any package |

### internal/hub: the worlds of a server

| File | What it does |
| --- | --- |
| `internal/hub/hub.go` | `Hub`: `NewSession` loads the world of `ws.Who` and tracks players per network, loading and evicting worlds, `Reap`/`Run` for idle worlds, `Finish`, `Close` |

### internal/game: one running world

| File | What it does |
| --- | --- |
| `internal/game/game.go` | Package doc; `World` (fields and the `w.mu` lock), options, `NewWorld`, `NewSession`, `Close`, idle checks and `End` (with the `world.finish` text) |
| `internal/game/budget.go` | Per-client Lua time and script memory budgets: `ClientKey`, `timed`, `charged`, `release`; `ErrBusy`, `ErrClientFull` |
| `internal/game/session.go` | `session` (one player, its fields), `Handle` dispatching `choose`/`input`/`click`/`answer`, `Close`, the player's room script instances (`env`, `start`, `free`, `victim`), pending questions |
| `internal/game/input.go` | Entering rooms (`enterLocked`, `place`), answering questions, typed lines and clicks, running hooks (`run`, `after`) and the error text players read |
| `internal/game/host.go` | The session as `script.Host`: `flush`, `Say`, `DescribeRoom`, `Items`, `Go`, `Image`, state access; `voice` (a pack's `from` is never a player's name) |
| `internal/game/commands.go` | Slash commands: a line starting with `/` never reaches `on_input`; dispatch to the engine's commands or the room script's (`on_command`), notices a pack cannot fake, the audit log, the broadcast rate limit, `/help` |
| `internal/game/gmtools.go` | The GM commands `/players`, `/rooms`, `/go`, `/say`, `/sayroom`, `/reset` (`resetRoom`, `restart`) and `/audit`; `RoleChanged`; `/gm` (GM mode, `session.member` vs `session.gm`) |
| `internal/game/gmtime.go` | The GM commands for the world's time: `/pause`, `/resume`, `/extratime`, `/mintime`; countdowns sent again on resume; parsing times (`parseDuration`) |
| `internal/game/timertools.go` | The GM timer tools `/timers`, `/extend N TIME`, `/cancel N`: the numbered list a GM saw last, refusing numbers whose timer changed |
| `internal/game/clock.go` | The world clock (stands still while paused) and the game clock (runs unpaused with players online); arming and disarming timers on them; extra time and `/mintime` (`shownTime`); `held` players while paused |
| `internal/game/party.go` | What a room's players share: unique names, `room.players`/`world.players`, leaving (`on_leave`), room timers (one that runs out in an empty room waits, `due`, for the next player) |
| `internal/game/events.go` | What happened in a room (`room.event`, T-0046): telling the room, the ring of each room's last 10 events, the replay when a player connects |
| `internal/game/typed.go` | What players type stays with them (T-0046): the words each player online typed lately and their state's strings, the pack's own words, `shareable` refusing shared text with typed words |
| `internal/game/push.go` | Output nobody asked for: the push queue (`ws.Pusher`), player timers (real or game time), question timeouts |
| `internal/game/lasting.go` | Timers that last with a store: noting changes for the next save, restoring room timers at load and a player's at join, question deadlines by `questionName` |
| `internal/game/world.go` | `state.world` and `on_change` delivery to other players |
| `internal/game/gamestate.go` | `state.game` (T-0018): loading it, the pack's setting defaults, noting changes for the next save, `world.finish` (`WithFinish`), refused state writes |
| `internal/game/settings.go` | The GM command `/set`: list, show and change the world's settings, audited, every player told |
| `internal/game/store.go` | With a store: loading a character (and its timers), saving after every hook (timers included), keeping what a failed save lost, replaced sessions |
| `internal/game/profile.go` | A world on a ruleset (`WithRuleset`): its modules and `pack` info, checking and loading the player's profile at join, `state.profile`, `progress.*` and saving them |

### internal/script: the Lua sandbox

| File | What it does |
| --- | --- |
| `internal/script/script.go` | Package doc; `Limits`, errors, `Env`, `Runtime`, `New`, `Close`, `Compile`, `Instance`, `Release`, `Load` |
| `internal/script/host.go` | What a script reaches the game through: the `Host`, `Party` and `Clock` interfaces, `Player`, `Voice`, `Item`, `Dialog` and its `Check` |
| `internal/script/thread.go` | Hooks: `Run`, `RunHook`, `Resume`, `Answer`, `Expire`, `Cancel`; each on its own goroutine, paused until the player answers |
| `internal/script/sandbox.go` | The allowlisted libraries (`openSandbox`): every global a script sees is registered here; read-only proxies, `newEnv` |
| `internal/script/safelib.go` | The wrapped string and table functions (`find`, `sub`, `rep`, `concat`, `table.concat`, `table.sort`) with bounded arguments and results |
| `internal/script/ui.go` | Output to the player: `say`, `room.describe`, `room.go`, `ui.dialog`, `ui.select`, `ui.ask`, `ui.items`, `ui.image`; `ValidItemID`; speaker names (`fromArg`) |
| `internal/script/events.go` | `room.event` and the `Events` interface; `Shared`, the check of text that reaches other players; `ReservedName` (names that read as staff) |
| `internal/script/words.go` | `words.normalize` and `words.same` |
| `internal/script/state.go` | `State`: `state.player`, `state.world` and `state.game` get/set/update, encoding, checking writes against the limits |
| `internal/script/statelimits.go` | `StateLimits` (sizes, key counts, depth, writes per hook) with their defaults and bounds; `Refusals` |
| `internal/script/game.go` | `state.game` (only a GM in GM mode writes it, settings keep to their choices) and `world.finish`; the `Game` interface; `EncodeString` |
| `internal/script/profile.go` | `state.profile`, `progress.export`/`complete` through the optional `ProfileHost`, the read-only `pack` table (`SetPack`) |
| `internal/script/migrate.go` | `Migrate`: a ruleset migration's `migrate(sheet)` in a sandbox of its own |
| `internal/script/timer.go` | `timer.*` (`game = true` through `Clock`), question timeouts, `room.players`, `world.players`, `world.paused`, `world.time` |
| `internal/script/commands.go` | A room script's `commands` table and `on_command` hook (`Env.Commands`, `ValidCommand`) |
| `internal/script/perm.go` | `perm.define` and `player:can`: pack permissions per instance, the engine's permissions only in GM mode |
| `internal/script/require.go` | `require` and pack modules (`AddModule`) |
| `internal/script/rewrite.go` | Compile-time rewrite of `..` into the size-checked `__concat` |
| `internal/script/memory.go` | The allocation watcher, the walk that measures what an instance keeps, `Reserve` and `Kept` |
| `internal/script/table.go` | Table size guards (`MaxArrayIndex`, `table.insert`) |
| `internal/script/format.go` | A safe `string.format` |
| `internal/script/random.go` | The sandbox's `math`: an allowlist without `randomseed`, `math.random` on `math/rand/v2` |

### internal/pack: story packs

| File | What it does |
| --- | --- |
| `internal/pack/pack.go` | Package doc; `Pack` (with the `Manifest` inline), `Room`, `Item`, `Limits`; `Load` reads `pack.yaml`, rooms, scripts, lib modules and checks them; content hash |
| `internal/pack/settings.go` | `settings:` in `pack.yaml` (T-0018): `Setting`, checking names, choices, labels and defaults, `ValidChoice` |
| `internal/pack/assets.go` | Pack images in memory, `AssetHandler` for `/packs/`, `AssetURL`, `Image` |
| `internal/pack/catalog.go` | `Catalog` of installed packs and versions: `Newest`, `Pick`, `Match` |
| `internal/pack/ruleset.go` | Rulesets and continuations (T-0010): `Manifest` (`kind`, `ruleset`, `continues`, `imports`, `exports`), its checks, ruleset migrations, `Catalog.Ruleset` and `Catalog.Check` |
| `internal/pack/version.go` | Semantic versions and ranges (`ParseVersion`, `ParseRange`, `Compatible`) |
| `internal/pack/lint.go` | Offline lint: no external URL in YAML strings or Lua string literals |

### internal/profile: characters across stories

| File | What it does |
| --- | --- |
| `internal/profile/check.go` | Which profile may play which world: `Check` (ruleset, version line, migrations, continuations) and `Locked`, with the reasons the lobby shows |
| `internal/profile/profile.go` | `Service`: the lobby's `Cards` and `Pick` (binding, moving up a version, running the migrations, copying imports) |

### web: the browser client

| File | What it does |
| --- | --- |
| `web/web.go` | Embeds `web/static/` into the binary |
| `web/static/app.js` | The `game` Alpine component: its state and `init`; merges the parts below with `Object.defineProperties` (keeps getters) |
| `web/static/lobby.js` | Part: sign-in, the account, the lobby and its world cards, join codes, invites, creating and ending worlds |
| `web/static/members.js` | Part: a world's members on its card (`GET`/`POST /api/members`), the play as GM / as player buttons |
| `web/static/password.js` | Part: forgot password, reset with a code or a mailed link (`#reset=`, `#verify=`), changing the password, an email address, reset codes for others, the class code and email at registration |
| `web/static/profiles.js` | Part: who plays a world on a ruleset, picking or making a character on its card (`cleanProfiles`, `pickProfile`) |
| `web/static/settings.js` | Part: a new world's settings in the New world form (`cleanSettings`, the picks sent to `POST /api/worlds`) |
| `web/static/play.js` | Part: starting and stopping a game, `receive` for every engine message, story lines and scrolling, timers, items; room events shown once by id |
| `web/static/dialog.js` | Part: dialogs, multiple choice, typed answers, the input line with its history and keys |
| `web/static/view.js` | Part: pictures, read aloud, large text and their labels |
| `web/static/pause.js` | Part: a paused world (T-0012): countdowns stand still (`clockNow`, `endIn`), the open question's countdown from `question:` timer messages |
| `web/static/clean.js` | Limits that match the engine, cleaning server data (`cleanCard`, `cleanItems`, `imageURL`, ...), settings, the page hash, clock text |
| `web/static/api.js` | `api`: the only `fetch`, to the account API's fixed paths (`scripts/check-web.sh` allows no other) |
| `web/static/storylog.js` | The story log kept in the browser per account and world (`loadLog`, `saveLog`, `forgetLogs`); session storage in edu mode; room events with their ids, never a GM's lines |
| `web/static/net.js` | The WebSocket to one world: reconnect with backoff, pings, close codes |
| `web/static/speech.js` | Read aloud with local voices only |

`web/static/index.html` holds only simple Alpine expressions, `web/static/style.css` the theme; vendored Alpine is pinned in `web/vendor.json`. More in docs/web-client.md.

### content: packs (YAML and Lua)

| Path | What it does |
| --- | --- |
| `content/content.go` | Embeds the demo pack into the binary (`Demo`) |
| `content/demo/scripts/` | The demo escape: cell, corridor, guard room, clearing |
| `content/demo/lib/rooms.lua` | Demo helpers: Whisp, the guard's countdown, solved rooms, the key box code, dying and the GM's `/revive` |
| `content/demo/lib/death.lua` | Three death modes from the world's `death` setting (stay, revive, respawn at a save point), for any pack to copy |
| `content/demo/lib/round.lua` | Free and turn-based rounds in a room, for any pack to copy |
| `content/demo/lib/items.lua` | Shared, split and used-up items and effects, for any pack to copy |
| `content/clockmaker/scripts/` | The co-op case "The Clockmaker's Silence": one script per location |
| `content/clockmaker/lib/case.lua` | The case module the rooms require: the party, the five levels, arriving and leaving, rewinding a level; installs its parts below |
| `content/clockmaker/lib/case_secrets.lua` | What each world picks once (desk word, clock times, map, crate, thief, evidence cards) and reading those items |
| `content/clockmaker/lib/case_bags.lua` | The satchels: carrying, giving and showing items |
| `content/clockmaker/lib/case_spots.lua` | Save spots: the door at the end of a level, going on without players who left |
| `content/clockmaker/lib/case_input.lua` | The casebook and the hooks every room shares: items, clicks, typed input |
| `content/test-pack/scripts/` | The tiny pack the browser tests run: every renderer once |
| `content/test-pack/lib/visits.lua` | A module to test `require` |
| `content/ruleset-test/tiny-rules/lib/sheet.lua` | Test ruleset Tiny Rules 1 (docs/rulesets.md): a level in `state.profile` |
| `content/ruleset-test/tiny-rules-2/lib/sheet.lua` | Tiny Rules 2: the level moved into `stats`, with courage |
| `content/ruleset-test/tiny-rules-2/migrations/2.lua` | The migration of a Tiny Rules 1 sheet to 2 |
| `content/ruleset-test/forest-start/scripts/` | Story on Tiny Rules 1: free the fox, export it and complete the story |
| `content/ruleset-test/dragon-isle/scripts/` | Story that continues Forest Start and imports its fox |
| `content/ruleset-test/sky-tower/scripts/` | Story on Tiny Rules 2, for moving a profile up a major version |

Pack format: docs/content-format.md; patterns for pack authors: docs/pack-patterns.md.

### scripts and deploy

| File | What it does |
| --- | --- |
| `scripts/check-codemap.sh` | This map and the file size limits (CI `codemap`) |
| `scripts/check-web.sh` | Client checks without a browser: modules parse, no external URLs, no HTML sinks (CI `web`) |
| `scripts/vendor.sh` | Vendored browser code: check, verify, update against `web/vendor.json` |
| `scripts/fuzz.sh` | Runs every `Fuzz*` target briefly |
| `scripts/board.sh` | Builds `tasks/BOARD.md` from the task files |
| `scripts/deploy.sh` | Deploys to a server over SSH and checks `/healthz` |
| `deploy/remote-up.sh` | Runs on the server: pulls and starts the images, waits until healthy |
| `scripts/clockmaker-maps.py` | Generates the clockmaker map puzzle pictures |

## Where to change what

- **A new Lua function for packs:** register it in `openSandbox` (`internal/script/sandbox.go`, or the file of its library, such as `timer.go`); if it needs the game, add a method to `Host` or `Party` (`internal/script/host.go`) and implement it on `session` in `internal/game`. Document it in docs/lua-sandbox.md. Ask first whether it is a core primitive or belongs in pack Lua (CLAUDE.md).
- **A new WebSocket message:** fields in `ws.Message` (`internal/ws/ws.go`), client to engine in `session.handle` (`internal/game/session.go`), engine to client in `receive` (`web/static/play.js`); document it in docs/websocket-protocol.md.
- **A new `/api/` route:** `Service.Handler` (`internal/account/account.go`) with `s.post` (`api.go`) or `s.get` (`worlds.go`); check edu mode and world visibility with the `may...` helpers in `internal/account/worlds.go`; the client's `fetch` paths are checked by `scripts/check-web.sh`; document it in docs/accounts.md.
- **A new setting:** env var in `cmd/talespeak/config.go` or `accounts.go`, passed down through a `Config` or `WithX` option; document it in docs/admin-limits.md or docs/setup-server.md and `deploy/.env.example`.
- **A database change:** a new migration file plus the `store` interface method and its SQLite implementation.
- **A pack room or module:** `content/<pack>/rooms/*.yaml`, `scripts/*.lua`, `lib/*.lua`; tests in `internal/game/*_test.go` drive the pack through a `World`.
- **Rulesets, profiles, continuations:** pack fields in `internal/pack/ruleset.go`, who may play what in `internal/profile/check.go`, the Lua side in `internal/script/profile.go` and `internal/game/profile.go`; design in docs/rulesets.md.

## Rules that span files

- **A sign-in that ends ends its games.** The WebSocket checks the sign-in at connect (`account.Service.Auth`), every `TALESPEAK_SIGNIN_RECHECK` after, and a sign-out, password change or reset closes the account's connections at once (`account.Config.SignOut` -> `ws.Server.SignOut`); the game session then ends as on any close.
- **One lock per world.** `World.mu` guards the Lua runtime, every session's fields and all world state; every Lua call runs under it. Methods that need it held end in `Locked` or say "the caller holds w.mu". Nothing blocks while holding it.
- **Hooks run on their own goroutine.** `ui.dialog`, `ui.select` and `ui.ask` block that goroutine until the player answers (never a coroutine yield). Timers and pushes call in from timer goroutines and take `w.mu` themselves.
- **Every outside input is bounded where it enters:** WebSocket message size and rate (`ws`), API rates (`account/limit.go`), pack sizes (`pack.Limits`), Lua time and memory (`script.Limits`), per-client budgets (`game`).
- **Timers count on the world's clocks** (`internal/game/clock.go`): a deadline is a time on the world clock (the wall clock less pauses) or, for game-time timers, on the game clock. Start and restart timers with `w.clockNow(game)` and `w.arm`, never `w.now()` and `w.after` directly, so `/pause` and game time stop them; a timer's `fire` checks `w.ready` first.
- **Edu mode (kids):** every world is private, players join only with a code, and only admins and GMs create worlds, whatever the other settings say (docs/accounts.md). It is checked in `internal/account/worlds.go` (`mayPlay`, `mayKnow`, `mayCreate`, the lobby) and set up in `cmd/talespeak/accounts.go`; no pack, request or Lua may get around it.
- **No chat, no faked senders** (T-0046): players never send each other text. Every new way for a script to reach other players (like `state.world`, `room.event`, room timer labels, `world.finish`) passes its text through `World.shareable` (`internal/game/typed.go`), and every new typed input reaches hooks through `session.typed` first. Only game masters send messages (`notice` with role `gm`); a pack's `from` passes `script.ReservedName` and `World.voice`.
- **Core vs Lua:** the Go core is glue; game mechanics (turns, items, death, endings) are pack Lua; the core gains only generic primitives.
- **Offline:** nothing loads from another origin, in the client or in packs (`scripts/check-web.sh`, `internal/pack/lint.go`).

## Tests

- Next to the code: `x_test.go` for the main cases, `x_edge_test.go` for limits and odd input, `x_fuzz_test.go` for fuzz targets, or a file per feature (`timer_test.go`). Look for the file named after the feature first.
- Test helpers live in the test file that uses them most: in `internal/game`, `move_test.go` (`roomsWorld`, `texts`) and `demo_test.go` (`play` and the demo steps).
- Browser tests: `e2e/tests/*.spec.js`, always importing `e2e/tests/fixtures.js` (offline and CSP guard, accounts per test).
- How to run them: docs/testing.md.

## Style guide

For people and agents alike. It writes down how the code is already written.

### Every change

- Read this file, then only the files the change needs; grep for a function before opening a whole file.
- Small, surgical changes: one topic per PR, no reformatting or renaming of code the change does not need.
- Files stay under 500 lines, grouped by function; CI fails at 600 (tests 800). A new feature goes into a new file instead of growing a big one. `scripts/size-allow.txt` lists the files still waiting for their split; they may not grow.
- Update every doc the change makes wrong, in the same PR: this file, docs/, README, task files.
- Never a secret anywhere, `protocol/` included: write `$VARIABLES`.
- A new dependency only after checking its license and size; nothing that needs cgo or a network service.

### Tests and security reviews

This is how to run them; what they found goes in `protocol/`, never here.

**On every task**, as the task-workflow skill (`.claude/skills/task-workflow/SKILL.md`) says:

1. Commit the change, then start the `test-engineer` agent (`.claude/agents/test-engineer.md`) with the task id, the task file and the base `origin/dev`. It adds the missing tests and reports bugs with a failing test; fix the code.
2. Start the `security-reviewer` agent (`.claude/agents/security-reviewer.md`) on the same diff. Fix every critical, high and medium finding; fix a low one or say in the PR why not.
3. Both can run at once when the code is stable. Without an Agent tool, follow the agent's file yourself and say so in the PR.
4. Write both results into the task's protocol entry: what each agent found, what was fixed, the commands and trimmed output.

**From time to time, a full pass** over the whole code, not one diff, because findings that span several PRs only show there:

1. When: at least every 10 merged PRs or every two weeks, whichever comes first, and before a tag to prod. The newest `protocol/*-full-review.md` says when the last one ran and at which commit.
2. Branch from `dev` (`full-review-YYYY-MM-DD`). Give both agents the whole repo as scope and the last full pass's commit as the base, so they read everything but look hardest at what changed since. Split the scope by package when one agent cannot read it all (for example `internal/script` and `internal/game` apart from the rest).
3. The test engineer also reports coverage per package and the packages or files without tests. The security reviewer also checks the rules of this file and of docs/ across packages: limits on every outside input, offline and CSP, kids safety, secrets in logs.
4. Fix what is small in the same PR. Everything else becomes a task in `tasks/open/` with the finding, its severity and `file:line`.
5. Log it as `protocol/YYYY-MM-DD-full-review.md`: the commit reviewed, the commands, every finding with its severity and what happened to it (fixed, task T-NNNN, or why not). Secrets only as `$VARIABLES`.

### Go

- Pure Go (`CGO_ENABLED=0`), standard library first, `gofmt` and `go vet` clean.
- A package comment on every package. A doc comment in full sentences on every exported name and every unexported one that is not obvious: what it is, and why. A limit says what it protects against.
- Limits are named constants, enforced where outside input enters.
- Errors callers test are `var ErrX = errors.New(...)`, checked with `errors.Is`; wrap with `fmt.Errorf("...: %w", err)`. What a player reads is a short plain sentence, never internals.
- Settings come through `WithX` options or a `Config` struct; defaults are named constants.
- Struct fields say in a comment which lock guards them; time comes from the injected clock (`WithClock`), never `time.Now` in game logic.
- Log with `log/slog`; never log passwords, tokens or join codes.
- Short plain names without stutter: `game.World`, not `game.GameWorld`.

### JavaScript (`web/static`)

- Plain ES modules, no build step; the only library is the vendored Alpine CSP build.
- Server text reaches the page only through `x-text` or `textContent`; no `x-html`, `eval` or inline scripts. HTML attributes hold only simple expressions.
- Every field from the server is cleaned and cut to the engine's limits before use (the helpers in `clean.js`).
- `fetch` only the server's own `/api/` paths.

### Lua (packs)

- Shared code goes in `lib/<name>.lua`, loaded with `require`; a module's top level never calls `say`, `ui` or `state.player`.
- What must last goes in `state.player` or `state.world` (shared changes through `state.world.update`), never in globals.
- Libs follow the 500-line rule too.

### Tests

- Standard `testing`, table-driven, no assertion library; injected clock, no sleeps, no network; pass with `-race` in any order.
- New tests go into a topic file instead of growing a long one. Fuzz anything that parses outside input. A client change gets a Playwright test.

### Writing (comments, docs, commits, protocol)

- Plain English, short sentences, present tense; say what and why, not the history of the change.
- Commits: `T-NNNN: what changed` (or `ci:`, `e2e:`, `build:` outside a task), signed off with `git commit -s`.
