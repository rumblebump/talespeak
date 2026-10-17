---
id: T-0002
title: Engine skeleton: HTTP, WebSocket, storage interface
type: feature
priority: high
created: 2026-10-17
depends: [T-0001]
---

## Goal

A Go binary serves the embedded web client, accepts WebSocket connections and stores state in SQLite behind a Store interface that Postgres can implement later.

## Acceptance

- [x] cmd/talespeak builds with CGO_ENABLED=0 (placeholder page, 2026-10-17)
- [x] SQLite via ncruces/go-sqlite3 behind a Store interface (WAL, embedded migrations)
- [x] /healthz endpoint for Caddy and Docker, `talespeak healthcheck` for the image
- [x] Unit tests for the store
- [x] Web client shows a Source link to the running version's code (AGPL section 13)

## Notes

WebSocket at /ws (coder/websocket, ISC): same-origin only, 16 KiB message limit, 2 min idle timeout, welcome/ping/pong; protocol in docs/websocket-protocol.md. /healthz pings the store. Reviewed with the test-engineer and security-reviewer agents (see protocol).

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
