package game

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Timers that last (T-0012). With a store, a player's timers, the
// deadlines of the questions they were asked and the rooms' timers are
// written with the save of the hook that changed them, deadlines
// absolute. Room timers come back when the world loads, a player's own
// when they join, so a reload or a restart only pauses what the player
// sees, never the clock: a timer that ran out meanwhile fires at once.

// timerKey tells one lasting timer from another; char is 0 for a room
// timer.
type timerKey struct {
	kind string
	char int64
	room string
	name string
}

// noteTimer notes a timer change for the next save: the latest change of
// each timer is written. The caller holds w.mu.
func (w *World) noteTimer(t store.Timer) {
	if w.store == nil {
		return
	}
	k := timerKey{t.Kind, t.Character, t.Room, t.Name}
	if t.Kind == store.TimerRoom {
		k.char = 0
	}
	w.timerSaves[k] = t
}

// noteTimer notes that the player's timer name started or changed (t), or
// stopped or ran out (nil). The caller holds w.mu.
func (s *session) noteTimer(name string, t *timer) {
	if s.char == 0 {
		return
	}
	st := store.Timer{Kind: store.TimerPlayer, Character: s.char, Name: name, Delete: t == nil}
	if t != nil {
		st.Deadline, st.Show, st.ReadOnly = t.deadline, t.show, t.readOnly
	}
	s.w.noteTimer(st)
}

// noteRoomTimer notes that the room timer name of room started or
// changed (t), or stopped or ran out (nil). The caller holds w.mu.
func (w *World) noteRoomTimer(room, name string, t *roomTimer) {
	st := store.Timer{Kind: store.TimerRoom, Room: room, Name: name, Delete: t == nil}
	if t != nil {
		st.Character, st.Deadline, st.Show, st.ReadOnly = t.byChar, t.deadline, t.show, t.readOnly
	}
	w.noteTimer(st)
}

// timerChanges lists the noted timer changes in a fixed order. The caller
// holds w.mu.
func (w *World) timerChanges() []store.Timer {
	keys := slices.SortedFunc(maps.Keys(w.timerSaves), func(a, b timerKey) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.char, b.char), cmp.Compare(a.room, b.room), cmp.Compare(a.name, b.name))
	})
	list := make([]store.Timer, len(keys))
	for i, k := range keys {
		list[i] = w.timerSaves[k]
	}
	return list
}

// writeTimers writes the noted timer changes on their own, before a
// player's timers are read. The caller holds w.mu.
func (w *World) writeTimers(ctx context.Context) error {
	if len(w.timerSaves) == 0 {
		return nil
	}
	if err := w.store.Save(ctx, store.Save{World: w.worldID, Timers: w.timerChanges()}); err != nil {
		return err
	}
	clear(w.timerSaves)
	return nil
}

// lastingDeadline is a stored deadline, brought forward to at most a
// timer's longest run from now, so a clock set back cannot keep a timer
// waiting for longer than a pack can ask for.
func lastingDeadline(at, now time.Time) time.Time {
	if latest := now.Add(script.MaxTimerSeconds * time.Second); at.After(latest) {
		return latest
	}
	return at
}

// validTimer reports whether a stored timer could have been started:
// its name, its label and its room. Others are dropped.
func (w *World) validTimer(t store.Timer) bool {
	return script.ValidItemID(t.Name) && (t.Show == "" || script.ValidLabel(t.Show)) && (t.Kind == store.TimerPlayer || w.pack.Rooms[t.Room] != nil)
}

// restoreRoomTimers starts the room timers the world had, from the store,
// as the world loads. The caller holds w.mu, or the world is new.
func (w *World) restoreRoomTimers(list []store.Timer) {
	now := w.now()
	for _, st := range list {
		room, name := st.Room, st.Name
		if !w.validTimer(st) || len(w.roomTimers[room]) >= MaxRoomTimers || w.roomTimers[room][name] != nil {
			w.log.Warn("dropping a stored room timer", "world", w.worldID, "room", room, "name", name)
			w.noteRoomTimer(room, name, nil)
			continue
		}
		t := &roomTimer{timer: timer{deadline: lastingDeadline(st.Deadline, now), show: st.Show, readOnly: st.ReadOnly}, byChar: st.Character}
		if w.roomTimers[room] == nil {
			w.roomTimers[room] = map[string]*roomTimer{}
		}
		w.roomTimers[room][name] = t
		t.stop = w.after(max(0, t.deadline.Sub(now)), func() { w.fireRoom(room, name, t) })
	}
}

