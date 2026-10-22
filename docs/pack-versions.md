# Pack versions

Every pack has a version in its `pack.yaml`, and a server can have several versions of one pack installed at once. A world remembers the version it runs, so updating a pack never breaks a world in the middle of a story: existing players finish on 1.x while new worlds start on 2.x.

```yaml
id: dragon-isle
version: 1.2.0
title: Dragon Isle
start: beach
```

## The version

A [semantic version](https://semver.org): `MAJOR.MINOR.PATCH`, like `1.2.0`, each number at most 999999999 and without leading zeros. An optional pre-release follows a `-`, like `2.0.0-beta.1`; it sorts before its release. Build metadata (`+build.5`) is not allowed. The field is required.

| Change | New version | What happens to running worlds |
| --- | --- | --- |
| A typo, a fixed script | `1.2.0` to `1.2.1` | They move to it when they load next |
| New rooms, new items, compatible state | `1.2.1` to `1.3.0` | They move to it when they load next |
| State or rooms the old worlds cannot use | `1.3.0` to `2.0.0` | They stay on 1.x; new worlds start on 2.0.0 |

Packs whose major version is 0 are treated as unstable: every minor version is a new line (`0.3.x` worlds never move to `0.4.0`), as in semver's caret ranges.

A newer version of the same line must read the `state.world` and `state.player` its older versions wrote: a script that changes the meaning of a key checks for the old form, or the pack takes a new major version.

## Which version a world runs

When a world loads, the server picks the version it runs from the installed versions of its pack:

1. Of the versions compatible with the one the world recorded (`^recorded`: that version or newer, with the same major version, or the same `0.minor` for 0.x), the newest release; a pre-release only when no release fits.
2. The world records that version. It never moves back: once a world ran 1.3.0, a server with only 1.2.0 refuses it ("This world needs another version of its pack than this server has.") instead of running state 1.3.0 wrote on 1.2.0. Installing 1.3.0 or a newer 1.x again makes it playable.
3. A world from before packs had versions takes the newest version.

New worlds start on the newest release of the pack, or its newest pre-release when it has no release. The lobby shows each world's version on its card, and a world no installed version can run as unavailable, with the reason. Admins and its game masters can still end it.

The server's main world runs the newest version of its pack (`TALESPEAK_PACK`, or the demo). When that is a new major version, the server starts a new main world on it; the former main world stays as a world of its own, playable while a version of its line is installed.

## Installing versions

Put each version in its own folder in `TALESPEAK_PACKS` (the folder names do not matter):

```text
packs/
  dragon-isle-1/      # pack.yaml: version 1.4.2
  dragon-isle-2/      # pack.yaml: version 2.0.0
```

Two folders with the same id and version stop the server at startup. To retire a major version, end its worlds in the lobby, then remove its folder.

Each version serves its own images at `/packs/<pack id>/<version>/assets/<file>`, so worlds on two versions never show each other's pictures.

## Changed files without a new version

The server keeps a hash of every file it reads from each installed version (table `installed_packs`). When a version's files change without a new version number, it logs a warning at startup:

```text
level=WARN msg="pack files changed without a new version: worlds run the new files, give each change its own version" pack=dragon-isle version=1.4.2
```

The worlds then run the changed files. Give every change its own version, so worlds and their players' state stay tied to the files that wrote it.

## Version ranges

The engine also reads version ranges, for packs that will name the rulesets and story packs they need (T-0010):

| Range | Means |
| --- | --- |
| `1.2.3` | exactly 1.2.3 |
| `^1.2` | 1.2.0 or newer, before 2.0.0 (`^0.2` before 0.3.0, `^0.0.3` only 0.0.3) |
| `~1.2` | 1.2.0 or newer, before 1.3.0 |
| `1.x`, `1` | any 1.x.x |
| `*` | any version |
| `>=1.0 <2.0` | every part must hold |
