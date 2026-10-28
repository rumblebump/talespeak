package sqlite

import "context"

// state.game (T-0018): one world's settings and what its game masters'
// hooks keep, in game_state. Save writes it, CreateWorld writes the
// settings picked in the lobby, FinishWorld deletes it.

// gameQueries write one key of game_state.
var gameQueries = stateQueries{
	put: `INSERT INTO game_state (world_id, key, value) VALUES (?, ?, ?) ON CONFLICT (world_id, key) DO UPDATE SET value = excluded.value`,
	del: `DELETE FROM game_state WHERE world_id = ? AND key = ?`,
}

// GameState returns what the world keeps in state.game.
func (s *Store) GameState(ctx context.Context, world int64) (map[string][]byte, error) {
	return s.values(ctx, `SELECT key, value FROM game_state WHERE world_id = ?`, world)
}