// restoreTimers starts the player's timers and keeps the deadlines of the
// questions they were asked, from the store, as they join; the shown
// timers' countdowns go into their first output. The caller holds w.mu.
func (s *session) restoreTimers(list []store.Timer) {
	w := s.w
	now := w.now()
	for _, st := range list {
		switch {
		case st.Kind == store.TimerQuestion && validQuestion(st.Name) && w.pack.Rooms[st.Room] != nil && len(s.questions) < maxQuestions:
			if s.questions == nil {
				s.questions = map[questionKey]time.Time{}
			}
			s.questions[questionKey{st.Room, st.Name}] = lastingDeadline(st.Deadline, now)
		case st.Kind == store.TimerPlayer && w.validTimer(st) && len(s.timers) < script.MaxTimers && s.timers[st.Name] == nil:
			name := st.Name
			t := &timer{deadline: lastingDeadline(st.Deadline, now), show: st.Show, readOnly: st.ReadOnly}
			if s.timers == nil {
				s.timers = map[string]*timer{}
			}
			s.timers[name] = t
			t.stop = w.after(max(0, t.deadline.Sub(now)), func() { s.fire(name, t) })
			if t.show != "" {
				s.out = append(s.out, ws.Message{Type: "timer", ID: name, Title: t.show, Ms: max(t.deadline.Sub(now).Milliseconds(), 1)})
			}
		default:
			w.log.Warn("dropping a stored timer", "world", w.worldID, "character", s.char, "kind", st.Kind, "name", st.Name)
			w.noteTimer(store.Timer{Kind: st.Kind, Character: s.char, Room: st.Room, Name: st.Name, Delete: true})
		}
	}
}

// wakeRoom fires the timers of room that ran out while nobody was there,
// now that a player came in: after their on_enter, since fireRoom waits
// for w.mu. The caller holds w.mu.
func (w *World) wakeRoom(room string) {
	for _, name := range slices.Sorted(maps.Keys(w.roomTimers[room])) {
		if t := w.roomTimers[room][name]; t.due {
			t.due = false
			t.stop = w.after(0, func() { w.fireRoom(room, name, t) })
		}
	}
}

// Question deadlines. A question with a timeout keeps its deadline while
// its hook waits; when the hook is cut off (the player reloads, the
// server restarts), the deadline stays with the character, and the next
// time the same room asks the same question (kind, title, options), it
// gets only the time left, or times out at once. An answer, a timeout or
// a question closed by the game (a move, /reset) forgets the deadline.

// maxQuestions bounds the question deadlines one character keeps: those
// of questions cut off and not asked again. Past it the one that runs
// out first goes.
const maxQuestions = script.MaxTimers

// questionKey is a question asked in a room: its room and questionName.
type questionKey struct{ room, name string }

// questionName tells a question from another: a hash of its kind, title,
// options and their bounds, as 32 hex digits.
func questionName(d script.Dialog) string {
	h := sha256.New()
	field := func(s string) {
		h.Write(binary.AppendUvarint(nil, uint64(len(s))))
		h.Write([]byte(s))
	}
	field(strconv.FormatBool(d.Multiple) + strconv.FormatBool(d.Ask) + strconv.FormatBool(d.Strict))
	field(strconv.Itoa(d.Min) + "-" + strconv.Itoa(d.Max))
	field(d.Title)
	for _, o := range d.Options {
		field(o)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// validQuestion reports whether name can be a questionName.
func validQuestion(name string) bool {
	_, err := hex.DecodeString(name)
	return len(name) == 32 && err == nil
}

// questionTimeout is how long the player has to answer d, the question
// just asked: its timeout, or with a store at most the time left of the
// same question cut off before. The caller holds w.mu.
func (s *session) questionTimeout(d script.Dialog) time.Duration {
	if s.char == 0 {
		return d.Timeout
	}
	now := s.w.now()
	k := questionKey{s.room, questionName(d)}
	at, ok := s.questions[k]
	if !ok || at.After(now.Add(d.Timeout)) {
		at = now.Add(d.Timeout)
	}
	if !ok && len(s.questions) >= maxQuestions {
		first := slices.MinFunc(slices.Collect(maps.Keys(s.questions)), func(a, b questionKey) int {
			return cmp.Or(s.questions[a].Compare(s.questions[b]), cmp.Compare(a.room, b.room), cmp.Compare(a.name, b.name))
		})
		s.dropQuestion(first)
	}
	if s.questions == nil {
		s.questions = map[questionKey]time.Time{}
	}
	s.questions[k], s.asked = at, k
	s.w.noteTimer(store.Timer{Kind: store.TimerQuestion, Character: s.char, Room: k.room, Name: k.name, Deadline: at})
	return at.Sub(now)
}

// forgetQuestion forgets the deadline of the open question, which was
// answered, timed out or closed. The caller holds w.mu.
func (s *session) forgetQuestion() {
	if s.asked != (questionKey{}) {
		s.dropQuestion(s.asked)
		s.asked = questionKey{}
	}
}

// dropQuestion forgets the deadline of question k. The caller holds w.mu.
func (s *session) dropQuestion(k questionKey) {
	delete(s.questions, k)
	s.w.noteTimer(store.Timer{Kind: store.TimerQuestion, Character: s.char, Room: k.room, Name: k.name, Delete: true})
}
