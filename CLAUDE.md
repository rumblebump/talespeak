# Talespeak: notes for Claude sessions

- Work on tasks with the steps in `.claude/skills/task-workflow/SKILL.md`: branch from `dev`, build, then run the `test-engineer` and `security-reviewer` agents (`.claude/agents/`) on every task, fix what they find, write a protocol entry, open the PR into `dev`.
- Engine: pure Go, `CGO_ENABLED=0`. Web client: plain JavaScript, Alpine.js (CSP build, vendored) later, own CSS. Offline first: nothing is ever loaded from another origin.
- Core vs Lua (Carl): the engine is only the glue between packs, assets, web and UI. Game mechanics (item rules, turns, death, endings) are written in Lua in packs or rulesets; the core only gains generic primitives (timers, persistence, UI messages) when Lua cannot do it.
- Never write secrets anywhere, including `protocol/` files: use `$VARIABLES`.
- Checks before pushing: `gofmt -l .`, `go vet ./...`, `go test -race -count=1 ./...`, `actionlint` when workflows changed.
- Design decisions: the Talespeak Engine Design doc (linked from the task files) and `docs/`.
