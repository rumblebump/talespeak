---
id: T-0014
title: Offline first and strict CSP enforcement
type: feature
priority: high
created: 2026-10-17
depends: [T-0002]
---

## Goal

The server and client never need the internet, the browser never loads anything from another origin, and packs with external references are rejected.

## Acceptance

- [ ] Engine sends the same CSP and security headers as deploy/Caddyfile, so servers without Caddy are protected
- [ ] No CORS headers; Origin check on WebSocket upgrades
- [ ] Pack lint rejects http://, https://, //host and external url() in YAML, Lua, CSS, SVG and HTML, listing file and line
- [ ] Lua sandbox has no network access
- [ ] CI scan fails on external URLs in web/
- [ ] Playwright guard: tests fail on any cross-origin request or CSP violation

## Notes

Section "Offline first, strict CSP" in the design doc. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
