# Talespeak

*Motivating for language and dungeons.*

Talespeak ("tales peak") is a small, self-hosted, programmable multiplayer text adventure engine (a MUD) for learning languages through DnD-style play. It runs on any CPU, with no GPU needed.

Go runs the server, storage, Lua runtime and optional AI glue. Stories, rules and glossaries are YAML and Lua files. The web client looks like a terminal, with clickable widgets.

## Guides

| Guide | For |
| --- | --- |
| [docs/setup-server.md](docs/setup-server.md) | Running your own server (Docker, Caddy, automatic HTTPS) |
| [docs/setup-dev.md](docs/setup-dev.md) | Local development and the shared dev server |
| [docs/ci-cd.md](docs/ci-cd.md) | What CI runs, images, releases |
| [tasks/README.md](tasks/README.md) | How work is tracked ([board](tasks/BOARD.md)) |
| [protocol/README.md](protocol/README.md) | How tests and checks are logged |

## License

The engine is licensed under the [GNU AGPL v3 or later](LICENSE) with a [content exception](LICENSE-EXCEPTION.md): your own stories, rule packs and assets can use any license, including commercial ones. See [docs/licensing.md](docs/licensing.md) for a plain-language summary.

## Status

Early design. See the [board](tasks/BOARD.md).
