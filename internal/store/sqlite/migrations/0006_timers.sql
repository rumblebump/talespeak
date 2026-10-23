-- Timers that last through reloads and restarts (T-0012), with absolute
-- deadlines. A player's timers and the deadlines of questions they were
-- asked belong to the character; a room's timers to the world.
CREATE TABLE player_timers (
    character_id INTEGER NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('timer', 'question')),
    room         TEXT NOT NULL DEFAULT '', -- a question's room; '' for a timer
    name         TEXT NOT NULL,            -- a timer's name, or a hash of the question
    deadline     INTEGER NOT NULL,         -- Unix milliseconds
    show         TEXT NOT NULL DEFAULT '',
    read_only    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (character_id, kind, room, name)
) STRICT, WITHOUT ROWID;

CREATE TABLE room_timers (
    world_id   INTEGER NOT NULL REFERENCES worlds (id) ON DELETE CASCADE,
    room       TEXT NOT NULL,
    name       TEXT NOT NULL,
    deadline   INTEGER NOT NULL, -- Unix milliseconds
    show       TEXT NOT NULL DEFAULT '',
    read_only  INTEGER NOT NULL DEFAULT 0,
    started_by INTEGER REFERENCES characters (id) ON DELETE SET NULL,
    PRIMARY KEY (world_id, room, name)
) STRICT, WITHOUT ROWID;
