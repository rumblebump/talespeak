# Rulesets, profiles and continuing stories

A **profile** is one of an account's characters. It is bound to a **ruleset** (an id and the version its sheet was last written with), and it carries its **sheet** (`state.profile` in Lua) from story to story on that ruleset. A **ruleset pack** holds the rules story packs share: Lua modules and the migrations of the sheet between major versions. A story pack names the ruleset it is for, the stories it continues and what carries over from them.

Packs without a ruleset work as before: their players need no profile, and `state.player` is all a character keeps.

## pack.yaml

A story on a ruleset:

```yaml
id: dragon-isle
version: 1.0.0
title: Dragon Isle
start: beach
kind: story                                   # the default
ruleset: { id: tiny-rules, version: "^1.0" }  # the ruleset and the versions the story works with
continues:                                    # the stories this one follows
  - { id: forest-start, version: ">=1.0", completed: true }
imports: [flags.rescued_fox]                  # what comes over from the stories in continues
exports: [flags.rescued_dragon, items.scale]  # what this story gives the stories after it
```

A ruleset pack:

```yaml
id: tiny-rules
version: 2.0.0
kind: ruleset
title: Tiny Rules
```

```text
pack.yaml
lib/<name>.lua          # modules every story on the ruleset can require
migrations/<major>.lua  # moves a sheet into this major version from the one before
```

