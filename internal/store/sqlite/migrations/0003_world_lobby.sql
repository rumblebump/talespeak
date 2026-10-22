-- Many worlds per server, listed in the lobby (T-0035). Every world before
-- this one was the server's main world of its pack.
ALTER TABLE worlds ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE worlds ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('public', 'private'));
ALTER TABLE worlds ADD COLUMN created_by INTEGER REFERENCES accounts (id) ON DELETE SET NULL;
ALTER TABLE worlds ADD COLUMN home INTEGER NOT NULL DEFAULT 0 CHECK (home IN (0, 1));
UPDATE worlds SET home = 1;

DROP INDEX worlds_pack;
CREATE INDEX worlds_home ON worlds (pack_id, home, finished_at);
CREATE INDEX worlds_open ON worlds (finished_at, visibility);
CREATE INDEX worlds_creator ON worlds (created_by, finished_at);
CREATE INDEX world_members_account ON world_members (account_id, role);
