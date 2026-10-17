---
id: T-0007
title: CI branch flow, scans, deploys and releases
type: chore
priority: high
created: 2026-10-17
depends: [T-0002]
---

## Goal

GitHub Actions run quick checks on dev, full checks on release PRs into main, deploy dev to the dev server and main to staging, and publish scanned multi-arch images and binaries on version tags cut from main.

## Acceptance

- [ ] ci.yml with path filters and an always-reported ci-ok check
- [ ] Secret scan on every push
- [x] Branch flow: dev (default) quick checks, PR into main full checks, tags from main release (workflows written)
- [x] Trivy fs and image scans, govulncheck, actionlint, Renovate config (written)
- [ ] dev is the default branch; branch protection on dev and main with ci-ok required
- [ ] Renovate GitHub App installed; first digest-pinning PR merged
- [ ] Dev deploy from dev and staging deploy from main over SSH, verified
- [ ] release.yml publishes ghcr.io image (amd64, arm64) and binaries, verified with a first tag
- [ ] Production deploy job turned on once the server exists

## Notes

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
