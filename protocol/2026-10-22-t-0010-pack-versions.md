# 2026-10-22: Pack versions (T-0010, first slice)

**Result:** packs carry a semver version; several versions of one pack run side by side, each world on the newest version compatible with the one it recorded. All checks green.
**Task:** T-0010
**Environment:** local container, Go 1.26.10, branch `claude/project-thread-mocahp` on top of dev `f89b949`; Playwright with Chromium (desktop and phone projects).

## Steps

```sh
# format, vet, all Go tests with the race detector
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

```text
(gofmt: no files)
ok  	github.com/rumblebump/talespeak/cmd/talespeak	8.718s
ok  	github.com/rumblebump/talespeak/content	1.256s
ok  	github.com/rumblebump/talespeak/internal/account	21.390s
ok  	github.com/rumblebump/talespeak/internal/clientip	1.019s
ok  	github.com/rumblebump/talespeak/internal/game	50.405s
ok  	github.com/rumblebump/talespeak/internal/hub	3.457s
ok  	github.com/rumblebump/talespeak/internal/pack	3.665s
ok  	github.com/rumblebump/talespeak/internal/script	14.725s
ok  	github.com/rumblebump/talespeak/internal/server	1.099s
ok  	github.com/rumblebump/talespeak/internal/store/sqlite	24.249s
ok  	github.com/rumblebump/talespeak/internal/ws	2.632s
ok  	github.com/rumblebump/talespeak/scripts	3.663s
```

```sh
# the new fuzz target: version and range parsing never panics, a parsed
# version prints as its text and is compatible with itself
go test -run '^$' -fuzz FuzzVersions -fuzztime 20s ./internal/pack/
```

```text
fuzz: elapsed: 20s, execs: 633931 (29074/sec), new interesting: 259 (total: 267)
PASS
```

The first fuzz idea found nothing, but writing it showed that 18446744073709551615.0.0 would overflow the next major version (Compatible would then hold nothing); version numbers are now capped at 999999999.

```sh
# browser tests against the built binary
./scripts/check-web.sh
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e && npm ci && npx playwright test
```

```text
ok alpine-csp 3.17.4 web/static/vendor/alpine-csp.js
first run: 8 failed (the lobby's pack picker now reads "Test pack (1.0.0)"; image URLs carry the version), 79 passed
after updating the two expectations: tests/lobby.spec.js and tests/demo.spec.js 12 passed; the rest passed in the first run
```

## What the new tests cover

- `internal/pack`: versions (valid, invalid, semver.org precedence order), ranges (`^ ~ x * = >= > <= <`, 0.x carets, pre-releases, combined), `Compatible`; the catalog's order (releases before pre-releases), `Newest`, `Pick` (empty, compatible, gone, never back, 0.x lines, unparsable, not installed), `Match`, twin versions refused; `version` required and checked in pack.yaml; the files hash (stable, ignores unread files, changes with each read file and with a rename); links still refused through the hashing file system; images served per version.
- `internal/hub`: worlds move to the newest compatible version and record it; a pre-release world stays on its line; two major versions loaded side by side; a world no version can run is refused with its message; after an update and a downgrade a world is refused rather than moving back; a store that cannot record the version keeps the world unloaded.
- `internal/store/sqlite`: `RecordPacks` reports a version whose files changed; `OpenWorld` with `keep` skips main worlds of another major version and starts a new one; `SetPackVersion`.
- `cmd/talespeak`: several versions in `TALESPEAK_PACKS`, the lobby's newest version per pack, the main world kept on a minor update and replaced on a major one, the old main world still running on its line.
- `internal/account`: cards show the version, a world no version can run is unavailable with the reason and cannot be played, a new world records the version it starts on.

## Review

Carl waived the test-engineer and security-reviewer agents for this task (2026-10-22, in the project thread), so neither ran. Things checked by hand:

- Pack files are still read only as regular files: `hashFS` implements `Lstat` and `ReadLink`, so `fs.Lstat` does not fall back to a link-following `Stat` (test `TestPackHashKeepsLinksOut`).
- The version in an image URL comes from a parsed pack, never from the request: the handler looks up `id/version` in a map of loaded packs. The web client's image check accepts only `/packs/<id>/<MAJOR.MINOR.PATCH[-pre]>/assets/<file>`.
- A world's recorded version is written before it loads a newer one, so a later downgrade cannot run newer state on older scripts.
- Versions and ranges are bounded (64 and 128 bytes, numbers at most 999999999) and fuzzed.

## Findings

- Follow-ups stay in T-0010: ruleset and story manifests (`kind`, `ruleset`, `continues`, imports/exports), profiles, migrations with snapshot and undo, memory measurement, one Lua budget per address across worlds, and `pack.version` for Lua.
