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

- [ ] pack.yaml manifest format for ruleset and story packs, documented in docs/
- [ ] Tables: accounts, profiles, profile_progress, installed_packs
- [ ] Semver range check for ruleset compatibility, with a human-readable reason when locked
- [ ] Continuation check (required completed packs) and import/export of flags and items
- [ ] Ruleset major-version migration via Lua script, with a snapshot and undo
- [ ] Conversion packs for cross-ruleset moves (design only in v1)
- [ ] Several packs (and two versions of one pack) loaded as separate worlds in one process, lazy-loaded and unloaded when idle
- [ ] Measure memory per idle and per active world on a 1 GB box (protocol entry)
- [ ] Tests for compatible, incompatible and migration cases

## Notes

T-0034 (2026-10-18) added accounts, worlds (one per pack for now, `OpenWorld`), world members and one character per account and world in SQLite. Profiles bound to a ruleset, pack versions (`worlds.pack_version` is recorded empty until pack.yaml has one), several worlds per process and migrations are still open here.

T-0035 (2026-10-22): several worlds per process (`internal/hub`: load on the first player, unload when idle, `TALESPEAK_LOADED_WORLDS`, `TALESPEAK_WORLDS_PER_ADDRESS`), a pack catalog (`TALESPEAK_PACKS`) and worlds created from it in the lobby. The Lua time and memory budgets per address are still counted per world; a budget shared by all worlds would belong here or with the hub.

Design: "Accounts, profiles and pack compatibility" in the Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
