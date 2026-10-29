package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/rumblebump/talespeak/internal/store"
)

// Profiles (T-0010): profiles, their sheets in profile_state, the snapshot
// a migration keeps, and their progress and exports per story pack.

// CreateProfile adds a profile to the account, within store.MaxProfiles.
func (s *Store) CreateProfile(ctx context.Context, account int64, name, ruleset, version string) (store.Profile, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Profile{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM profiles WHERE account_id = ?`, account).Scan(&n); err != nil {
		return store.Profile{}, err
	}
	if n >= store.MaxProfiles {
		return store.Profile{}, store.ErrProfiles
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO profiles (account_id, name, name_key, ruleset_id, ruleset_version) VALUES (?, ?, ?, ?, ?)
		RETURNING id`, account, name, strings.ToLower(name), ruleset, version).Scan(&id)
	if isUnique(err) {
		return store.Profile{}, store.ErrNameTaken
	}
	if err != nil {
		return store.Profile{}, err
	}
	p, err := profile(ctx, tx, id)
	if err != nil {
		return store.Profile{}, err
	}
	return p, tx.Commit()
}

const profileColumns = `p.id, p.account_id, p.name, p.ruleset_id, p.ruleset_version, p.generation, coalesce(sn.ruleset_version, ''), p.created_at`

const profileFrom = ` FROM profiles p LEFT JOIN profile_snapshots sn ON sn.profile_id = p.id`

// scanner is a *sql.Row or *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanProfile(sc scanner) (store.Profile, error) {
	var p store.Profile
	var created string
	if err := sc.Scan(&p.ID, &p.Account, &p.Name, &p.Ruleset, &p.Version, &p.Generation, &p.Undo, &created); err != nil {
		return store.Profile{}, err
	}
	p.Created = parseTime(created)
	return p, nil
}

func profile(ctx context.Context, q querier, id int64) (store.Profile, error) {
	p, err := scanProfile(q.QueryRowContext(ctx, `SELECT `+profileColumns+profileFrom+` WHERE p.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Profile{}, store.ErrNotFound
	}
	return p, err
}

// Profile returns one profile, or store.ErrNotFound.
func (s *Store) Profile(ctx context.Context, id int64) (store.Profile, error) {
	return profile(ctx, s.db, id)
}

// AccountProfiles returns the account's profiles, the oldest first.
func (s *Store) AccountProfiles(ctx context.Context, account int64) ([]store.Profile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+profileColumns+profileFrom+` WHERE p.account_id = ? ORDER BY p.id LIMIT ?`,
		account, store.MaxProfiles)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []store.Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// AccountProgress returns the progress of the account's profiles.
func (s *Store) AccountProgress(ctx context.Context, account int64) ([]store.Progress, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pr.profile_id, pr.pack_id, pr.pack_version, pr.status
		FROM profile_progress pr JOIN profiles p ON p.id = pr.profile_id WHERE p.account_id = ? ORDER BY pr.profile_id, pr.pack_id`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []store.Progress
	for rows.Next() {
		var pr store.Progress
		if err := rows.Scan(&pr.Profile, &pr.Pack, &pr.Version, &pr.Status); err != nil {
			return nil, err
		}
		list = append(list, pr)
	}
	return list, rows.Err()
}

// BoundProfiles returns the profile of each of the account's characters
// in unfinished worlds that has one, by world.
func (s *Store) BoundProfiles(ctx context.Context, account int64) (map[int64]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.world_id, c.profile_id FROM characters c JOIN worlds w ON w.id = c.world_id
		WHERE c.account_id = ? AND c.profile_id IS NOT NULL AND w.finished_at IS NULL`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bound := map[int64]int64{}
	for rows.Next() {
		var world, p int64
		if err := rows.Scan(&world, &p); err != nil {
			return nil, err
		}
		bound[world] = p
	}
	return bound, rows.Err()
}

// ProfileState returns what the profile keeps in state.profile.
func (s *Store) ProfileState(ctx context.Context, profile int64) (map[string][]byte, error) {
	return s.values(ctx, `SELECT key, value FROM profile_state WHERE profile_id = ?`, profile)
}

// Exports returns what the profile exported in pack.
func (s *Store) Exports(ctx context.Context, profile int64, pack string) (map[string][]byte, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, value FROM profile_flags WHERE profile_id = ? AND pack_id = ?`, profile, pack)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	vals := map[string][]byte{}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		vals[k] = v
	}
	return vals, rows.Err()
}

// BindProfile binds the account's character in b.World to b.Profile.
func (s *Store) BindProfile(ctx context.Context, b store.Bind) (store.Character, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Character{}, err
	}
	defer tx.Rollback()
	var owner int64
	err = tx.QueryRowContext(ctx, `SELECT account_id FROM profiles WHERE id = ?`, b.Profile).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != b.Account) {
		return store.Character{}, store.ErrNotFound
	}
	if err != nil {
		return store.Character{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO characters (world_id, account_id, name) VALUES (?, ?, ?)
		ON CONFLICT (world_id, account_id) DO NOTHING`, b.World, b.Account, b.Name); err != nil {
		return store.Character{}, err
	}
	var c store.Character
	if err := tx.QueryRowContext(ctx, `SELECT id, name, room, coalesce(profile_id, 0) FROM characters WHERE world_id = ? AND account_id = ?`,
		b.World, b.Account).Scan(&c.ID, &c.Name, &c.Room, &c.Profile); err != nil {
		return store.Character{}, err
	}
	switch c.Profile {
	case b.Profile:
	case 0:
		if _, err := tx.ExecContext(ctx, `UPDATE characters SET profile_id = ? WHERE id = ?`, b.Profile, c.ID); err != nil {
			return store.Character{}, err
		}
		c.Profile = b.Profile
		if err := put(ctx, tx, playerQueries, c.ID, b.Imports); err != nil {
			return store.Character{}, err
		}
	default:
		return store.Character{}, store.ErrProfileChanged
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_progress (profile_id, pack_id, pack_version, status) VALUES (?, ?, ?, 'playing')
		ON CONFLICT (profile_id, pack_id) DO UPDATE SET pack_version = excluded.pack_version,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`, b.Profile, b.Pack, b.Version); err != nil {
		return store.Character{}, err
	}
	return c, tx.Commit()
}

