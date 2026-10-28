---
id: T-0010
title: Accounts, profiles and pack compatibility
type: feature
priority: high
created: 2026-10-17
depends: [T-0002, T-0003]
---

## Goal

An account holds several profiles; each profile is bound to a ruleset and version, and can move into story packs whose manifest it satisfies, carrying over only the declared imports.

## Acceptance

- [x] pack.yaml manifest format for ruleset and story packs, documented in docs/
- [x] Tables: accounts, profiles, profile_progress, installed_packs
- [x] Semver range check for ruleset compatibility, with a human-readable reason when locked
- [x] Continuation check (required completed packs) and import/export of flags and items
- [x] Ruleset major-version migration via Lua script, with a snapshot and undo
- [x] Conversion packs for cross-ruleset moves (design only in v1)
- [x] Several packs (and two versions of one pack) loaded as separate worlds in one process, lazy-loaded and unloaded when idle
- [x] Measure memory per idle and per active world on a 1 GB box (protocol entry)
- [x] Tests for compatible, incompatible and migration cases

## Notes

T-0034 (2026-10-18) added accounts, worlds (one per pack for now, `OpenWorld`), world members and one character per account and world in SQLite. Profiles bound to a ruleset, pack versions (`worlds.pack_version` is recorded empty until pack.yaml has one), several worlds per process and migrations are still open here.

T-0035 (2026-10-22): several worlds per process (`internal/hub`: load on the first player, unload when idle, `TALESPEAK_LOADED_WORLDS`, `TALESPEAK_WORLDS_PER_ADDRESS`), a pack catalog (`TALESPEAK_PACKS`) and worlds created from it in the lobby. The Lua time and memory budgets per address are still counted per world; a budget shared by all worlds would belong here or with the hub.

Design: "Accounts, profiles and pack compatibility" in the Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6

2026-10-22, pack versions (first slice, protocol: [2026-10-22-t-0010-pack-versions.md](../../protocol/2026-10-22-t-0010-pack-versions.md)): `version` is required in pack.yaml (semver, docs/pack-versions.md); `TALESPEAK_PACKS` holds several versions of one pack; a world runs the newest version compatible (`^recorded`) with the one it recorded, records it, and never moves back; new worlds and the main world start on the newest release; a new major version of the main pack starts a new main world. Images are served per version. `installed_packs` keeps a hash of each version's files and the server warns when files change without a new version. `internal/pack` reads semver versions and ranges (`^ ~ x * >= > <= <`), which the ruleset and continuation checks below will use.

Then still open (done below, 2026-10-27): `kind`/`ruleset`/`continues`/`imports`/`exports` in pack.yaml and their docs, profiles and `profile_progress`, the ruleset compatibility check with its reason for a profile, imports and exports, ruleset migrations with snapshot and undo, conversion packs (design), memory measurement on a 1 GB box, and one Lua budget per address across worlds. Lua does not see its pack's version yet (`pack.version`, and the version a world ran before, would let a pack migrate state within a major version).

2026-10-27, rulesets and profiles (design calls 13 and 14; docs/rulesets.md):

- pack.yaml: `kind` (story | ruleset), `ruleset` {id, version range}, `continues` [{id, version, completed}], `imports`/`exports` (`flags.<name>`, `items.<name>`), in `internal/pack/ruleset.go` (pack.go only gains the inline `Manifest` and two check calls, to keep clear of T-0018's `settings:`). Ruleset packs carry `lib/` and `migrations/<major>.lua`; story modules shadow ruleset modules of the same name. The catalog picks the newest installed ruleset in a story's range; the server does not start when a story has none.
- Store (migration 0010): `profiles` (with a generation), `profile_state`, `profile_snapshots`/`profile_snapshot_state`, `profile_progress`, `profile_flags`, `characters.profile_id`. A save writes the profile only while its generation matches, so a world that loaded the sheet before a migration or undo writes nothing more to it.
- Fit (`internal/profile/check.go`): same ruleset id; the profile's version within `^profile` of the world's ruleset version and not newer (moves up within the line); an older major moves up when the ruleset has every migration from there; a profile new to the story must have completed each `completed: true` continuation. Reasons for the lobby: "Finish Forest Start first.", "X plays Tiny Rules 2.0.0, and this world runs the older 1.0.0.", "Nothing moves X from ... to ...".
- Lobby: the card's `profiles`, `POST /api/profiles` (pick or make, 20 at once then one every 3 s per account), `web/static/profiles.js`. Joining a ruleset world without a fitting profile is refused with a sentence that sends the player to the lobby.
- Lua: `state.profile.get/set` (state.player's limits), `progress.export(name, value)`, `progress.complete()`, `pack.id`, `pack.version`, `pack.ruleset.{id,version}`. Imports are copied into `state.player` under the same names on the first binding.
- Migrations: run in a fresh sandbox (`script.Migrate`) on a copy; on success one transaction keeps the snapshot and writes the new sheet. `talespeak profile list NAME` and `talespeak profile undo ID` restore the snapshot.
- Conversion packs: design only, in docs/rulesets.md.
- One Lua budget per address across worlds: left open. The time budget could be shared cheaply, but the memory budget measures each world's Lua state by walking it under that world's lock; summing across worlds needs cross-world locking or stale numbers. `TALESPEAK_WORLDS_PER_ADDRESS` (4 plus the main world) already bounds what one address holds.
- Memory (`go test ./internal/game -run XXX -bench WorldMemory -benchtime 3x` with `GOMEMLIMIT=1GiB`, 20 worlds each): demo pack about 2.47 MB per idle world, 2.51 MB with one player; clockmaker about 3.85 MB idle, 3.93 MB with one player. So the default 20 loaded worlds need well under 100 MB besides what players keep. The protocol entry with these numbers is for the merging session.
- Known gap: a leaving player's profile changes whose last save fails are not kept for a retry (state.player's are).
- `TestEmbeddedMigrationsAreNumberedInOrder` fails on this branch alone, since 0007 to 0009 come from other tasks; it passes once those are merged.