| Field | Rules |
| --- | --- |
| `kind` | `story` (default) or `ruleset`. A ruleset pack has no `start`, no rooms, and none of the fields below; nobody plays it as a world, and the lobby does not offer it |
| `ruleset` | `id` of a ruleset pack and a `version` range ([pack-versions.md](pack-versions.md#version-ranges)). Worlds of the story run the newest installed ruleset version in the range. The server does not start when no installed ruleset fits a story |
| `continues` | At most 20: `id` of another story pack, `version` (a range, `*` when left out) and `completed`. With `completed: true` a profile may start this story only once it finished a version of that one in the range |
| `imports`, `exports` | At most 50 names each, `flags.<name>` or `items.<name>` (the name 1 to 58 of a-z, 0-9, `_` and `-`). `continues` and `exports` need a `ruleset`; `imports` need `continues` |
| `migrations/` | Only `<major>.lua` files, for majors from 2 to the pack's own; checked like room scripts (offline check included) |

The engine has no rules of its own about flags and items: they are names and values the stories agree on, and the Lua in the packs gives them their meaning (an item in `items.scale` is whatever the story's inventory code makes of it).

## Which profile may play which world

A world of a story on a ruleset runs one ruleset version: the newest installed one in the story's range. A profile **fits** when:

1. it is on the same ruleset id (a `dnd5e` character never enters a `tiny-rules` story);
2. its version can be read by the world's: the same major version (or the same `0.minor` line), and not newer. A profile on an older version of the line moves up to the world's version when it plays (the sheet stays as it is: a newer minor version reads what older ones wrote, as with packs);
3. or it is on an older major version (1 or later) and the world's ruleset pack has a migration into every major version from there: `2.lua`, `3.lua` and so on. It is migrated first (below);
4. and, for a profile that has not started this story yet, it finished every story in `continues` with `completed: true`.

The lobby card of such a world shows the ruleset and, before the account picked a character for it, its profiles on that ruleset with the reason any of them does not fit ("Hero plays Tiny Rules 2.0.0, and this world runs the older 1.0.0."). When nothing fits and no new profile may start the story, the card says why: **"Finish Forest Start first."** (the title of the newest installed version of that pack, or its id).

Picking (or making) a profile binds the account's character in that world to it for good: each world's character points to one profile. The first binding:

- creates the character when there is none, and starts the profile's progress of the story (`profile_progress`, status `playing`);
- copies the story's `imports` into the character's `state.player`, under the same names (`state.player.get("flags.rescued_fox")`): for each name, the value the first story in `continues` that the profile played (in its range) exported. Later changes to the profile's exports do not reach this world again.

A player whose character has no profile yet, or whose profile no longer fits (it moved on to a newer major version in another world), is sent back to the lobby when they connect: "Pick a character for this world in the lobby first." or "Your character has to be checked again: pick it for this world in the lobby."

An account has at most 20 profiles. A new profile's name is 1 to 20 ASCII letters, digits, spaces, `-` and `_`, starting with a letter, unique in the account in any letter case.

## Lua

| What | Does |
| --- | --- |
| `state.profile.get(key)`, `state.profile.set(key, value)` | The character's sheet: like `state.player`, with its limits (256 KiB, 500 keys, the 50 writes per hook shared with the other state calls), but it goes with the profile into every story on the ruleset. In a pack without a ruleset it is a script error |
| `progress.export(name, value)` | Keeps `value` as what this story exports under `name`, which must be in the pack's `exports` (else a script error); nil removes it. Values follow the rules of `state.player`; it counts as a write. Returns true, or nil and the reason |
| `progress.complete()` | Marks this story completed for the profile: stories that continue it with `completed: true` open to it. It cannot be undone by a script |
| `pack.id`, `pack.version` | The pack and the version the world runs |
| `pack.ruleset.id`, `pack.ruleset.version` | The ruleset and the version the world runs; `pack.ruleset` is nil without one |

A story's scripts `require` the ruleset's `lib/` modules like their own. A story module with the same name as a ruleset module wins, so a story can replace one; it cannot reach the ruleset's then.

The sheet, the exports and the completion are saved with the hook's other changes, in the same transaction. A profile played in two worlds at once sees the sheet as it was when the player joined each, and the last write of each key wins.

## Migrations, the snapshot and undo

A ruleset's `migrations/<major>.lua` defines `migrate(sheet)`: `sheet` is a table of the profile's `state.profile` keys and values, and `migrate` returns the new sheet (or changes `sheet` and returns nothing). It runs in a sandbox of its own, with the world's Lua limits and no player: `say`, `ui`, `state` and timers raise an error in it. The result must hold what `state.profile` can keep.

```lua
-- migrations/2.lua of tiny-rules: the level moves into a stats table.
function migrate(sheet)
  sheet.stats = { level = sheet.level or 1, courage = 1 }
  sheet.level = nil
  return sheet
end
```

When a player picks a profile of an older major version for a world on a newer one, the engine runs every migration from the profile's major version up, in order, on a copy of the sheet. Only when all of them succeed does it save, in one transaction: the old sheet and version as the profile's **snapshot** (one per profile: the newest migration's), and the new sheet at the world's ruleset version. A failed migration changes nothing; the player reads that the ruleset's migration failed, and the server logs why.

Once migrated, the profile plays worlds of the new major version only. A world that loaded the old sheet before the migration writes nothing more to it.

An admin brings the snapshot back:

```sh
docker compose exec talespeak /talespeak profile list ann     # the account's profiles: id, name, ruleset, version, and what undo goes back to
docker compose exec talespeak /talespeak profile undo 12      # profile 12 is on its old version and sheet again
```

Undo is for a migration that went wrong: install a ruleset version with a fixed migration, undo, and the next pick migrates again with it. What the profile did on the new version since the migration is lost. Every migration and undo also stops worlds that loaded the sheet before from writing to it.

## Conversion packs (design only)

Moving a character to **another** ruleset (a `dnd5e` hero into a `cyberpunk` story) is not built. The design: a conversion pack, `kind: conversion`, declares

```yaml
id: dnd-to-cyber
version: 1.0.0
kind: conversion
title: Through the portal
converts: { from: { id: dnd5e, version: "^5" }, to: { id: cyberpunk, version: "^2" } }
```

and carries `convert.lua` with `convert(sheet)`, run like a migration (sandbox, snapshot, undo). The lobby would offer it as a choice on a profile of the `from` ruleset: "Convert Hero to Cyberpunk", which makes a **new** profile on the `to` ruleset and leaves the old one as it is, so nothing is lost. Progress and exports stay with the old profile; a conversion may list `imports` like a story to carry flags over. Until then, a player makes a new profile for the other ruleset.

## Storage

| Table | Holds |
| --- | --- |
| `profiles` | account, name, ruleset id and version, a generation counted up by each migration and undo |
| `profile_state` | the sheet, in the encoding of `state.player` |
| `profile_snapshots`, `profile_snapshot_state` | the version and sheet before the last migration |
| `profile_progress` | per profile and story pack: the pack version last played, status `playing` or `completed` |
| `profile_flags` | per profile and story pack: the exported `flags.*` and `items.*` |
| `characters.profile_id` | the profile each world's character plays; NULL in worlds of packs without a ruleset |

Finishing a world keeps the profiles, their progress and exports: they are the account's, not the world's.

## Try it

`content/ruleset-test/` has a tiny ruleset in two major versions and three stories (`forest-start`, `dragon-isle` continuing it, `sky-tower` on 2.x): `TALESPEAK_PACKS=content/ruleset-test go run ./cmd/talespeak serve`. The browser tests play them (`e2e/tests/rulesets.spec.js`).
