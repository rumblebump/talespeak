---
id: T-0038
title: Code map (CODING.md) and source files under 500 lines
type: chore
priority: medium
created: 2026-10-23
depends: [T-0011]
---

## Goal

A session reads `CODING.md` instead of the whole code: it maps every source file, says where to change what, and holds the style guide. Source files stay under 500 lines, split by function, so changes stay small.

## Acceptance

- [x] `CODING.md`: request path, one line per source file, where to change what, rules that span files, test layout, style guide
- [x] `scripts/check-codemap.sh` with its test, CI job `codemap` (needed by `ci-ok`): every source file mapped, no dead paths, at most 600 lines (tests 800) unless `scripts/size-allow.txt` allows it
- [x] CLAUDE.md, CONTRIBUTING.md, the task-workflow skill and docs/ci-cd.md point to it
- [ ] Map updated for the GM tools work (T-0011) once it is merged
- [ ] Split, moves only, one commit per file: `internal/game/game.go`, `web/static/app.js`, `internal/script/script.go`, `internal/script/sandbox.go`, `internal/account/account.go`, `content/clockmaker/lib/case.lua`, `internal/game/demo_test.go`, `internal/ws/ws_test.go` (and `ws.go`, `hub.go` if T-0011 grew them past 500)
- [ ] `scripts/size-allow.txt` is empty
- [ ] After the splits: both agents run again over every split file (Carl, 2026-10-23: moves can bring in bugs), as the first full pass of CODING.md, logged as `protocol/YYYY-MM-DD-full-review.md`

## Notes

Carl, 2026-10-23: wait until the GM tools thread (T-0011) has merged before splitting or merging any of this. Plan with the split of each file: the "Code map and file splitting" doc linked from the project thread. CODING.md maps files, not every function: a full function list goes stale and makes parallel PRs conflict; `grep -n '^func'` gives it live.
