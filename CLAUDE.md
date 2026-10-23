# Talespeak: notes for Claude sessions

- Read `CODING.md` before reading code: it maps every source file, says where to change what, and holds the style guide. Then open only the files it points to.
- When a change adds, removes, renames or splits a file, or changes what one does, update its line in `CODING.md` in the same PR. Keep source files under 500 lines, split by function (`scripts/check-codemap.sh` fails at 600, tests at 800).
- Update every doc a change makes wrong (`docs/`, `CODING.md`, README, task files) in the same PR. Write what you learned down in short form (CODING.md, docs, task notes) so the next session need not re-read the code.
- Work on tasks with the steps in `.claude/skills/task-workflow/SKILL.md`: branch from `dev`, build, then run the `test-engineer` and `security-reviewer` agents (`.claude/agents/`) on every task, fix what they find, write a protocol entry, open the PR into `dev`.
- Engine: pure Go, `CGO_ENABLED=0`. Web client: plain JavaScript, Alpine.js (CSP build, vendored) later, own CSS. Offline first: nothing is ever loaded from another origin.
- Core vs Lua (Carl): the engine is only the glue between packs, assets, web and UI. Game mechanics (item rules, turns, death, endings) are written in Lua in packs or rulesets; the core only gains generic primitives (timers, persistence, UI messages) when Lua cannot do it.
- Never write secrets anywhere, including `protocol/` files: use `$VARIABLES`.
- Checks before pushing: `gofmt -l .`, `go vet ./...`, `go test -race -count=1 ./...`, `scripts/check-codemap.sh`, `actionlint` when workflows changed.
- Design decisions: the Talespeak Engine Design doc (linked from the task files) and `docs/`.
