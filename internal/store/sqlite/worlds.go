package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// OpenWorld returns the newest unfinished main world of the pack whose
// recorded version keep accepts (nil accepts any), creating one that
// records packVersion when there is none. It changes no world's version:
// the hub records the version a world runs when it loads.
func (s *Store) OpenWorld(ctx context.Context, packID, packVersion string, keep func(recorded string) bool) (store.World, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.World{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, pack_version FROM worlds WHERE pack_id = ? AND home = 1 AND finished_at IS NULL ORDER BY id DESC`, packID)
	if err != nil {
		return store.World{}, err
	}
	var id int64
	for rows.Next() {
		var wid int64
		var v string
		if err := rows.Scan(&wid, &v); err != nil {
			rows.Close()
			return store.World{}, err
		}
		if keep == nil || keep(v) {
			id = wid
			break
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return store.World{}, err
	}
	if id == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO worlds (pack_id, pack_version, home) VALUES (?, ?, 1)`, packID, packVersion)
		if err != nil {
			return store.World{}, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return store.World{}, err
		}
	}
	w, err := world(ctx, tx, id)
	if err != nil {
		return store.World{}, err
	}
	return w, tx.Commit()
}

// SetPackVersion records the pack version the world runs now.
func (s *Store) SetPackVersion(ctx context.Context, world int64, version string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE worlds SET pack_version = ? WHERE id = ?`, version, world)
	if err != nil {
		return err
	}
	return oneRow(res)
}

// RecordPacks notes the packs installed now and returns those installed
// before with the same id and version but another hash.
func (s *Store) RecordPacks(ctx context.Context, packs []store.InstalledPack) ([]store.InstalledPack, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var changed []store.InstalledPack
	for _, p := range packs {
		var had string
		err := tx.QueryRowContext(ctx, `SELECT hash FROM installed_packs WHERE pack_id = ? AND version = ?`, p.ID, p.Version).Scan(&had)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return nil, err
		case had != p.Hash:
			changed = append(changed, p)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO installed_packs (pack_id, version, hash) VALUES (?, ?, ?)
			ON CONFLICT (pack_id, version) DO UPDATE SET hash = excluded.hash, last_seen = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
			p.ID, p.Version, p.Hash); err != nil {
			return nil, err
		}
	}
	return changed, tx.Commit()
}

// SetVisibility changes who may see and play the world.
func (s *Store) SetVisibility(ctx context.Context, world int64, visibility string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE worlds SET visibility = ? WHERE id = ?`, visibility, world)
	if err != nil {
		return err
	}
	return oneRow(res)
}

// CreateWorld adds a world and makes its creator a game master of it, within
// the limits of w.
func (s *Store) CreateWorld(ctx context.Context, nw store.NewWorld) (store.World, error) {
	tx, err := s.db.BeginTx(ctx, nil) // _txlock=immediate: the counts below cannot change before the insert
	if err != nil {
		return store.World{}, err
	}
	defer tx.Rollback()
	var total, mine int
	if err := tx.QueryRowContext(ctx, `SELECT count(*), count(CASE WHEN created_by = ? THEN 1 END) FROM worlds WHERE finished_at IS NULL`,
		nw.CreatedBy).Scan(&total, &mine); err != nil {
		return store.World{}, err
	}
	switch {
	case nw.Total > 0 && total >= nw.Total:
		return store.World{}, store.ErrServerWorlds
	case nw.PerAccount > 0 && mine >= nw.PerAccount:
		return store.World{}, store.ErrAccountWorlds
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO worlds (pack_id, pack_version, title, visibility, created_by) VALUES (?, ?, ?, ?, ?)`,
		nw.PackID, nw.PackVersion, nw.Title, nw.Visibility, nw.CreatedBy)
	if err != nil {
		return store.World{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return store.World{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO world_members (world_id, account_id, role) VALUES (?, ?, ?)`, id, nw.CreatedBy, store.MemberGM); err != nil {
		return store.World{}, err
	}
	w, err := world(ctx, tx, id)
	if err != nil {
		return store.World{}, err
	}
	return w, tx.Commit()
}

