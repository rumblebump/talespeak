---
id: T-0040
title: Pack archives, admin upload and a hashed asset store
type: feature
priority: medium
created: 2026-10-23
depends: []
---

## Goal

A pack can be installed from one file (`.talespak`, or `.easytalespak` from the pack editor) by an admin, its assets are stored once by content hash, and every import is checked against the server's pack limits, which the editor can read. Core glue only (Go); no game mechanics. Part 1 of the pack editor plan (Carl 2026-10-23, see Notes).

## Acceptance

- [ ] `.talespak` and `.easytalespak` are both a zip of the pack folder (today's format); `.easytalespak` also carries `editor/`, which the engine ignores. Documented in docs/content-format.md
- [ ] Loading an archive is safe: no paths outside the pack (`..`, absolute, links), a cap on entries and on unpacked size (zip bombs), then the same `pack.Load` checks (strict YAML, offline lint)
- [ ] Assets in their own folder, each stored under its content hash (sha256); a `files.meta` maps file name to hash, size and type, so a file used twice (or by two versions or packs) is stored once
- [ ] Allowed asset types: png, jpg, gif and webp (images; gif for animations), mp3 and ogg (sound, used by T-0041); checked from the file's bytes, not only the extension
- [ ] Settings with defaults, as `TALESPEAK_*` env vars: largest pack, largest asset, most assets per pack. An import over any of them is refused with an error naming the limit and the file. Listed in docs/admin-limits.md
- [ ] A public `GET /api/pack-limits` returns those settings and the allowed types (no account needed), for the editor (T-0042)
- [ ] Only admins upload a pack in the web UI (`POST /api/` route, size-capped before reading); the pack shows up in the catalog without a restart, with today's version rules (docs/pack-versions.md)
- [ ] `TALESPEAK_PACKS` also accepts archive files next to folders
- [ ] Tests for each refusal (bad path, bomb, too big, too many, wrong type, bad YAML), test-engineer and security-reviewer runs, protocol entry

## Notes

- Today packs are folders under `TALESPEAK_PACKS`, read at start; images only (png, jpg, gif, webp, 2 MiB each, 64 MiB per pack). Carl 2026-10-23: gif, webp, png and jpg all stay ("gif is interesting as it can be animations"). Open: whether a folder pack keeps plain `assets/<name>` or is converted to the hashed layout on load.
- Reading chosen for "hash + filename in files.meta": the hash is the storage name, the pack and scripts keep using the friendly file name.
- Plan doc: Talespeak pack editor: options (Claude Doc, https://claude.ai/artifact/LKWWAHzptm1eM8KGMbXts9).
