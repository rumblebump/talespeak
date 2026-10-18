# 2026-10-18: T-0034: local accounts, invites, and state that lasts

**Result:** all acceptance items done. Go tests pass with -race, all 81 Playwright tests pass, and the black-box checks against a local server behave as documented. The security review found 1 high, 1 medium and 6 low issues; 7 are fixed, and one low one (open games after a password change) is a follow-up in T-0013. The test engineer found 2 bugs; both are fixed.
**Task:** T-0034 (first slices of T-0010, T-0013 and T-0018)
**Environment:** cloud sandbox, Linux amd64, Go 1.26.9, Chromium from Playwright. Branch `claude/project-thread-artxd4`, rebased on `dev` after PR #30. New direct dependency: `golang.org/x/crypto` v0.58.0 (argon2id; BSD-3, pure Go, already in go.sum as indirect).

Carl asked for accounts and saving next: local sign-ins, `state.player` and `state.world` in SQLite, and invites to a world. The task was first numbered T-0032 on this branch; PR #30 took that number meanwhile, so it is T-0034 now.

## Steps

```sh
gofmt -l .
go vet ./...
go test -race -count=1 ./...
go test -short -count=1 ./...
go test -cover ./...
go test -run '^$' -fuzz '^FuzzStateLoad$' -fuzztime 15s ./internal/script
go test -run '^$' -fuzz '^FuzzNormalizeCode$' -fuzztime 15s ./internal/account
go test -run '^$' -fuzz '^FuzzHTTPS$' -fuzztime 15s ./internal/clientip
./scripts/check-web.sh
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e && npx playwright test
```

```text
gofmt: none; go vet: ok
-race: all packages ok; -short: all ok
coverage: cmd/talespeak 92.9%, account 92.4%, clientip 96.0%, game 96.5%,
  script 97.3%, store/sqlite 85.1% (the rest is database error branches), ws 97.8%
fuzz: FuzzStateLoad, FuzzNormalizeCode, FuzzHTTPS pass
check-web: ok
Playwright: 81 passed, 3 skipped
govulncheck: not run, vuln.go.dev answers 403 through the sandbox proxy
```

Black-box checks: the binary serving the test pack on a fresh data directory, driven with curl (WebSocket upgrade with plain upgrade headers). Codes and tokens are masked.

```sh
TALESPEAK_LISTEN=127.0.0.1:18790 TALESPEAK_DATA=$DATA TALESPEAK_PACK=content/test-pack bin/talespeak serve
curl -s $URL/api/me
curl -c $JAR -H 'Content-Type: application/json' -H 'Sec-Fetch-Site: same-origin' -d '{"name":"Mira","password":"$PASSWORD"}' $URL/api/register
# … the same register with name "mira", with Sec-Fetch-Site: cross-site, and as a form post
# GET /ws without and with the cookie; POST /api/invites as a player; 12 sign-ins with a wrong password
bin/talespeak invite -data $DATA
sqlite3 $DATA/talespeak.db  # what the tables hold
```

```text
me signed out: {"account":null,"registration":"self","world":{"pack":"test-pack","access":"open","play":false,"invite":false}}
register: 200, one talespeak_session cookie
same name other case: 409
cross-site: 403
form post: 415
ws no cookie: 401
ws cookie: 101
player invite: 403
12 wrong passwords: 401 ×10, then 429 429
cli invite: $CODE  (player of test-pack, 1 use(s), expires 2026-10-25T19:45:36Z)
accounts: Mira with a $argon2id$ hash; logins: one 32-byte token hash; invites: one 32-byte code hash
```

## What changed

