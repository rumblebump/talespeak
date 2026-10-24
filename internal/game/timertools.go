package game

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The game master's timer tools (T-0012): /timers lists the timers that
// run in the world with numbers, and /extend and /cancel act on a number
// from the list the game master saw last. A number whose timer stopped or
// changed meanwhile is refused, so a list read a while ago never hits
// another timer. Listed are the rooms' timers and those of the players
// online with their open questions; a player who is away keeps theirs in
// the store, where nobody changes them.

// maxTimersListed bounds the list /timers shows and remembers.
const maxTimersListed = 50

// timerRef is one timer of a /timers list.
type timerRef struct {
	kind string   // store.TimerRoom, store.TimerPlayer or store.TimerQuestion
	p    *session // whose timer or question; nil for a room timer
	room string   // a room timer's room
	name string   // a timer's name
	id   string   // a question's dialog id
	t    *timer
	rt   *roomTimer
}

// timerRefs lists the world's timers: the rooms' by room and name, then
// each online player's by name, with their open question. The caller
// holds w.mu.
func (w *World) timerRefs() []timerRef {
	var list []timerRef
	for _, room := range slices.Sorted(maps.Keys(w.roomTimers)) {
		for _, name := range slices.Sorted(maps.Keys(w.roomTimers[room])) {
			rt := w.roomTimers[room][name]
			list = append(list, timerRef{kind: store.TimerRoom, room: room, name: name, t: &rt.timer, rt: rt})
		}
	}
	for _, p := range w.online() {
		for _, name := range slices.Sorted(maps.Keys(p.timers)) {
			list = append(list, timerRef{kind: store.TimerPlayer, p: p, name: name, t: p.timers[name]})
		}
		if p.dialogTimer != nil {
			list = append(list, timerRef{kind: store.TimerQuestion, p: p, id: p.dialogID, t: p.dialogTimer})
		}
	}
	return list
}

// live reports whether r still names the timer it named when it was
// listed. The caller holds w.mu.
func (w *World) live(r timerRef) bool {
	switch r.kind {
	case store.TimerRoom:
		return w.roomTimers[r.room][r.name] == r.rt
	case store.TimerPlayer:
		return !r.p.closed && r.p.timers[r.name] == r.t
	default:
		return !r.p.closed && r.p.dialogTimer == r.t && r.p.dialogID == r.id
	}
}

// what names r's timer for the game master and the audit log.
func (s *session) what(r timerRef) string {
	label := "secret timer " + r.name
	if r.t.show != "" {
		label = strconv.Quote(r.t.show) + " (" + r.name + ")"
	}
	switch r.kind {
	case store.TimerRoom:
		return "room " + s.roomTitle(r.room) + ": " + label
	case store.TimerPlayer:
		return r.p.name + ": " + label
	default:
		return r.p.name + "'s question " + strconv.Quote(shorten(r.p.pending.Dialog.Title, maxMarkArgs))
	}
}

// leftText says how long r's timer has left.
func (s *session) leftText(r timerRef) string {
	w := s.w
	if r.rt != nil && r.rt.due {
		return "ran out, waits for a player to come in"
	}
	text := durationText(r.t.deadline.Sub(w.clockNow(r.t.game))) + " left"
	if r.t.game {
		text += " in game time"
	}
	return text
}

func (s *session) timersCommand() []ws.Message {
	w := s.w
	list := w.timerRefs()
	if len(list) == 0 {
		s.timerList = nil
		return []ws.Message{notice("No timers run here now.")}
	}
	var b strings.Builder
	b.WriteString("Timers (/extend N 5m gives timer N more time, /cancel N stops it):")
	if !w.pausedAt.IsZero() {
		b.WriteString("\nThe game is paused: they wait for /resume.")
	}
	more := len(list) - maxTimersListed
	list = list[:min(len(list), maxTimersListed)]
	for i, r := range list {
		b.WriteString("\n" + strconv.Itoa(i+1) + ". " + s.what(r) + ", " + s.leftText(r))
	}
	if more > 0 {
		b.WriteString("\n… and " + strconv.Itoa(more) + " more.")
	}
	s.timerList = list
	return []ws.Message{notice(b.String())}
}

// pickTimer reads the number of a timer of the game master's last
// /timers list; a message to answer when it names none that runs.
func (s *session) pickTimer(text string) (timerRef, []ws.Message) {
	n, err := strconv.Atoi(text)
	if len(text) > maxCommandNumber || err != nil || n < 1 || n > len(s.timerList) {
		return timerRef{}, []ws.Message{notice("Type /timers first, then a number from its list.")}
	}
	r := s.timerList[n-1]
	if !s.w.live(r) {
		return timerRef{}, []ws.Message{notice("Timer " + strconv.Itoa(n) + " is not running as listed any more. /timers lists them again.")}
	}
	return r, nil
}

