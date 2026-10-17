# Story pack format

A story pack is a folder of YAML and Lua files. The engine ships one, `content/demo`; installing other packs comes with T-0010.

```text
pack.yaml
rooms/<any name>.yaml
scripts/<name>.lua
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
```

| Field | Rules |
| --- | --- |
| `id` | Like the pack id; unique in the pack |
| `title` | One line, 1 to 120 characters |
| `description` | 1 to 4000 bytes |
| `script` | A file name ending in `.lua`, read from `scripts/`; no folders. What it can do: [lua-sandbox.md](lua-sandbox.md) |

Loading is strict: unknown fields, more than one YAML document per file, files over 64 KiB, or scripts over 256 KiB are errors. Every problem is reported with its file, and a pack with errors does not start.
