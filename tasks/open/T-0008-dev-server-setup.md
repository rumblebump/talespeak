---
id: T-0008
title: Set up the dev and staging servers
type: chore
priority: medium
created: 2026-10-17
depends: [T-0007]
---

## Goal

The dev server runs the edge image (branch dev) and the public staging server runs the main image (branch main), both behind Caddy with valid certificates, and both receive deploys from GitHub Actions. Staging may break.

## Acceptance

- [ ] Follows docs/setup-dev.md exactly; guide fixed where it was wrong
- [ ] GitHub environments dev and staging with DEPLOY_SSH_* secrets; DEV_DEPLOY and STAGING_DEPLOY set
- [ ] Staging VPS sized about 4 vCPU, 8 GB RAM, 80 GB SSD with the AI sidecar
- [ ] Protocol entry with the commands used (no secrets)

## Notes

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
