---
id: T-0007
title: CI, image builds, dev deploy and releases
type: chore
priority: high
created: 2026-10-17
depends: [T-0002]
---

## Goal

GitHub Actions run only the jobs a change needs, deploy main to the dev server, and publish multi-arch images and binaries on version tags.

## Acceptance

- [ ] ci.yml with path filters and an always-reported ci-ok check
- [ ] Secret scan on every push
- [ ] Dev deploy over SSH after green CI on main
- [ ] release.yml publishes ghcr.io image (amd64, arm64) and binaries

## Notes

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
