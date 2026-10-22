-- Pack versions (T-0010): every version of a pack the server has had, and
-- a hash of its files, so a pack that changes without a new version is
-- noticed. worlds.pack_version is the version a world runs.
CREATE TABLE installed_packs (
    pack_id    TEXT NOT NULL,
    version    TEXT NOT NULL,
    hash       TEXT NOT NULL,
    first_seen TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (pack_id, version)
) STRICT, WITHOUT ROWID;
