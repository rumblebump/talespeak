package game

import (
	"cmp"
	"errors"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// What the players of a room share: who is there, leaving the world, and
// the room's timers (script.Party). Turn order and shared items are Lua
// built on these (T-0019, T-0020).

// freeName is DefaultPlayerName, or with the lowest number no other player
// of the world has: "traveler", "traveler 2", ... Accounts bring real names
// (T-0010). The caller holds w.mu.
func (w *World) freeName() string {
	taken := map[string]bool{}
	for s := range w.sessions {
		taken[s.name] = true
	}
	name := DefaultPlayerName
	for n := 2; taken[name]; n++ {
		name = DefaultPlayerName + " " + strconv.Itoa(n)
	}
	return name
}

// here reports whether s is among the players of room: connected, not
// leaving, and in it. The caller holds w.mu.
func (s *session) here(room string) bool {
	return !s.closed && !s.leaving && s.room == room
}

// inRoom lists the players in room, the earliest to come in first. The
// caller holds w.mu.
func (w *World) inRoom(room string) []*session {
	var list []*session
	for p := range w.sessions {
		if p.here(room) {
			list = append(list, p)
		}
	}
	slices.SortFunc(list, func(a, b *session) int { return cmp.Compare(a.arrived, b.arrived) })
	return list
}

// Players implements script.Party.
func (s *session) Players() []script.Player {
	list := s.w.inRoom(s.room)
	out := make([]script.Player, len(list))
	for i, p := range list {
		out[i] = script.Player{ID: p.id, Name: p.name}
	}
	return out
}

// leave runs on_leave(player) in the room the player is in as they leave
// the world: it can change state.world, which the others hear of, but its
// output reaches no one and it cannot ask or move. It runs within the
// player's Lua time; when that is used up, it does not run. The caller
// holds w.mu.
func (s *session) leave() {
	w := s.w
	if w.closed || s.closed {
		return
	}
	s.drop()
	s.leaving = true
	env := s.envs[s.room]
	if env != nil && env.HasHook("on_leave") {
		s.moves = 0
		h := script.Hook{Name: "on_leave", Player: s.name, PlayerID: s.id, NoWait: true}
		_, err := s.run1(func() (*script.Thread, error) { return w.rt.RunHook(s, env, h) })
		if err != nil {
			w.log.Warn("room script failed", "room", s.room, "hook", "on_leave", "err", logText(err))
		}
	}
	s.next = ""
	s.out = nil
	_ = s.flush() // tell the others what on_leave changed
}

// MaxRoomTimers is how many timers one room runs at once.
const MaxRoomTimers = script.MaxTimers

// roomTimer is a timer of a room, shared by the players in it.
type roomTimer struct {
	timer
	by *session // who started it: on_timer runs for them while they are in the room
}

type roomTimerKey struct{ room, name string }

// roomTimerID is a room timer's id on the wire. Timer names are like item
// ids, which never hold a colon, so it never meets a player timer's id.
func roomTimerID(name string) string { return "room:" + name }

// timerMessage is what the client shows for the room timer t named name:
// its countdown, or nothing (removed) when t is nil or secret.
func (w *World) timerMessage(name string, t *roomTimer) ws.Message {
	m := ws.Message{Type: "timer", ID: roomTimerID(name)}
	if t != nil && t.show != "" {
		m.Title, m.Ms = t.show, max(t.deadline.Sub(w.now()).Milliseconds(), 1)
	}
	return m
}

// StartRoomTimer, StopRoomTimer and RoomTimerLeft implement script.Party.
// They run during a hook, under w.mu. The players in the room hear of a
// shown timer's change once the hook ends (announceTimers).
func (s *session) StartRoomTimer(name string, d time.Duration, show string) error {
	w, room := s.w, s.room
	ts := w.roomTimers[room]
	old := ts[name]
	if old == nil && len(ts) >= MaxRoomTimers {
		return errors.New("more than " + strconv.Itoa(MaxRoomTimers) + " room timers running")
	}
	if old != nil {
		old.stop()
	}
	if ts == nil {
		ts = map[string]*roomTimer{}
		w.roomTimers[room] = ts
	}
	t := &roomTimer{timer: timer{deadline: w.now().Add(d), show: show, readOnly: s.readOnlyWorld}, by: s}
	t.stop = w.after(d, func() { w.fireRoom(room, name, t) })
	ts[name] = t
	if show != "" || (old != nil && old.show != "") {
		w.timerNews[roomTimerKey{room, name}] = struct{}{}
	}
	return nil
}

func (s *session) StopRoomTimer(name string) bool {
	w := s.w
	t := w.roomTimers[s.room][name]
	if t == nil {
		return false
	}
	t.stop()
	w.dropRoomTimer(s.room, name)
	if t.show != "" {
		w.timerNews[roomTimerKey{s.room, name}] = struct{}{}
	}
	return true
}

func (s *session) RoomTimerLeft(name string) (time.Duration, bool) {
	t := s.w.roomTimers[s.room][name]
	if t == nil {
		return 0, false
	}
	return t.deadline.Sub(s.w.now()), true
}

// dropRoomTimer forgets the room's timer name. The caller holds w.mu.
func (w *World) dropRoomTimer(room, name string) {
	delete(w.roomTimers[room], name)
	if len(w.roomTimers[room]) == 0 {
		delete(w.roomTimers, room)
	}
}

// announceTimers tells the players in the rooms of the room timers the
// running hook changed what they show now: one message per timer however
// often it changed, s's own in its output, the others' pushed. The caller
// holds w.mu.
func (w *World) announceTimers(s *session) {
	if len(w.timerNews) == 0 {
		return
	}
	keys := slices.SortedFunc(maps.Keys(w.timerNews), func(a, b roomTimerKey) int {
		return cmp.Or(cmp.Compare(a.room, b.room), cmp.Compare(a.name, b.name))
	})
	clear(w.timerNews)
	if w.closed {
		return
	}
	for _, k := range keys {
		m := w.timerMessage(k.name, w.roomTimers[k.room][k.name])
		for _, p := range w.inRoom(k.room) {
			if p == s {
				s.out = append(s.out, m)
			} else {
				p.push([]ws.Message{m})
			}
		}
	}
}

// showRoomTimers tells the player who moves from room old to room to
// that old's shown timers are gone for them, and shows them to's. The
// caller holds w.mu.
func (s *session) showRoomTimers(old, to string) {
	for _, name := range slices.Sorted(maps.Keys(s.w.roomTimers[old])) {
		if s.w.roomTimers[old][name].show != "" {
			s.out = append(s.out, s.w.timerMessage(name, nil))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(s.w.roomTimers[to])) {
		if t := s.w.roomTimers[to][name]; t.show != "" {
			s.out = append(s.out, s.w.timerMessage(name, t))
		}
	}
}

// fireRoom runs on_timer(player, name, "room") once the room timer t ran
// out, unless it was stopped or replaced meanwhile: for the player who
// started it while they are in the room, else for the player there
// longest. In an empty room it is dropped. When that player's address has
// no Lua time left, it tries again a little later.
func (w *World) fireRoom(room, name string, t *roomTimer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.roomTimers[room][name] != t {
		return
	}
	w.dropRoomTimer(room, name)
	target := t.by
	if !target.here(room) {
		target = nil
		if list := w.inRoom(room); len(list) > 0 {
			target = list[0]
		}
	}
	if target == nil {
		return // nobody is there to see it go
	}
	mark := len(target.out)
	if t.show != "" { // gone for everyone, before what on_timer says
		m := w.timerMessage(name, nil)
		for _, p := range w.inRoom(room) {
			if p == target {
				target.out = append(target.out, m)
			} else {
				p.push([]ws.Message{m})
			}
		}
	}
	if !target.runPushed(script.Hook{Name: "on_timer", Args: []string{name, "room"}, ReadOnlyWorld: t.readOnly}) {
		target.out = target.out[:mark]
		if w.roomTimers[room] == nil {
			w.roomTimers[room] = map[string]*roomTimer{}
		}
		w.roomTimers[room][name] = t
		t.stop = w.after(retryBusy, func() { w.fireRoom(room, name, t) })
	}
}

// stopRoomTimers stops every room timer. The caller holds w.mu.
func (w *World) stopRoomTimers() {
	for _, ts := range w.roomTimers {
		for _, t := range ts {
			t.stop()
		}
	}
	clear(w.roomTimers)
}
