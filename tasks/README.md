# Tasks

All work is tracked as plain Markdown files in this folder, so planning lives in git next to the code.

## Status is the folder

| Folder | Meaning |
| --- | --- |
| `open/` | Not started |
| `doing/` | In progress (keep this short) |
| `done/` | Finished; kept for history |

Changing status = `git mv tasks/open/T-0007-*.md tasks/doing/`. The git log of a task file is its history.

## One file per task

File name: `T-<4-digit id>-<short-slug>.md`, for example `T-0005-whisp-prototype.md`.
Copy `_template.md` to start a new one and take the next free id (`scripts/board.sh --next-id`).

Front matter fields:

| Field | Values |
| --- | --- |
| `id` | `T-0001`, … (never reused) |
| `title` | Short, plain words |
| `type` | `feature`, `bug`, `chore`, `docs`, `research` |
| `priority` | `high`, `medium`, `low` |
| `created` | `YYYY-MM-DD` |
| `depends` | List of task ids, or `[]` |

Bugs use the same file format with `type: bug` and a **Steps to reproduce** section.

## Board

`tasks/BOARD.md` is generated, never edited by hand:

```sh
scripts/board.sh > tasks/BOARD.md
```

## Rules

- A task in `doing/` links the protocol file(s) for any testing done (see `../protocol/README.md`).
- A task moves to `done/` only when every acceptance box is ticked.
- Commits that work on a task mention its id, for example `T-0005: add phonetic matcher`.
