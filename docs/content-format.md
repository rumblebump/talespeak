# Story pack format

A story pack is a folder of YAML and Lua files. The engine ships one, `content/demo`; admins install others with `TALESPEAK_PACKS` ([accounts.md](accounts.md)).

```text
pack.yaml
rooms/<any name>.yaml
scripts/<name>.lua
lib/<name>.lua          # optional modules every room script can require
assets/<name>.png       # optional images: .png, .jpg, .jpeg, .gif, .webp
```

A ruleset pack (`kind: ruleset`) has `pack.yaml`, `lib/` and `migrations/<major>.lua` only: [rulesets.md](rulesets.md).

## pack.yaml

```yaml
id: demo              # 1 to 64 of a-z, 0-9, _ and -
version: 1.0.0        # a semantic version, MAJOR.MINOR.PATCH: see pack-versions.md
title: The damp cell  # one line, 1 to 120 characters
description: >-       # optional: what the lobby shows about the pack, at most 300 bytes
  Wake up in a cell and talk, trick or puzzle your way out.
start: cell           # the room new players enter
settings:            # optional: choices each world makes, at most 20
  - name: death       # the key in state.game: 1 to 32 of a-z, 0-9 and _, starting with a letter, unique
    label: When the guard catches a player   # what the lobby and /set show: one line, 1 to 100 bytes
    choices: [respawn, revive, stay]         # 2 to 20 unique words of 1 to 32 of a-z, 0-9, _ and -
    default: respawn  # optional: one of the choices; the first choice when left out
```

A story on a ruleset also names it, the stories it continues and what carries over: `kind`, `ruleset`, `continues`, `imports` and `exports`, in [rulesets.md](rulesets.md#packyaml). A pack without them works as before, with no characters across stories.

**Settings** are what each world of the pack decides for itself, such as what happens when a player dies. Whoever creates a world picks them in the lobby's New world form (each defaults to its `default`); the world's game master changes one while it runs with `/set NAME VALUE` in GM mode ([gm-tools.md](gm-tools.md)). Scripts read a setting with `state.game.get(NAME)`, which always holds one of its choices: a world that has none (created before the pack had the setting, or the server's main world) gets the default when it loads, and `state.game.set` refuses any other value ([lua-sandbox.md](lua-sandbox.md)). The demo's `death` setting drives `lib/death.lua` ([pack-patterns.md](pack-patterns.md#death-and-save-points)).

## rooms/*.yaml

One room per file.

```yaml
id: cell
title: A damp cell
description: >-
  Stone walls, a heap of straw and a narrow window far above you.
script: cell.lua      # optional, a file in scripts/
items:                # optional, what the player can click on entering
  - id: straw
    label: heap of straw
```

| Field | Rules |
| --- | --- |
| `id` | Like the pack id; unique in the pack |
| `title` | One line, 1 to 120 characters |
| `description` | 1 to 4000 bytes |
| `script` | A file name ending in `.lua`, read from `scripts/`; no folders. What it can do: [lua-sandbox.md](lua-sandbox.md) |
| `items` | At most 20; each with an `id` like a room id, unique in the room, and a one-line `label` of 1 to 100 bytes. A click runs the script's `on_click`; the script can change them with `ui.items` |

## lib/

Lua modules the room scripts share, so a helper is written once instead of copied into every room. `require("rooms")` in any script returns what `lib/rooms.lua` returns. A story on a ruleset can also require the ruleset pack's modules; its own module of the same name wins.

| Rule | |
| --- | --- |
| Names | 1 to 64 of a-z, 0-9, `_` and `-`, then `.lua`; plain files only (no links or folders), and nothing else in `lib/` |
| Size | At most 256 KiB each, and at most 100 modules |
| Checks | The same as for room scripts, including the offline check below |

How `require` behaves: [lua-sandbox.md](lua-sandbox.md).

## assets/

Images a script shows with `ui.image`. The engine serves them itself at `/packs/<pack id>/<version>/assets/<file>`, so a pack never needs another server.

| Rule | |
| --- | --- |
| Names | 1 to 64 of a-z, 0-9, `_` and `-`, then `.png`, `.jpg`, `.jpeg`, `.gif` or `.webp`; no folders |
| Content | Must match the extension (checked from the file's bytes) |
| Size | At most 2 MiB each and 64 MiB per pack by default; admins can change both ([admin-limits.md](admin-limits.md)) |
| SVG | Not yet: an SVG file can carry scripts and links, so it needs its own checks first |

Small pixel art (up to 256 pixels wide) is scaled up sharp; larger pictures are scaled smoothly.

## Offline: no external references

A pack must work without the internet, so it carries every file it uses and points nowhere else. Loading refuses:

- a URL with a scheme (`https://`, `http://`, `ws://`, `ftp://` and the like, also `https:host` without slashes), a `javascript:` or `vbscript:` link, a `//host` reference (also inside `url(...)`) or a bare `www.` host in any YAML value or key, or in any Lua string literal, reported with file and line. Text is read the way a browser reads a URL, so `\\host`, `https:\\host` and references broken by a tab or line break count too. A `//` only counts when a host follows it (a name with a dot and a letter top-level domain, a port, an IPv4 or IPv6 address, or `localhost`), so story text like `//INCOMING TRANSMISSION//` is fine;
- YAML tags (`!!binary`, `!!str`, `!custom`): the format needs none, and `!!binary` would hide text from the check;
- `.css`, `.svg`, `.svgz`, `.html`, `.htm`, `.shtml`, `.xhtml`, `.xht`, `.xml`, `.xsl`, `.js`, `.mjs`, `.cjs` and `.wasm` files anywhere in the pack: they are not part of the format, and a browser could run them or load more from them. Hidden folders (`.git`) are skipped, because the engine never reads them.

YAML and Lua comments are not checked, and the engine never reads other files (a `README.md` or `LICENSE`), so credits and links for authors go there. A string built at runtime (`"https:" .. "//x"`) is caught only when one literal holds the reference; nor are rarer forms: percent-encoded or non-ASCII host names, numeric IP addresses like `//3232235777`, and names without a dot like `//intranet/x`. For all of them, the browser's Content-Security-Policy still blocks every load from another origin, and the client shows pack text only as text, never in a URL attribute.

Loading is strict: unknown fields, more than one YAML document per file, files over 64 KiB, or scripts over 256 KiB are errors. Every problem is reported with its file, and a pack with errors does not start.
