---
id: T-0006
title: Kids mode chat filter
type: feature
priority: high
created: 2026-10-17
depends: [T-0002]
---

## Goal

Free-text chat in kids mode passes normalize, word list, personal-info block and the enguard classifier before anyone sees it.

## Acceptance

- [ ] go-away word list plus editable YAML list
- [ ] Phone, email, address and link blocking
- [ ] enguard tiny-guard classifier via the Model2Vec code from T-0005
- [ ] Test set of allowed and blocked messages in CI
- [ ] Edu floor for chat, from T-0011: GMs see all chat in their world, kids open no private chat outside it, and players see a notice that GMs can read chat; covered by tests next to `internal/account/edu_floor_test.go`

## Notes

T-0035 (2026-10-22): world names typed in the lobby are free text (at most 40 characters; control, direction and blank-looking characters and stacked accents removed) and every signed-in account sees the names of public worlds and their creators. The filter here must cover them; until then a kids server should run in edu mode, where worlds are listed only to their members. `cleanTitle` also drops the zero-width joiner, so emoji sequences split; revisit with the filter.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
