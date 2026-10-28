-- state.game (T-0018): what a world keeps as a whole, its settings
-- included, written only by game masters in GM mode and the world's
-- settings. One world's, never shared with another; deleted when the
-- world finishes, like world_state.
CREATE TABLE game_state (
    world_id INTEGER NOT NULL REFERENCES worlds (id) ON DELETE CASCADE,
    key      TEXT NOT NULL,
    value    BLOB NOT NULL,
    PRIMARY KEY (world_id, key)
) STRICT, WITHOUT ROWID;