// SetProfileVersion moves the profile to version while it is at gen.
func (s *Store) SetProfileVersion(ctx context.Context, profile, gen int64, version string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE profiles SET ruleset_version = ? WHERE id = ? AND generation = ?`, version, profile, gen)
	if err != nil {
		return err
	}
	if err := oneRow(res); errors.Is(err, store.ErrNotFound) {
		return store.ErrProfileChanged
	} else if err != nil {
		return err
	}
	return nil
}

// MigrateProfile replaces the sheet, keeping the old one as the snapshot.
func (s *Store) MigrateProfile(ctx context.Context, profile, gen int64, from, to string, state map[string][]byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE profiles SET ruleset_version = ?, generation = generation + 1
		WHERE id = ? AND generation = ? AND ruleset_version = ?`, to, profile, gen, from)
	if err != nil {
		return err
	}
	if err := oneRow(res); errors.Is(err, store.ErrNotFound) {
		return store.ErrProfileChanged
	} else if err != nil {
		return err
	}
	if err := execAll(ctx, tx, profile, `DELETE FROM profile_snapshots WHERE profile_id = ?`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_snapshots (profile_id, ruleset_version) VALUES (?, ?)`, profile, from); err != nil {
		return err
	}
	if err := execAll(ctx, tx, profile,
		`INSERT INTO profile_snapshot_state (profile_id, key, value) SELECT profile_id, key, value FROM profile_state WHERE profile_id = ?`,
		`DELETE FROM profile_state WHERE profile_id = ?`); err != nil {
		return err
	}
	for k, v := range state {
		if _, err := tx.ExecContext(ctx, profileQueries.put, profile, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UndoProfile brings back the snapshot of the last migration.
func (s *Store) UndoProfile(ctx context.Context, id int64) (store.Profile, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Profile{}, err
	}
	defer tx.Rollback()
	var version string
	err = tx.QueryRowContext(ctx, `SELECT ruleset_version FROM profile_snapshots WHERE profile_id = ?`, id).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Profile{}, store.ErrNotFound
	}
	if err != nil {
		return store.Profile{}, err
	}
	if err := execAll(ctx, tx, id,
		`DELETE FROM profile_state WHERE profile_id = ?`,
		`INSERT INTO profile_state (profile_id, key, value) SELECT profile_id, key, value FROM profile_snapshot_state WHERE profile_id = ?`,
		`DELETE FROM profile_snapshots WHERE profile_id = ?`); err != nil {
		return store.Profile{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE profiles SET ruleset_version = ?, generation = generation + 1 WHERE id = ?`, version, id); err != nil {
		return store.Profile{}, err
	}
	p, err := profile(ctx, tx, id)
	if err != nil {
		return store.Profile{}, err
	}
	return p, tx.Commit()
}

var (
	profileQueries = stateQueries{
		put: `INSERT INTO profile_state (profile_id, key, value) VALUES (?, ?, ?) ON CONFLICT (profile_id, key) DO UPDATE SET value = excluded.value`,
		del: `DELETE FROM profile_state WHERE profile_id = ? AND key = ?`,
	}
)

// putProfile writes the profile part of a save: only for the profile of
// the save's character in the save's world, and only while the profile
// is at the generation the world loaded. A migration or undo since then
// wins, and what the world changed in the old sheet is dropped.
func putProfile(ctx context.Context, tx *sql.Tx, sv store.Save) error {
	if sv.Profile == 0 || (len(sv.ProfileState) == 0 && sv.Progress == nil) {
		return nil
	}
	var gen int64
	err := tx.QueryRowContext(ctx, `SELECT p.generation FROM profiles p JOIN characters c ON c.profile_id = p.id
		WHERE p.id = ? AND c.id = ? AND c.world_id = ?`, sv.Profile, sv.Character, sv.World).Scan(&gen)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && gen != sv.ProfileGen) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := put(ctx, tx, profileQueries, sv.Profile, sv.ProfileState); err != nil {
		return err
	}
	pr := sv.Progress
	if pr == nil {
		return nil
	}
	status := store.ProgressPlaying
	if pr.Complete {
		status = store.ProgressCompleted
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_progress (profile_id, pack_id, pack_version, status) VALUES (?, ?, ?, ?)
		ON CONFLICT (profile_id, pack_id) DO UPDATE SET pack_version = excluded.pack_version,
			status = CASE WHEN excluded.status = 'completed' THEN 'completed' ELSE status END,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`, sv.Profile, pr.Pack, pr.Version, status); err != nil {
		return err
	}
	for _, c := range pr.Exports {
		var err error
		if c.Value == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM profile_flags WHERE profile_id = ? AND pack_id = ? AND name = ?`, sv.Profile, pr.Pack, c.Key)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO profile_flags (profile_id, pack_id, name, value) VALUES (?, ?, ?, ?)
				ON CONFLICT (profile_id, pack_id, name) DO UPDATE SET value = excluded.value`, sv.Profile, pr.Pack, c.Key, c.Value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// execAll runs each query with the profile id as its one argument.
func execAll(ctx context.Context, tx *sql.Tx, id int64, queries ...string) error {
	for _, q := range queries {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return nil
}
