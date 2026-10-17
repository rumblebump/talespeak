---
id: T-0015
title: Test harness for Go and Playwright
type: chore
priority: high
created: 2026-10-17
depends: [T-0002, T-0004]
---

## Goal

Every later feature can add tests cheaply: Go unit, integration, fuzz and blackbox tests, Lua pack tests, and Playwright tests against a real server.

## Acceptance

- [ ] Clock interface and fake clock; local fakes for the LLM sidecar and SMTP
- [ ] `-short` split between quick and full tests; golden file helper with `-update`
- [ ] `talespeak test content/<pack>` runs `tests/*_test.lua`
- [ ] content/test-pack (two rooms, a dialog, a timer, a soundboard)
- [ ] e2e/ with @playwright/test (plain JS), server started from a fresh data directory
- [ ] Offline and CSP guard fixture used by every browser test
- [ ] axe-core accessibility smoke test
- [ ] CI jobs go and e2e green with real tests

## Notes

Plan: docs/testing.md. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
