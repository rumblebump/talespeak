package game

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/rumblebump/talespeak/internal/script"
)

// state.game (T-0018): what the world keeps as a whole, its settings
// included. Every script reads it; only hooks of a game master in GM mode
// write it (script.Game), and the engine writes the settings: their
// defaults at load, the lobby's picks (the store, at creation) and /set
// (settings.go). It is saved after every hook with state.world, and
// world.finish ends the world.

// WithFinish lets world.finish end the world: finish runs on its own
// goroutine once a hook called it, without w.mu, and ends the world like
// ending it in the lobby (hub.Hub.Finish). Without it, world.finish
// returns nil and a reason, as in the server's main world.
func WithFinish(finish func(text string)) Option {
	return func(w *World) { w.finish = finish }
}

// loadGame reads state.game from the store.
func (w *World) loadGame(ctx context.Context) error {
	vals, err := w.store.GameState(ctx, w.worldID)
	if err != nil {
		return err
	}
	return w.game.Load(vals)
}

// applySettings gives every setting of the pack a valid value: its
// default when the world has none, or one its pack no longer offers (a
// world created before the setting, or the server's main world). With a
// store, the next save writes them.
func (w *World) applySettings() {
	for _, st := range w.pack.Settings {
		if v, ok := w.game.String(st.Name); ok && st.ValidChoice(v) {
			continue
		}
		if err := w.game.SetString(st.Name, st.Default); err != nil {
			w.log.Warn("setting not applied", "setting", st.Name, "err", err)
		}
	}
}

// changeGame notes a key of state.game a hook or the engine changed, for
// the next save. The caller holds w.mu.
func (w *World) changeGame(key string) {
	if w.store != nil && !slices.Contains(w.gameChanged, key) {
		w.gameChanged = append(w.gameChanged, key)
	}
}

// gameKeys returns the state.game keys the next save writes: those a
// failed save did not write, and those changed since. The caller holds
// w.mu.
func (w *World) gameKeys() []string {
	keys := w.gameUnsaved
	for _, k := range w.gameChanged {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	w.gameChanged = nil
	return keys
}

// errMainWorld refuses world.finish in a world that cannot end from a
// script.
var errMainWorld = errors.New("this world cannot end from a script (the server's main world ends only with the server)")

// Finish implements script.Game: the world ends for everyone once the
// hook is over, its state is deleted and its audit log kept. A second
// call while it ends changes nothing. The caller holds w.mu.
func (s *session) Finish(text string) error {
	w := s.w
	if w.finish == nil {
		return errMainWorld
	}
	if w.finishing {
		return nil
	}
	w.finishing, w.finishText = true, text
	go func() {
		w.mu.Lock()
		if err := s.audit("world.finish", text); err != nil {
			w.log.Error("world ends without its audit entry", "err", err) // the pack's ending, not a game master's choice
		}
		w.mu.Unlock()
		w.finish(text)
	}()
	return nil
}

// StateRefused implements script.Refusals: the log shows the first write
// of each hook that a state limit refused (the script got nil and msg).
// The caller holds w.mu.
func (s *session) StateRefused(msg string) {
	s.w.log.Warn("state write refused", "player", s.id, "room", s.room, "reason", strconv.Quote(shorten(msg, maxLogBytes)))
}

// session is the script.Game of its hooks, and hears of refused writes.
var (
	_ script.Game     = (*session)(nil)
	_ script.Refusals = (*session)(nil)
)
