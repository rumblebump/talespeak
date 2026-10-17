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

- [ ] cmd/talespeak builds with CGO_ENABLED=0
- [ ] SQLite via ncruces/go-sqlite3 behind a Store interface
- [ ] /healthz endpoint for Caddy and Docker
- [ ] Unit tests for the store
- [ ] Web client shows a Source link to the running version's code (AGPL section 13)

## Notes

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
