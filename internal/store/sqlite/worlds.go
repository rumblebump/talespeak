package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// OpenWorld returns the newest unfinished world of the pack, creating one
// when there is none, and records the pack version it runs now.
func (s *Store) OpenWorld(ctx context.Context, packID, packVersion string) (store.World, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.World{}, err
	}
	defer tx.Rollback()
	w := store.World{PackID: packID, PackVersion: packVersion}
	err = tx.QueryRowContext(ctx, `SELECT id FROM worlds WHERE pack_id = ? AND finished_at IS NULL ORDER BY id DESC LIMIT 1`, packID).Scan(&w.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, err := tx.ExecContext(ctx, `INSERT INTO worlds (pack_id, pack_version) VALUES (?, ?)`, packID, packVersion)
		if err != nil {
			return store.World{}, err
		}
		if w.ID, err = res.LastInsertId(); err != nil {
			return store.World{}, err
		}
	case err != nil:
		return store.World{}, err
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE worlds SET pack_version = ? WHERE id = ?`, packVersion, w.ID); err != nil {
			return store.World{}, err
		}
	}
	return w, tx.Commit()
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

// RedeemInvite uses one use of the invite to world: the account becomes a
// member. A member keeps a higher role (gm) than the invite's. An account
// that is a member with that role already uses nothing.
func (s *Store) RedeemInvite(ctx context.Context, codeHash []byte, world, account int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role string
	err = tx.QueryRowContext(ctx, `SELECT role FROM invites WHERE code_hash = ? AND world_id = ? AND uses_left > 0 AND expires_at > ?`,
		codeHash, world, now.Unix()).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	var had string
	err = tx.QueryRowContext(ctx, `SELECT role FROM world_members WHERE world_id = ? AND account_id = ?`, world, account).Scan(&had)
	switch {
	case err == nil && (had == role || had == store.MemberGM):
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invites SET uses_left = uses_left - 1 WHERE code_hash = ?`, codeHash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO world_members (world_id, account_id, role) VALUES (?, ?, ?)
		ON CONFLICT (world_id, account_id) DO UPDATE SET role = excluded.role`, world, account, role); err != nil {
		return err
	}
	return tx.Commit()
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
