-- What game masters do in worlds (T-0011). It outlives the world's game
-- state: finishing a world keeps its log.
CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    world_id   INTEGER NOT NULL REFERENCES worlds (id) ON DELETE CASCADE,
    account_id INTEGER REFERENCES accounts (id) ON DELETE SET NULL,
    name       TEXT NOT NULL,
    action     TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    at         INTEGER NOT NULL -- Unix milliseconds
) STRICT;
CREATE INDEX audit_log_world ON audit_log (world_id, id);
