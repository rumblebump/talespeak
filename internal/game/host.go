package game

import (
	"errors"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The session as script.Host: what a hook sends to the player while it
// runs, collected until the hook ends. Lua calls these during a hook, so
// w.mu is held.

// flush returns the output of the call, and tells the other players what
// the call changed in state.world. The caller holds w.mu.
func (s *session) flush() []ws.Message {
	s.w.save(s)
	if len(s.w.changed) > 0 {
		s.w.announce(s, s.w.changed)
		s.w.changed = nil
	}
	s.w.announceTimers(s)
	out := s.out
	s.out = nil
	return out
}

// Say, DescribeRoom, Items, Image, Go, Room and PlayerState implement
// script.Host.
func (s *session) Say(text string, how script.Voice) {
	s.out = append(s.out, ws.Message{Type: "text", Text: text, Speak: how.Speak, Hide: how.Hide, From: how.From})
}

func (s *session) DescribeRoom() {
	r := s.w.pack.Rooms[s.room]
	s.out = append(s.out, ws.Message{Type: "room", ID: r.ID, Title: r.Title, Text: r.Description, Items: wsItems(s.items)})
}

func (s *session) Items(items []script.Item) {
	s.items = items
	s.out = append(s.out, ws.Message{Type: "items", Items: wsItems(items)})
}

func (s *session) Go(room string) bool {
	if s.w.pack.Rooms[room] == nil {
		return false
	}
	s.next = room
	return true
}

func (s *session) Room() string { return s.room }

func (s *session) PlayerState() *script.State { return &s.state }

func (s *session) WorldState() *script.State { return &s.w.world }

// reserve accounts for state.player growing by delta bytes: the world and
// the player's client address share the limits of script memory. The
// caller holds w.mu.
func (s *session) reserve(delta int64) error {
	w := s.w
	if delta > 0 && s.client != "" && w.clientKept(s.client)+delta > w.clientMemory {
		return errors.New("too many players from this network keep state right now")
	}
	if err := w.rt.Reserve(delta); err != nil {
		return errors.New("this world is full right now")
	}
	w.stateTotal += delta
	if s.client != "" {
		if w.clientState[s.client] += delta; w.clientState[s.client] <= 0 {
			delete(w.clientState, s.client)
		}
	}
	return nil
}

func (s *session) Image(name, caption string) bool {
	url, ok := s.w.pack.Image(name)
	if ok {
		s.out = append(s.out, ws.Message{Type: "image", URL: url, Text: caption})
	}
	return ok
}

func wsItems(items []script.Item) []ws.Item {
	out := make([]ws.Item, len(items))
	for i, it := range items {
		out[i] = ws.Item{ID: it.ID, Label: it.Label}
	}
	return out
}
