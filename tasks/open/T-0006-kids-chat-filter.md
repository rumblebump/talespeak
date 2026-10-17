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

## Notes

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