- **Store** (`internal/store`, `internal/store/sqlite`, migration `0002_accounts_worlds.sql`): accounts (names unique in any letter case), sign-in sessions by token hash, worlds, world members, invites by code hash, one character per account and world (AUTOINCREMENT ids, so `player.id` never repeats), `player_state` and `world_state` as STRICT tables. `Save` writes one hook's changes in one transaction with fixed queries. SQLite now runs with `synchronous=NORMAL` (WAL).
- **Accounts** (`internal/account`): argon2id password hashes (at most 2 at once, 8 waiting, else 503), the JSON API under `/api/` (me, register, login, logout, join, invites), same-origin checks (Sec-Fetch-Site, Origin, JSON only, 4 KiB bodies), rate limits per address (IPv6 by /64), per name and address, per name, and per account for invite codes. Cookie `__Host-talespeak_session` over HTTPS, `talespeak_session` over plain HTTP.
- **Settings:** `TALESPEAK_MODE`, `TALESPEAK_REGISTRATION`, `TALESPEAK_WORLD_ACCESS`, `TALESPEAK_SIGNIN_BURST`. Edu mode is always invite only. Staging runs public, self registration, open world.
- **Command line:** `talespeak account add [-admin] NAME`, `account password NAME`, `account role NAME admin|player` (passwords from standard input), `talespeak invite [-uses N] [-days N] [-gm]`.
- **Game** (`internal/game/store.go`): `WithStore` loads `state.world` at start and the character's state on join, saves after every hook (counted as Lua time), retries a failed save with the next one, and keeps the failed save of a player who left until it can be written. The room is saved on every move; a returning player continues there, with the room's items shown. Joining again elsewhere ends the older session, closed with code 4001.
- **WebSocket** (`internal/ws`): `WithAuth` after the connection limits; the session gets the account's id and name.
- **Web client:** sign-in and register form, invite code form, sign out, make an invite, a "play here" view after a takeover; fetch only to `/api/[a-z]+` (enforced by `scripts/check-web.sh`).
- Docs: new `docs/accounts.md`; updates to admin-limits, lua-sandbox, pack-patterns, testing, websocket-protocol, web-client, setup-server, setup-dev.

## Reviews

**security-reviewer:**
- High: the account limits counted each IPv6 address on its own, so a /64 gave unlimited registrations, filled the hashing pool, and could push a guessed name's bucket out of the limiter map. Fixed:
  - Every account limit now uses the /64 key the WebSocket limits use (`clientip.Key`).
  - A full limiter map no longer drops an empty bucket: a new key gets one try, and the bucket closest to full is dropped.
  - The hash pool refuses at once (503) when 8 sign-ins are waiting already.
  - `docs/accounts.md` repeats the `TALESPEAK_TRUSTED_PROXIES` warning.
- Medium: ten wrong passwords from one address locked anyone out. Fixed: the strict name limit counts per name and address; only wrong passwords use up name tokens; a looser limit of 100 wrong passwords an hour per name covers many addresses.
- Low: the WebSocket upgrade queried the database before the connection limits. Fixed: auth runs after them. `GET /api/me` stays without its own limit: it is one indexed lookup, about the cost of a static file.
- Low: `/api/join` used up invites of other worlds and made the account a member there. Fixed: `RedeemInvite` takes the world.
- Low: no `__Host-` cookie prefix. Fixed over HTTPS.
- Low: one SQL query was built from strings. Fixed: fixed query pairs.
- Low (data): a failed save of a player who left was lost. Fixed: kept on the world (at most 1000, oldest dropped and logged) and written by the next save or when the player returns.
- Low, open: a password change, sign-out or demotion does not end a game that is open already; the WebSocket checks the sign-in only when it connects. Follow-up noted in T-0013.

**test-engineer:**
- Added tests in `internal/store/sqlite/accounts_test.go`, `internal/script/state_load_test.go` (with `FuzzStateLoad`), `internal/clientip/https_test.go` (`FuzzHTTPS`), `internal/account/{limit,names,api_edge}_test.go` (`FuzzNormalizeCode`), `internal/game/store_edge_test.go`, `internal/ws/auth_test.go`, `cmd/talespeak/accounts_test.go`.
- Bug: `CreateLogin` could delete the session it had just made when more than 10 ended in the same second. Fixed: the pruning leaves the new token out; `createlogin_tie_test.go` covers it.
- Bug: the code comment said nobody elsewhere could lock an account out, but the per-name limit across addresses does that. Kept on purpose and documented in the code and `docs/accounts.md`: someone with many addresses can block sign-ins to one account (a correct password too) while they keep guessing, about one try per 36 s once the 100 are used up; letting correct passwords through would let a guesser go on until one is right. Open sessions are not affected.
- Smaller: `CreateLogin` and `CreateInvite` pruned with the real clock. Fixed: they take `now`.

## Findings

- Still open, each in its task file: pack versions and profiles (T-0010), roles beyond admin and GM, GM tools and the audit log (T-0011), timers and room timers after a restart, so the turn deadline in `lib/round.lua` (T-0012), reset codes, email, SSO and checking open games after a password change (T-0013), `state.game`, death modes, finishing a world from the game and the edu chat filter for shared state (T-0018).
- Staging needs an admin account to make invites: `printf '%s\n' "$PASSWORD" | docker compose exec -T talespeak /talespeak account add -admin $NAME` (docs/setup-server.md).
