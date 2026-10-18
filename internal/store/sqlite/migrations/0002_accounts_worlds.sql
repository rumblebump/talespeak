-- Accounts and their sign-in sessions (T-0034).
CREATE TABLE accounts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    name_key      TEXT NOT NULL UNIQUE, -- name in lower case: names are unique in any letter case
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'player' CHECK (role IN ('player', 'admin')),
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

-- Only a hash of each session token is stored.
CREATE TABLE logins (
    token_hash BLOB PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL -- Unix seconds
) STRICT, WITHOUT ROWID;
CREATE INDEX logins_account ON logins (account_id, expires_at);
CREATE INDEX logins_expires ON logins (expires_at);

-- A world is one running game of a pack. Ids are never used twice, so a
-- new world never inherits an old one's state.
CREATE TABLE worlds (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    pack_id      TEXT NOT NULL,
    pack_version TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at  TEXT
) STRICT;
CREATE INDEX worlds_pack ON worlds (pack_id, finished_at);

CREATE TABLE world_members (
    world_id   INTEGER NOT NULL REFERENCES worlds (id) ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('player', 'gm')),
    PRIMARY KEY (world_id, account_id)
) STRICT, WITHOUT ROWID;

-- Only a hash of each invite code is stored.
CREATE TABLE invites (
    code_hash  BLOB PRIMARY KEY,
    world_id   INTEGER NOT NULL REFERENCES worlds (id) ON DELETE CASCADE,
    created_by INTEGER REFERENCES accounts (id) ON DELETE SET NULL,
    role       TEXT NOT NULL CHECK (role IN ('player', 'gm')),
    uses_left  INTEGER NOT NULL CHECK (uses_left >= 0),
    expires_at INTEGER NOT NULL -- Unix seconds
) STRICT, WITHOUT ROWID;
CREATE INDEX invites_expires ON invites (expires_at);

-- One character per account and world. Ids are never used twice: they are
-- player.id in Lua, and a new player must not inherit an old one's turns.
CREATE TABLE characters (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    world_id   INTEGER NOT NULL REFERENCES worlds (id) ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    room       TEXT NOT NULL DEFAULT '',
    UNIQUE (world_id, account_id)
) STRICT;

-- What scripts set with state.player and state.world, in the encoding of
-- package script; the limits are counted on it.
CREATE TABLE player_state (
    character_id INTEGER NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    value        BLOB NOT NULL,
    PRIMARY KEY (character_id, key)
) STRICT, WITHOUT ROWID;

CREATE TABLE world_state (
    world_id INTEGER NOT NULL REFERENCES worlds (id) ON DELETE CASCADE,
    key      TEXT NOT NULL,
    value    BLOB NOT NULL,
    PRIMARY KEY (world_id, key)
) STRICT, WITHOUT ROWID;
