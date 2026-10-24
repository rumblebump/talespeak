-- Profiles (T-0010): an account's characters, each bound to a ruleset id
-- and the ruleset version its sheet was last written with. generation
-- counts migrations and undos, so a world that loaded the sheet before
-- one writes nothing more to it.
CREATE TABLE profiles (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    name_key        TEXT NOT NULL, -- name in lower case: unique per account in any letter case
    ruleset_id      TEXT NOT NULL,
    ruleset_version TEXT NOT NULL,
    generation      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (account_id, name_key)
) STRICT;

-- The sheet, what scripts set with state.profile, in the encoding of
-- package script.
CREATE TABLE profile_state (
    profile_id INTEGER NOT NULL REFERENCES profiles (id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      BLOB NOT NULL,
    PRIMARY KEY (profile_id, key)
) STRICT, WITHOUT ROWID;

-- The sheet and version before the last migration, which
-- `talespeak profile undo` brings back: one per profile.
CREATE TABLE profile_snapshots (
    profile_id      INTEGER PRIMARY KEY REFERENCES profiles (id) ON DELETE CASCADE,
    ruleset_version TEXT NOT NULL,
    taken_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE TABLE profile_snapshot_state (
    profile_id INTEGER NOT NULL REFERENCES profile_snapshots (profile_id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      BLOB NOT NULL,
    PRIMARY KEY (profile_id, key)
) STRICT, WITHOUT ROWID;

-- How far a profile got in each story pack, and the flags and items the
-- story exported for the stories that continue it.
CREATE TABLE profile_progress (
    profile_id   INTEGER NOT NULL REFERENCES profiles (id) ON DELETE CASCADE,
    pack_id      TEXT NOT NULL,
    pack_version TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('playing', 'completed')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (profile_id, pack_id)
) STRICT, WITHOUT ROWID;

CREATE TABLE profile_flags (
    profile_id INTEGER NOT NULL,
    pack_id    TEXT NOT NULL,
    name       TEXT NOT NULL, -- flags.<name> or items.<name>
    value      BLOB NOT NULL,
    PRIMARY KEY (profile_id, pack_id, name),
    FOREIGN KEY (profile_id, pack_id) REFERENCES profile_progress (profile_id, pack_id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

-- Each world's character points to the profile it plays; NULL in worlds
-- of packs without a ruleset.
ALTER TABLE characters ADD COLUMN profile_id INTEGER REFERENCES profiles (id) ON DELETE SET NULL;
CREATE INDEX characters_account ON characters (account_id, profile_id);
CREATE INDEX characters_profile ON characters (profile_id);