// extendCommand gives a timer more time: "/extend 3 5m".
func (s *session) extendCommand(args string) []ws.Message {
	w := s.w
	num, amount, _ := strings.Cut(args, " ")
	r, refused := s.pickTimer(num)
	if refused != nil {
		return refused
	}
	d, ok := parseDuration(amount)
	if !ok {
		return []ws.Message{notice("Type /extend, the timer's number and a time, such as /extend " + num + " 5m.")}
	}
	now := w.clockNow(r.t.game)
	at := r.t.deadline.Add(d)
	if r.t.deadline.Before(now) { // ran out, and waits for Lua time or a player
		at = now.Add(d)
	}
	if at.Sub(now) > script.MaxTimerSeconds*time.Second {
		return []ws.Message{notice("A timer runs at most a day.")}
	}
	if s.slowDown() {
		return []ws.Message{notice(slowDownMessage)}
	}
	if err := s.audit("extend timer", s.what(r)+" +"+durationText(d)); err != nil {
		return []ws.Message{{Type: "error", Message: auditFailed}}
	}
	r.t.deadline = at
	if r.rt != nil {
		r.rt.due = false
	}
	w.arm(r.t)
	mark := "gave a secret timer " + durationText(d) + " more."
	if r.t.show != "" {
		mark = "gave " + durationText(d) + " more: " + strconv.Quote(r.t.show) + "."
	}
	switch r.kind {
	case store.TimerRoom:
		w.noteRoomTimer(r.room, r.name, r.rt)
		if r.t.show != "" {
			w.timerNews[roomTimerKey{r.room, r.name}] = struct{}{}
		}
		s.markRoom(r.room, mark)
	case store.TimerPlayer:
		r.p.noteTimer(r.name, r.t)
		s.tell(r.p, r.p.timerMessage(r.name, r.t), s.gmNotice(mark))
	default:
		if _, kept := r.p.questions[r.p.asked]; kept && r.p.asked != (questionKey{}) {
			r.p.questions[r.p.asked] = at
			w.noteTimer(store.Timer{Kind: store.TimerQuestion, Character: r.p.char, Room: r.p.asked.room, Name: r.p.asked.name, Deadline: at})
		}
		s.tell(r.p, r.p.questionCountdown(), s.gmNotice("gave "+durationText(d)+" more to answer."))
	}
	s.out = append(s.out, notice("Done: "+s.leftText(r)+"."))
	return s.flush()
}

// cancelCommand stops a timer without its on_timer: "/cancel 3". A
// question's time limit goes, and the question waits for its answer.
func (s *session) cancelCommand(args string) []ws.Message {
	w := s.w
	r, refused := s.pickTimer(strings.TrimSpace(args))
	if refused != nil {
		return refused
	}
	if s.slowDown() {
		return []ws.Message{notice(slowDownMessage)}
	}
	what := s.what(r)
	if err := s.audit("cancel timer", what); err != nil {
		return []ws.Message{{Type: "error", Message: auditFailed}}
	}
	r.t.stop()
	mark := "stopped a secret timer."
	if r.t.show != "" {
		mark = "stopped " + strconv.Quote(r.t.show) + "."
	}
	switch r.kind {
	case store.TimerRoom:
		w.dropRoomTimer(r.room, r.name)
		if r.t.show != "" {
			w.timerNews[roomTimerKey{r.room, r.name}] = struct{}{}
		}
		s.markRoom(r.room, mark)
	case store.TimerPlayer:
		delete(r.p.timers, r.name)
		r.p.noteTimer(r.name, nil)
		s.tell(r.p, r.p.timerMessage(r.name, nil), s.gmNotice(mark))
	default:
		r.p.dialogTimer = nil
		if r.p.asked != (questionKey{}) {
			r.p.dropQuestion(r.p.asked)
			r.p.asked = questionKey{}
		}
		s.tell(r.p, r.p.questionCountdown(), s.gmNotice("took the time limit off the question."))
	}
	s.out = append(s.out, notice("Stopped: "+what+"."))
	return s.flush()
}

// tell gives p messages about what the game master s did: s in its
// output, anyone else pushed. The caller holds w.mu.
func (s *session) tell(p *session, ms ...ws.Message) {
	if p == s {
		s.out = append(s.out, ms[:len(ms)-1]...) // the game master needs no mark of their own
		return
	}
	p.push(ms)
}

// markRoom shows the players in room, other than s, what s did to its
// timers; the countdowns follow with announceTimers. The caller holds
// w.mu.
func (s *session) markRoom(room, text string) {
	s.tellRoom(room, s.gmNotice(text))
}
