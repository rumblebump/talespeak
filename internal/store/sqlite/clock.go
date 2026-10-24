package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// A world's time (T-0012): whether it is paused, its game clock and
// /mintime on the worlds row, each member's extra time on world_members.

// WorldClock returns the world's time, or store.ErrNotFound.
func (s *Store) WorldClock(ctx context.Context, world int64) (store.Clock, error) {
	var at sql.NullInt64
	var pausedMs, gameMs, minMs int64
	err := s.db.QueryRowContext(ctx, `SELECT paused_at, paused_ms, game_ms, min_time_ms FROM worlds WHERE id = ?`, world).
		Scan(&at, &pausedMs, &gameMs, &minMs)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Clock{}, store.ErrNotFound
	}
	if err != nil {
		return store.Clock{}, err
	}
	c := store.Clock{PausedFor: millis(pausedMs), Game: millis(gameMs), MinTime: millis(minMs)}
	if at.Valid {
		c.PausedAt = time.UnixMilli(at.Int64)
	}
	return c, nil
}

func millis(n int64) time.Duration { return time.Duration(n) * time.Millisecond }

// putClock writes the world's time in a save.
func putClock(ctx context.Context, tx *sql.Tx, world int64, c *store.Clock) error {
	if c == nil {
		return nil
	}
	var at any
	if !c.PausedAt.IsZero() {
		at = c.PausedAt.UnixMilli()
	}
	_, err := tx.ExecContext(ctx, `UPDATE worlds SET paused_at = ?, paused_ms = ?, game_ms = ?, min_time_ms = ? WHERE id = ?`,
		at, c.PausedFor.Milliseconds(), c.Game.Milliseconds(), c.MinTime.Milliseconds(), world)
	return err
}

// ExtraTime returns the member's extra-time factor, or store.ErrNotFound.
func (s *Store) ExtraTime(ctx context.Context, world, account int64) (float64, error) {
	var f float64
	err := s.db.QueryRowContext(ctx, `SELECT extra_time FROM world_members WHERE world_id = ? AND account_id = ?`, world, account).Scan(&f)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	return f, err
}

// SetExtraTime sets the extra-time factor of the world's member named
// name, in any letter case, and returns its account.
func (s *Store) SetExtraTime(ctx context.Context, world int64, name string, factor float64) (int64, error) {
	if factor < store.MinExtraTime || factor > store.MaxExtraTime {
		return 0, errors.New("sqlite: extra time out of range")
	}
	var account int64
	err := s.db.QueryRowContext(ctx, `UPDATE world_members SET extra_time = ?
		WHERE world_id = ? AND account_id = (SELECT id FROM accounts WHERE name_key = ?) RETURNING account_id`,
		factor, world, nameKey(name)).Scan(&account)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	return account, err
}
