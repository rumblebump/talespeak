package game

import (
	"slices"
	"strconv"

	"github.com/rumblebump/talespeak/internal/ws"
)

// What happened in a room (room.event, script.Events): told to the
// players in it, and its last MaxRoomEvents kept in memory, so a player
// who connects again reads what happened there meanwhile. Players never
// write events: only a pack's hook does, and Shared (typed.go) refuses
// events with words a player typed. A game master's /say and /sayroom
// are not events: they are never kept.

// MaxRoomEvents is how many of a room's last events the world keeps.
const MaxRoomEvents = 10

// roomEvent is one kept event; id is unique across the world's runs, so
// a client can tell an event it shows already from a new one.
type roomEvent struct {
	id, text string
}

// Event implements script.Events. The caller holds w.mu.
func (s *session) Event(text string, all bool) error {
	w := s.w
	if err := w.shareable(text); err != nil {
		return err
	}
	w.eventSeq++
	ev := roomEvent{id: w.runID + "." + strconv.FormatInt(w.eventSeq, 36), text: text}
	ring := append(w.events[s.room], ev)
	if n := len(ring) - MaxRoomEvents; n > 0 {
		ring = slices.Delete(ring, 0, n)
	}
	if w.events == nil {
		w.events = map[string][]roomEvent{}
	}
	w.events[s.room] = ring
	m := ws.Message{Type: "event", ID: ev.id, Text: text}
	for _, p := range w.inRoom(s.room) {
		if p != s {
			p.push([]ws.Message{m})
		}
	}
	if all {
		s.out = append(s.out, m)
	}
	return nil
}

// replay is what happened in room lately, the oldest first, for a player
// who connects. The caller holds w.mu.
func (w *World) replay(room string) []ws.Message {
	ring := w.events[room]
	out := make([]ws.Message, len(ring))
	for i, ev := range ring {
		out[i] = ws.Message{Type: "event", ID: ev.id, Text: ev.text, Replay: true}
	}
	return out
}
