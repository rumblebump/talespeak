---
id: T-0039
title: TestRealClockWorldChanges is flaky under load
type: bug
priority: low
created: 2026-10-23
depends: []
---

## Goal

`TestRealClockWorldChanges` (`internal/game/push_edge_test.go`) passes on a loaded CI runner as reliably as on an idle machine.

## Acceptance

- [ ] The test no longer depends on a fixed 2 s real-time deadline per player, or the deadline is long enough under `-race -cover` with all packages in parallel
- [ ] Checked with `go test -race -count=20 -run TestRealClockWorldChanges ./internal/game` while the rest of the suite runs

## Notes

Found by the test engineer in the first full pass (protocol/2026-10-23-full-review.md): it failed once with "player N heard no change" during a loaded `go test -cover ./...`, and passed in 13 reruns. It uses real timers on purpose (on_change throttling); older than T-0038 (commit 108346b).
