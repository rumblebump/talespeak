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
| SVG | Not yet: an SVG file can carry scripts and links, so it waits for pack lint (T-0014) |

Small pixel art (up to 256 pixels wide) is scaled up sharp; larger pictures are scaled smoothly.

Loading is strict: unknown fields, more than one YAML document per file, files over 64 KiB, or scripts over 256 KiB are errors. Every problem is reported with its file, and a pack with errors does not start.
