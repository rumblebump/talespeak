package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/rumblebump/talespeak/internal/store"
)

// Who belongs to a world, and with which role (world_members).

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

// RemoveMember ends the account's membership of the world.
func (s *Store) RemoveMember(ctx context.Context, world, account int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM world_members WHERE world_id = ? AND account_id = ?`, world, account)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Members lists the world's members, game masters first, then by name,
// at most limit.
func (s *Store) Members(ctx context.Context, world int64, limit int) ([]store.Member, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.name, m.role, m.extra_time FROM world_members m JOIN accounts a ON a.id = m.account_id
		WHERE m.world_id = ? ORDER BY m.role = 'gm' DESC, a.name_key LIMIT ?`, world, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []store.Member
	for rows.Next() {
		var m store.Member
		if err := rows.Scan(&m.Account, &m.Name, &m.Role, &m.ExtraTime); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}
