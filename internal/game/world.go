package game

import (
	"errors"
	"slices"
	"time"

	"github.com/rumblebump/talespeak/internal/script"
)

// state.world: values a world's players share, and on_change, which tells
// the other players' rooms what changed.

// MaxChangedKeys is the most keys one on_change call gets. A player who
// waits for Lua time while many keys change gets the latest ones.
const MaxChangedKeys = script.MaxStateWrites

// reserveWorld counts state.world toward the world's script memory. The
// caller holds w.mu.
func (w *World) reserveWorld(delta int64) error {
	if err := w.rt.Reserve(delta); err != nil {
		return errors.New("this world is full right now")
	}
	return nil
}

// change notes a key the running hook changed in state.world. The caller
// holds w.mu.
func (w *World) change(key string) {
	if !slices.Contains(w.changed, key) {
		w.changed = append(w.changed, key)
	}
}

// announce gives every player but writer the changed keys, and schedules
// their on_change. The caller holds w.mu.
func (w *World) announce(writer *session, keys []string) {
	if w.closed {
		return
	}
	for s := range w.sessions {
		if s != writer {
			s.changed = mergeKeys(s.changed, keys)
		}
	}
	w.deliverSoon()
}

// mergeKeys adds keys to old, each once, keeping the latest
// MaxChangedKeys.
func mergeKeys(old, keys []string) []string {
	for _, k := range keys {
		if i := slices.Index(old, k); i >= 0 {
			old = slices.Delete(old, i, i+1)
		}
		old = append(old, k)
	}
	if n := len(old) - MaxChangedKeys; n > 0 {
		old = slices.Delete(old, 0, n)
	}
	return old
}

// DeliverGap is the shortest time between two deliveries of on_change in
// a world. Changes made meanwhile reach each player in one call, so a
// player writing world state as fast as they can makes the others' rooms
// run at most a few times a second.
const DeliverGap = 250 * time.Millisecond

// deliverSoon schedules a delivery of on_change, unless one is due. The
// caller holds w.mu.
func (w *World) deliverSoon() {
	if !w.delivering {
		w.delivering = true
		w.after(max(0, w.lastDeliver.Add(DeliverGap).Sub(w.now())), w.deliver)
	}
}

// deliver runs on_change for every player with changed keys, one player
// per hold of the world's lock, so players acting meanwhile wait for one
// hook at most. A player whose address has no Lua time left keeps the
// keys for a later delivery.
func (w *World) deliver() {
	w.mu.Lock()
	w.delivering = false
	w.lastDeliver = w.now()
	var due []*session
	for s := range w.sessions {
		if len(s.changed) > 0 {
			due = append(due, s)
		}
	}
	w.mu.Unlock()
	retry := false
	for _, s := range due {
		w.mu.Lock()
		if !w.closed && !s.closed && len(s.changed) > 0 {
			keys := s.changed
			s.changed = nil
			if !s.runPushed(script.Hook{Name: "on_change", Keys: keys, ReadOnlyWorld: true}) {
				s.changed = mergeKeys(keys, s.changed)
				retry = true
			}
		}
		w.mu.Unlock()
	}
	if retry {
		w.mu.Lock()
		if !w.closed && !w.delivering {
			w.delivering = true
			w.after(retryBusy, w.deliver)
		}
		w.mu.Unlock()
	}
}
