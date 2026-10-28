# 2026-10-28: All open tasks before the pack editor, in one batch

**Result:** the rest of T-0011, T-0012, T-0013 with email, T-0018, the rest of T-0010, T-0033 and T-0039 in one branch, with Carl's 15 design calls. All checks green; one final test-engineer and security-reviewer run over the whole batch, every finding fixed.
**Task:** T-0010, T-0011, T-0012, T-0013, T-0018, T-0033, T-0039
**Environment:** local container, Go 1.26.10, Playwright 1.56.1 (the container's Chromium through an uncommitted local config), branch `claude/project-thread-hh6t1w` on top of dev `dee8b61`.

## What changed

- **T-0011 (design calls 1 to 3):** GM mode like sudo (**play as GM** / **play as player**, `as=player`, `/gm [on|off]`, audited and marked); `perm.define` / `player:can`, the engine's permissions only in GM mode; the lobby's members list (`GET`/`POST /api/members`); the edu floor tests.
- **T-0012 (4 to 8):** `/pause` and `/resume` (through restarts), game-time timers and `world.time()`, `/timers`, `/extend`, `/cancel`, `/extratime`, `/mintime`; migration 0007.
- **T-0013 (15):** the provider interface, registration `off`/`self`/`sso`, class code, email at registration, reset codes, mailed reset and confirmation links, the SMTP outbox on net/smtp, the sign-in recheck (close code 4002); migration 0008.
- **T-0018 (9 to 12):** `state.game` per world, `settings:` in `pack.yaml` and `/set`, `world.finish`, the demo's `lib/death.lua` (demo 1.2.0), `TALESPEAK_STATE_*`; migration 0009.
- **T-0010 (13, 14):** rulesets, profiles and continuing stories, ruleset migrations with a snapshot and `talespeak profile undo`; migration 0010.
- **T-0033:** `math.randomseed` gone, `math.random` on math/rand/v2. **T-0039:** one 30 s deadline in `TestRealClockWorldChanges`.
- When merging the parts: `state.profile`, `progress.export` and ruleset migrations ignored the configured state limits (zero-value encoder); they use the server's limits now. `internal/ws/options.go` split from `ws.go`.

## Steps

```sh
gofmt -l .
go vet ./...
scripts/check-codemap.sh
scripts/check-web.sh
go test -race -count=1 ./...
./scripts/fuzz.sh 10s
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e && npx playwright test -c pw-local.config.js   # local config only sets the Chromium path
```

```text
(gofmt: no files; vet clean; check-codemap and check-web exit 0)
ok  	github.com/rumblebump/talespeak/cmd/talespeak
ok  	github.com/rumblebump/talespeak/internal/account
ok  	github.com/rumblebump/talespeak/internal/game
ok  	github.com/rumblebump/talespeak/internal/mail
ok  	github.com/rumblebump/talespeak/internal/profile
ok  	github.com/rumblebump/talespeak/internal/script
ok  	github.com/rumblebump/talespeak/internal/store/sqlite
... every package ok
playwright: 123 passed, 3 skipped (1.6m)
fuzz.sh 10s: 30 targets, exit 0, no finding (second run, after the fixes)
```

The first fuzz run, while both agents ran tests, failed `FuzzRequire` once: loading a two-line script took longer than the target's 20 ms under that load ("script ran too long"); the input passed when run again. The target now skips a load that times out.

## Test engineer

Added 15 test files: the configured state limits on `state.profile`, `progress.export`, `Migrate` and in a ruleset world (`internal/script/profile_limits_test.go`, `internal/game/profile_limits_test.go`, `internal/profile/limits_test.go`, plus a profile changed during a pick); GM mode against permissions, questions, pause and role changes (`internal/game/gmmode_edge_test.go`); `world.finish` while paused and from a game-time timer, also through the store as `serve` wires it (`internal/game/clock_finish_test.go`, `cmd/talespeak/finish_state_test.go`); settings at the lowest state limits; timer tools while paused; reset codes following the members list and demoted GMs' sign-ins (`internal/account/members_reset_test.go`); the recheck closing exactly the changed sign-ins with the real `ws.Server` (`internal/account/recheck_test.go`); verify links opened signed in; outbox store failures, caps and logs without addresses (`internal/mail/outbox_edge_test.go`); `GameState` in its package; `math.random`'s bounds. It fixed `TestExtraTime`, which ignored a failing `CreateWorld`. Coverage: internal/mail 85.6 → 92.0 %, internal/profile 87.4 → 90.1 %, internal/game 95.3 → 95.8 %, internal/script 97.1 %, internal/account 90.6 %.

Its findings:

- **Low, fixed:** a hook waiting on a question kept the `player.gm` and `player:can` it started with after `/gm off`, while `state.game.set` followed the new mode. `/gm` now waits until the question is answered ("Answer the question first"), so all three always agree (`TestGMModeDuringQuestion`).
- **Low/medium, fixed:** a reset code a GM made kept working after the player was made a GM or admin. Codes are checked again when used (below).
- **Low, documented:** what a hook says right before `world.finish` may not reach players; only the `world.finish` text is certain. docs/lua-sandbox.md says to put the last words in the text.

## Security reviewer

No critical or high findings; four low, three info:

- **Low, fixed:** a room timer a player started that fell to a GM in GM mode (the starter left or had no Lua time) ran with the GM's rights: `player.gm`, engine permissions, `state.game` writes. Now it runs without them for anyone but its starter (`script.Hook.NoGM`; `TestRoomTimerFallsToGMWithoutRights`).
- **Low, fixed:** opening a `#verify=` link confirmed the address at once, so a mail scanner opening it could confirm a stranger's address for an attacker's account. The page now asks first (**confirm the address**; `e2e/tests/verify.spec.js`).
- **Low, fixed:** confirmation mails, which anyone may ask for, could fill the 1000-mail outbox while SMTP is down and block reset links. They take at most half now (`store.MaxVerifyOutbox`).
- **Low, fixed:** a sign-out between the upgrade's sign-in check and the connection being watched left it open until the next recheck. `watch` checks once more (`TestSignOutWhileOpening`).
- **Info, fixed:** a GM's reset code outlived the GM's right to make it: `codeStillAllowed` checks the maker again (`store.AccountByID`; `TestResetCodeCheckedWhenUsed`).
- **Info, fixed:** `player.gm` and `state.game` rights could disagree after `/gm` during a question (the test engineer's first finding).
- **Info, fixed:** `deploy/compose.yml` did not pass the `TALESPEAK_STATE_*` settings to the container.
- Checked and fine: role changes and the edu floor, reset tokens (random, hashed, single use, rate limited, one answer for every name), class code compared in constant time, mail headers and TLS, parameterised SQL, CSRF on every new POST, the Lua additions, the GM time commands' caps, no new origin in the client, no secrets, no new dependency. govulncheck was not run.

## Findings

- T-0010, T-0011, T-0012 and T-0013 moved to `tasks/done/`. T-0018 stays open: the chat ring and the edu filter on shared strings (need T-0006), inventories across worlds, a GM view of refused state writes, read-only shared data.
- Load flakes seen once each in full `-race` runs under load and passing alone: `TestManyPlayersWithTopLevelData`, `TestHomePinned`.
