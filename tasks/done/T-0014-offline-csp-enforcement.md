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

- [x] Engine sends the same CSP and security headers as deploy/Caddyfile, so servers without Caddy are protected
- [x] No CORS headers; Origin check on WebSocket upgrades (same origin only); optional `TALESPEAK_HOSTS` answers 421 to other Host names
- [x] Pack lint rejects http://, https://, //host and external url() in YAML, Lua, CSS, SVG and HTML, listing file and line. CSS, SVG and HTML are not part of the pack format yet, so a pack carrying them is refused; when they are added, lint must cover their url(), src and href
- [x] Lua sandbox has no network access (a test pins the exact sandbox surface and that the script package links no net code)
- [x] CI scan fails on external URLs in web/
- [x] Playwright guard: tests fail on any cross-origin request or CSP violation

## Notes

Protocol: protocol/2026-10-18-t-0014-offline-csp.md.

Section "Offline first, strict CSP" in the design doc. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
