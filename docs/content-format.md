# Story pack format

A story pack is a folder of YAML and Lua files. The engine ships one, `content/demo`; installing other packs comes with T-0010.

```text
pack.yaml
rooms/<any name>.yaml
scripts/<name>.lua
assets/<name>.png       # optional images: .png, .jpg, .jpeg, .gif, .webp
```

## pack.yaml

```yaml
id: demo              # 1 to 64 of a-z, 0-9, _ and -
title: The damp cell  # one line, 1 to 120 characters
start: cell           # the room new players enter
```

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

## assets/

Images a script shows with `ui.image`. The engine serves them itself at `/packs/<pack id>/assets/<file>`, so a pack never needs another server.

| Rule | |
| --- | --- |
| Names | 1 to 64 of a-z, 0-9, `_` and `-`, then `.png`, `.jpg`, `.jpeg`, `.gif` or `.webp`; no folders |
| Content | Must match the extension (checked from the file's bytes) |
| Size | At most 2 MiB each and 64 MiB per pack by default; admins can change both ([admin-limits.md](admin-limits.md)) |
| SVG | Not yet: an SVG file can carry scripts and links, so it needs its own checks first |

Small pixel art (up to 256 pixels wide) is scaled up sharp; larger pictures are scaled smoothly.

## Offline: no external references

A pack must work without the internet, so it carries every file it uses and points nowhere else. Loading refuses:

- a URL with a scheme (`https://`, `http://`, `ws://`, `ftp://` and the like, also `https:host` without slashes), a `javascript:` or `vbscript:` link, a `//host` reference (also inside `url(...)`) or a bare `www.` host in any YAML value or key, or in any Lua string literal, reported with file and line. Text is read the way a browser reads a URL, so `\\host`, `https:\\host` and references broken by a tab or line break count too. A `//` only counts when a host follows it (a name with a dot, a port, an IP address or `localhost`), so story text like `//INCOMING TRANSMISSION//` is fine;
- YAML tags (`!!binary`, `!!str`, `!custom`): the format needs none, and `!!binary` would hide text from the check;
- `.css`, `.svg`, `.svgz`, `.html`, `.htm`, `.shtml`, `.xhtml`, `.xht`, `.xml`, `.xsl`, `.js`, `.mjs`, `.cjs` and `.wasm` files anywhere in the pack: they are not part of the format, and a browser could run them or load more from them. Hidden folders (`.git`) are skipped, because the engine never reads them.

YAML and Lua comments are not checked, and the engine never reads other files (a `README.md` or `LICENSE`), so credits and links for authors go there. A string built at runtime (`"https:" .. "//x"`) is caught only when one literal holds the reference; the browser's Content-Security-Policy still blocks every load from another origin, and the client shows script text only as text.

Loading is strict: unknown fields, more than one YAML document per file, files over 64 KiB, or scripts over 256 KiB are errors. Every problem is reported with its file, and a pack with errors does not start.
