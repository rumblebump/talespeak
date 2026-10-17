---
id: T-0016
title: Public, invite-only and secret worlds with player limits
type: feature
priority: medium
created: 2026-10-17
depends: [T-0011]
---

## Goal

In public mode a GM chooses whether a world is listed publicly, invite only, or secret with a join code, and sets the maximum number of players.

## Acceptance

- [ ] Visibility public / invite only / secret per world, changeable by its GM
- [ ] Lobby lists public worlds with seats taken and the limit; secret worlds never appear in lists or search
- [ ] Player limit (GMs do not count); full worlds refuse joins
- [ ] Join codes: random, stored as a hash, GM can replace or expire them; wrong guesses rate-limited per account and IP
- [ ] Edu mode: worlds are always invite only; listings and codes are off
- [ ] Tests: listing, full world, wrong and expired codes, edu restrictions

## Notes

"World visibility and size" under "Roles and server modes" in the design doc. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