// World returns the world, finished or not, or store.ErrNotFound.
func (s *Store) World(ctx context.Context, id int64) (store.World, error) {
	return world(ctx, s.db, id)
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const worldColumns = `w.id, w.pack_id, w.pack_version, w.title, w.visibility, coalesce(w.created_by, 0), w.home, w.created_at, w.finished_at IS NOT NULL`

func world(ctx context.Context, q querier, id int64) (store.World, error) {
	var w store.World
	var created string
	err := q.QueryRowContext(ctx, `SELECT `+worldColumns+` FROM worlds w WHERE w.id = ?`, id).
		Scan(&w.ID, &w.PackID, &w.PackVersion, &w.Title, &w.Visibility, &w.CreatedBy, &w.Home, &created, &w.Finished)
	if errors.Is(err, sql.ErrNoRows) {
		return store.World{}, store.ErrNotFound
	}
	w.Created = parseTime(created)
	return w, err
}

// parseTime reads a time the database wrote with strftime; the zero time
// when it cannot.
func parseTime(s string) time.Time {
	t, err := time.Parse("2006-01-02T15:04:05.999Z", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ListWorlds returns the unfinished worlds account may see, the newest
// first: the public ones and its own, or with all every one.
func (s *Store) ListWorlds(ctx context.Context, account int64, all bool, limit int) ([]store.WorldInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+worldColumns+`, coalesce(a.name, ''), coalesce(m.role, ''),
			(SELECT count(*) FROM characters c WHERE c.world_id = w.id)
		FROM worlds w
		LEFT JOIN world_members m ON m.world_id = w.id AND m.account_id = ?
		LEFT JOIN accounts a ON a.id = w.created_by
		WHERE w.finished_at IS NULL AND (? OR w.visibility = 'public' OR m.role IS NOT NULL)
		ORDER BY m.role IS NULL, w.id DESC LIMIT ?`, account, all, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []store.WorldInfo
	for rows.Next() {
		var wi store.WorldInfo
		var created string
		if err := rows.Scan(&wi.ID, &wi.PackID, &wi.PackVersion, &wi.Title, &wi.Visibility, &wi.CreatedBy, &wi.Home, &created, &wi.Finished,
			&wi.Creator, &wi.Role, &wi.Players); err != nil {
			return nil, err
		}
		wi.Created = parseTime(created)
		list = append(list, wi)
	}
	return list, rows.Err()
}

// IsGM reports whether the account is a game master of an unfinished world.
func (s *Store) IsGM(ctx context.Context, account int64) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM world_members m JOIN worlds w ON w.id = m.world_id
		WHERE m.account_id = ? AND m.role = 'gm' AND w.finished_at IS NULL)`, account).Scan(&ok)
	return ok, err
}

// FinishWorld ends the world and deletes its state and its players' state.
func (s *Store) FinishWorld(ctx context.Context, world int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE worlds SET finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND finished_at IS NULL`, world)
	if err != nil {
		return err
	}
	if err := oneRow(res); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM world_state WHERE world_id = ?`,
		`DELETE FROM player_state WHERE character_id IN (SELECT id FROM characters WHERE world_id = ?)`,
		`DELETE FROM invites WHERE world_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, world); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Member returns the account's role in the world, or store.ErrNotFound.
func (s *Store) Member(ctx context.Context, world, account int64) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM world_members WHERE world_id = ? AND account_id = ?`, world, account).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return role, err
}

// AddMember makes the account a member of the world, or changes its role.
func (s *Store) AddMember(ctx context.Context, world, account int64, role string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO world_members (world_id, account_id, role) VALUES (?, ?, ?)
		ON CONFLICT (world_id, account_id) DO UPDATE SET role = excluded.role`, world, account, role)
	return err
}

