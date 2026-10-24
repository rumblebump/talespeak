package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// Timers that last (T-0012): a player's timers and question deadlines in
// player_timers, a room's in room_timers, deadlines in Unix milliseconds
// on the world clock, or with game on the game clock (clock.go).

// PlayerTimers returns the character's timers and question deadlines.
func (s *Store) PlayerTimers(ctx context.Context, character int64) ([]store.Timer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, room, name, deadline, show, read_only, game FROM player_timers
		WHERE character_id = ? ORDER BY kind, room, name`, character)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []store.Timer
	for rows.Next() {
		t := store.Timer{Character: character}
		var ms int64
		if err := rows.Scan(&t.Kind, &t.Room, &t.Name, &ms, &t.Show, &t.ReadOnly, &t.Game); err != nil {
			return nil, err
		}
		t.Deadline = time.UnixMilli(ms)
		list = append(list, t)
	}
	return list, rows.Err()
}

// RoomTimers returns the timers of the world's rooms.
func (s *Store) RoomTimers(ctx context.Context, world int64) ([]store.Timer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT room, name, deadline, show, read_only, coalesce(started_by, 0), game FROM room_timers
		WHERE world_id = ? ORDER BY room, name`, world)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []store.Timer
	for rows.Next() {
		t := store.Timer{Kind: store.TimerRoom}
		var ms int64
		if err := rows.Scan(&t.Room, &t.Name, &ms, &t.Show, &t.ReadOnly, &t.Character, &t.Game); err != nil {
			return nil, err
		}
		t.Deadline = time.UnixMilli(ms)
		list = append(list, t)
	}
	return list, rows.Err()
}

// putTimers writes the timers of a save of world. A player timer is only
// written for a character of world, and a room timer's starter only when
// it is one, so a save never touches another world's rows.
func putTimers(ctx context.Context, tx *sql.Tx, world int64, timers []store.Timer) error {
	for _, t := range timers {
		var err error
		switch {
		case t.Kind == store.TimerRoom && t.Delete:
			_, err = tx.ExecContext(ctx, `DELETE FROM room_timers WHERE world_id = ? AND room = ? AND name = ?`, world, t.Room, t.Name)
		case t.Kind == store.TimerRoom:
			_, err = tx.ExecContext(ctx, `INSERT INTO room_timers (world_id, room, name, deadline, show, read_only, started_by, game)
				VALUES (?, ?, ?, ?, ?, ?, (SELECT id FROM characters WHERE id = ? AND world_id = ?), ?)
				ON CONFLICT (world_id, room, name) DO UPDATE SET deadline = excluded.deadline, show = excluded.show,
					read_only = excluded.read_only, started_by = excluded.started_by, game = excluded.game`,
				world, t.Room, t.Name, t.Deadline.UnixMilli(), t.Show, t.ReadOnly, t.Character, world, t.Game)
		case t.Kind != store.TimerPlayer && t.Kind != store.TimerQuestion:
			err = fmt.Errorf("unknown timer kind %q", t.Kind)
		case t.Delete:
			_, err = tx.ExecContext(ctx, `DELETE FROM player_timers WHERE character_id IN (SELECT id FROM characters WHERE id = ? AND world_id = ?)
				AND kind = ? AND room = ? AND name = ?`, t.Character, world, t.Kind, t.Room, t.Name)
		default:
			_, err = tx.ExecContext(ctx, `INSERT INTO player_timers (character_id, kind, room, name, deadline, show, read_only, game)
				SELECT id, ?, ?, ?, ?, ?, ?, ? FROM characters WHERE id = ? AND world_id = ?
				ON CONFLICT (character_id, kind, room, name) DO UPDATE SET deadline = excluded.deadline, show = excluded.show,
					read_only = excluded.read_only, game = excluded.game`,
				t.Kind, t.Room, t.Name, t.Deadline.UnixMilli(), t.Show, t.ReadOnly, t.Game, t.Character, world)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
