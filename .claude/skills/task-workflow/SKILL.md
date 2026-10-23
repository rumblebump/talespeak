---
name: task-workflow
description: How to work on a Talespeak task from tasks/open to a pull request into dev, with the test-engineer and security-reviewer agents. Use whenever starting or finishing a task T-NNNN.
---

# Working on a task

Every task follows the same steps, so every change gets the same tests and the same security review.

1. **Start.** Branch from `dev` (`t-NNNN-short-name`). `git mv` the task file from `tasks/open/` to `tasks/doing/` and regenerate `tasks/BOARD.md` with `scripts/board.sh > tasks/BOARD.md`.
2. **Build.** Read `CODING.md` first and open only the files it points to. Implement the acceptance list. Keep the engine pure Go (`CGO_ENABLED=0`) and offline first (nothing loaded from another origin). Check new dependencies for license and size before adding them.
3. **Tests.** Run the `test-engineer` agent (`.claude/agents/test-engineer.md`) with the task id, the task file and the base `origin/dev`. It writes missing tests and reports bugs; fix the bugs it reports.
4. **Security review.** Run the `security-reviewer` agent (`.claude/agents/security-reviewer.md`) on the same diff, after the tests are in. Fix every critical, high and medium finding. For a low finding, fix it or note in the PR why not.
   Steps 3 and 4 can run at the same time when the code is stable.
5. **Docs and check.** Update `CODING.md` for every file added, removed, split or changed in purpose, and every doc the change makes wrong (`docs/`, README, task files). Then `gofmt -l .`, `go vet ./...`, `go test -race -count=1 ./...`, `scripts/check-codemap.sh`, `actionlint` when workflows changed.
6. **Protocol.** Write `protocol/YYYY-MM-DD-<slug>.md` with the exact commands and trimmed output, including what both agents found and what was fixed. Secrets only as `$VARIABLES`.
7. **Finish.** Tick the acceptance boxes. Move the task to `tasks/done/` only when all boxes are ticked; otherwise leave it in `doing/` and list what is left in its notes. Regenerate the board.
8. **Pull request** into `dev`, title with the task id. In the body, summarize the test and security results and link the protocol file.

If an agent cannot be started (no Agent tool), follow its file yourself, step by step, and say so in the PR.