// CreateInvite stores an invite, and drops expired ones.
func (s *Store) CreateInvite(ctx context.Context, inv store.Invite, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM invites WHERE expires_at <= ? OR uses_left = 0`, now.Unix()); err != nil {
		return err
	}
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM invites WHERE world_id = ?`, inv.WorldID).Scan(&live); err != nil {
		return err
	}
	if live >= store.MaxLiveInvites {
		return store.ErrInvites
	}
	var by any
	if inv.CreatedBy != 0 {
		by = inv.CreatedBy
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO invites (code_hash, world_id, created_by, role, uses_left, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		inv.CodeHash, inv.WorldID, by, inv.Role, inv.Uses, inv.Expires.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// RedeemInvite uses one use of the invite: the account becomes a member of
// its world. A member keeps a higher role (gm) than the invite's. An account
// that is a member with that role already uses nothing.
func (s *Store) RedeemInvite(ctx context.Context, codeHash []byte, account int64, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var role string
	var world int64
	err = tx.QueryRowContext(ctx, `SELECT i.role, i.world_id FROM invites i JOIN worlds w ON w.id = i.world_id
		WHERE i.code_hash = ? AND i.uses_left > 0 AND i.expires_at > ? AND w.finished_at IS NULL`,
		codeHash, now.Unix()).Scan(&role, &world)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	var had string
	err = tx.QueryRowContext(ctx, `SELECT role FROM world_members WHERE world_id = ? AND account_id = ?`, world, account).Scan(&had)
	switch {
	case err == nil && (had == role || had == store.MemberGM):
		return world, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invites SET uses_left = uses_left - 1 WHERE code_hash = ?`, codeHash); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO world_members (world_id, account_id, role) VALUES (?, ?, ?)
		ON CONFLICT (world_id, account_id) DO UPDATE SET role = excluded.role`, world, account, role); err != nil {
		return 0, err
	}
	return world, tx.Commit()
}

// Character returns the account's character in the world, creating it with
// name when there is none.
func (s *Store) Character(ctx context.Context, world, account int64, name string) (store.Character, error) {
	var c store.Character
	err := s.db.QueryRowContext(ctx, `INSERT INTO characters (world_id, account_id, name) VALUES (?, ?, ?)
		ON CONFLICT (world_id, account_id) DO UPDATE SET world_id = world_id
		RETURNING id, name, room`, world, account, name).Scan(&c.ID, &c.Name, &c.Room)
	return c, err
}

// PlayerState returns what the character keeps in state.player.
func (s *Store) PlayerState(ctx context.Context, character int64) (map[string][]byte, error) {
	return s.values(ctx, `SELECT key, value FROM player_state WHERE character_id = ?`, character)
}

// WorldState returns what the world keeps in state.world.
func (s *Store) WorldState(ctx context.Context, world int64) (map[string][]byte, error) {
	return s.values(ctx, `SELECT key, value FROM world_state WHERE world_id = ?`, world)
}

func (s *Store) values(ctx context.Context, query string, id int64) (map[string][]byte, error) {
	rows, err := s.db.QueryContext(ctx, query, id)
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

// Save writes what one hook call changed in one transaction.
func (s *Store) Save(ctx context.Context, sv store.Save) error {
	if sv.Empty() {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := put(ctx, tx, worldQueries, sv.World, sv.WorldState); err != nil {
		return err
	}
	if err := put(ctx, tx, playerQueries, sv.Character, sv.PlayerState); err != nil {
		return err
	}
	if sv.Room != "" && sv.Character != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE characters SET room = ? WHERE id = ?`, sv.Room, sv.Character); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// stateQueries write one key of a state table.
type stateQueries struct{ put, del string }

var (
	worldQueries = stateQueries{
		put: `INSERT INTO world_state (world_id, key, value) VALUES (?, ?, ?) ON CONFLICT (world_id, key) DO UPDATE SET value = excluded.value`,
		del: `DELETE FROM world_state WHERE world_id = ? AND key = ?`,
	}
	playerQueries = stateQueries{
		put: `INSERT INTO player_state (character_id, key, value) VALUES (?, ?, ?) ON CONFLICT (character_id, key) DO UPDATE SET value = excluded.value`,
		del: `DELETE FROM player_state WHERE character_id = ? AND key = ?`,
	}
)

// put writes changes for the owner id with q.
func put(ctx context.Context, tx *sql.Tx, q stateQueries, id int64, changes []store.Change) error {
	for _, c := range changes {
		var err error
		if c.Value == nil {
			_, err = tx.ExecContext(ctx, q.del, id, c.Key)
		} else {
			_, err = tx.ExecContext(ctx, q.put, id, c.Key, c.Value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
