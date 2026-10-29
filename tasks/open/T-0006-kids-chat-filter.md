---
id: T-0006
title: Name filter for kids (no chat to filter)
type: feature
priority: high
created: 2026-10-17
depends: [T-0002]
---

## Goal

There is no chat between players (Carl 2026-10-29, T-0046): players talk next to each other or elsewhere, typed text only answers the story, and the engine refuses shared text with words a player typed. What people still type that others see is names: account names, world names, character names. Those pass a filter on a kids server before anyone sees them.

## Acceptance

- [ ] go-away word list plus editable YAML list, for account, world and character names
- [ ] Phone, email, address and link blocking in those names
- [ ] Test set of allowed and blocked names in CI
- [ ] Maybe: the enguard tiny-guard classifier via the Model2Vec code from T-0005, if word lists are not enough for names

Dropped with chat (T-0046): filtering chat messages, the edu floor for chat (GMs see all chat, no private chat, the notice that GMs read chat), the GM transcript. Game masters' `/say` and `/sayroom` are audited and need no filter.

## Notes

T-0035 (2026-10-22): world names typed in the lobby are free text (at most 40 characters; control, direction and blank-looking characters and stacked accents removed) and every signed-in account sees the names of public worlds and their creators. The filter here must cover them; until then a kids server should run in edu mode, where worlds are listed only to their members. `cleanTitle` also drops the zero-width joiner, so emoji sequences split; revisit with the filter.

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
