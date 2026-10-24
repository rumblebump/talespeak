-- A world's time (T-0012): a game master pauses it, game-time timers
-- count only while it runs with players in it, and a minimum deadline
-- and extra time per member stretch what players see.
--
-- Timer deadlines are on the world clock: the wall clock less the time
-- the world spent paused (paused_ms, plus the running pause since
-- paused_at), so pausing never rewrites a timer. Game-time timers'
-- deadlines are on the game clock (game_ms).
ALTER TABLE worlds ADD COLUMN paused_at INTEGER;                       -- Unix milliseconds when the running pause began; NULL while the world runs
ALTER TABLE worlds ADD COLUMN paused_ms INTEGER NOT NULL DEFAULT 0;    -- milliseconds paused before, in all
ALTER TABLE worlds ADD COLUMN game_ms INTEGER NOT NULL DEFAULT 0;      -- game time counted, in milliseconds
ALTER TABLE worlds ADD COLUMN min_time_ms INTEGER NOT NULL DEFAULT 0;  -- /mintime; 0 is off

ALTER TABLE world_members ADD COLUMN extra_time REAL NOT NULL DEFAULT 1 CHECK (extra_time BETWEEN 1 AND 3);

ALTER TABLE player_timers ADD COLUMN game INTEGER NOT NULL DEFAULT 0;  -- 1: deadline on the game clock
ALTER TABLE room_timers ADD COLUMN game INTEGER NOT NULL DEFAULT 0;
