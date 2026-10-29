package game

import (
	"context"
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The game master's commands for the world's time (T-0012): /pause and
// /resume, /extratime and /mintime here, /timers, /extend and /cancel in
// timertools.go. Like the other GM commands they run with w.mu held,
// only after command has checked GM mode, and write the audit log before
// they act.

// timeCommand runs one of the time commands.
func (s *session) timeCommand(name, args string) []ws.Message {
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	switch name {
	case "pause":
		return s.pauseCommand(true)
	case "resume":
		return s.pauseCommand(false)
	case "timers":
		return s.timersCommand()
	case "extend":
		return s.extendCommand(args)
	case "cancel":
		return s.cancelCommand(args)
	case "extratime":
		return s.extraTimeCommand(args)
	default:
		return s.minTimeCommand(args)
	}
}

// pauseCommand pauses the world (on) or resumes it. Every player hears
// of it with a mark, and their client stops its countdowns; on resume it
// gets them again with the time left.
func (s *session) pauseCommand(on bool) []ws.Message {
	w := s.w
	if paused := !w.pausedAt.IsZero(); paused == on {
		if on {
			return []ws.Message{notice("The game is paused already. /resume goes on.")}
		}
		return []ws.Message{notice("The game is not paused.")}
	}
	if s.slowDown() {
		return []ws.Message{notice(slowDownMessage)}
	}
	action, text := "resume", "resumed the game."
	if on {
		action, text = "pause", "paused the game: timers and questions wait, and so does everything players do."
	}
	if err := s.audit(action, ""); err != nil {
		return []ws.Message{{Type: "error", Message: auditFailed}}
	}
	if on {
		w.pause()
	} else {
		w.resume()
	}
	mark := s.gmNotice(text)
	var mine []ws.Message
	for _, p := range w.online() {
		ms := []ws.Message{pauseMessage(on), mark}
		if !on {
			ms = append(ms, p.countdowns()...)
		}
		if p == s {
			mine = ms
		} else {
			p.push(ms)
		}
	}
	return append(mine, s.flush()...) // the save keeps the pause through a restart
}

// countdowns are the player's shown countdowns with the time left: their
// timers, their room's and the open question's. The caller holds w.mu.
func (s *session) countdowns() []ws.Message {
	var out []ws.Message
	for _, name := range slices.Sorted(maps.Keys(s.timers)) {
		if t := s.timers[name]; t.show != "" {
			out = append(out, s.timerMessage(name, t))
		}
	}
	ts := s.w.roomTimers[s.room]
	for _, name := range slices.Sorted(maps.Keys(ts)) {
		if t := ts[name]; t.show != "" {
			out = append(out, s.w.timerMessage(name, t))
		}
	}
	if s.dialogTimer != nil {
		out = append(out, s.questionCountdown())
	}
	return out
}

// timerMessage is what the client shows for the player's timer t named
// name: its countdown, or nothing (removed) when t is nil or secret. The
// caller holds w.mu.
func (s *session) timerMessage(name string, t *timer) ws.Message {
	m := ws.Message{Type: "timer", ID: name}
	if t != nil && t.show != "" {
		m.Title, m.Ms = t.show, max(t.deadline.Sub(s.w.clockNow(t.game)).Milliseconds(), 1)
	}
	return m
}

// questionTimerID is the id on the wire of the countdown of the question
// with dialog id: like a room timer's, it never meets a player timer's.
func questionTimerID(id string) string { return "question:" + id }

// questionCountdown tells the client the time left to answer the open
// question, or that it has no time limit any more. The caller holds w.mu.
func (s *session) questionCountdown() ws.Message {
	m := ws.Message{Type: "timer", ID: questionTimerID(s.dialogID)}
	if t := s.dialogTimer; t != nil {
		m.Ms = max(t.deadline.Sub(s.w.clockNow(false)).Milliseconds(), 1)
	}
	return m
}

// extraTimeCommand sets a member's extra-time factor ("/extratime ann
// 1.5"), or lists the online players' factors. It counts for questions
// and shown timers that start from then on.
func (s *session) extraTimeCommand(args string) []ws.Message {
	w := s.w
	fields := strings.Fields(args)
	if len(fields) == 0 {
		var b strings.Builder
		b.WriteString("Extra time of the players online (1 is none; /extratime NAME 1.5 changes it):")
		for _, p := range w.online() {
			b.WriteString("\n" + p.name + " " + factorText(p.extra))
		}
		return []ws.Message{notice(b.String())}
	}
	f, ok := parseFactor(fields[len(fields)-1])
	if len(fields) < 2 || !ok {
		return []ws.Message{notice("Type /extratime, a player's name and a factor from 1 to 3, such as /extratime ann 1.5.")}
	}
	name := strings.Join(fields[:len(fields)-1], " ") // account names have no spaces; players without one ("traveler 2") may
	if w.store == nil && !slices.ContainsFunc(w.online(), func(p *session) bool { return strings.EqualFold(p.name, name) }) {
		return []ws.Message{notice("There is no player " + strconv.Quote(name) + " here. /players lists them.")}
	}
	if err := s.audit("extra time", name+" "+factorText(f)); err != nil {
		return []ws.Message{{Type: "error", Message: auditFailed}}
	}
	var account int64
	if w.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
		var err error
		account, err = w.store.SetExtraTime(ctx, w.worldID, name, f)
		cancel()
		if errors.Is(err, store.ErrNotFound) {
			return []ws.Message{notice("There is no member " + strconv.Quote(name) + " in this world.")}
		}
		if err != nil {
			w.log.Error("extra time not written", "world", w.worldID, "err", err)
			return []ws.Message{{Type: "error", Message: "The server cannot change that right now. Try again in a moment."}}
		}
	}
	for p := range w.sessions {
		if (account != 0 && p.account == account) || (w.store == nil && strings.EqualFold(p.name, name)) {
			p.extra = f
		}
	}
	return []ws.Message{notice(name + " has " + factorText(f) + " times as long now, for questions and shown timers that start from now on.")}
}

// parseFactor reads an extra-time factor: a number from 1 to 3, kept to
// two decimals.
func parseFactor(text string) (float64, bool) {
	if len(text) > maxCommandNumber {
		return 0, false
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(f) || f < store.MinExtraTime || f > store.MaxExtraTime {
		return 0, false
	}
	return math.Round(f*100) / 100, true
}

// maxCommandNumber bounds a number or a time typed after a command, so
// parsing never sees much.
const maxCommandNumber = 20

func factorText(f float64) string {
	return strconv.FormatFloat(max(f, 1), 'f', -1, 64)
}

// minTimeCommand sets the world's minimum deadline ("/mintime 30s",
// "/mintime off"), or says what it is. Question timeouts and shown timers
// that start from then on and are shorter get that long; secret timers,
// which packs use for their rules, keep their time.
func (s *session) minTimeCommand(args string) []ws.Message {
	w := s.w
	if args == "" {
		if w.minTime == 0 {
			return []ws.Message{notice("This world has no minimum time. /mintime 30s gives every question and shown timer at least 30 seconds.")}
		}
		return []ws.Message{notice("Questions and shown timers here give at least " + durationText(w.minTime) + ". /mintime off ends that.")}
	}
	var d time.Duration
	if strings.ToLower(args) != "off" {
		var ok bool
		if d, ok = parseDuration(args); !ok || d > MaxMinTime {
			return []ws.Message{notice("Type /mintime and a time of 1s to 1h, such as /mintime 30s, or /mintime off.")}
		}
	}
	detail := "off"
	if d > 0 {
		detail = durationText(d)
	}
	if err := s.audit("mintime", detail); err != nil {
		return []ws.Message{{Type: "error", Message: auditFailed}}
	}
	w.minTime, w.clockDirty = d, true
	text := "This world has no minimum time any more."
	if d > 0 {
		text = "Questions and shown timers that start from now on give at least " + durationText(d) + "."
	}
	return append([]ws.Message{notice(text)}, s.flush()...)
}

// parseDuration reads a time a game master types: "90" (seconds), "30s",
// "5m", "1h", "1m30s", "1.5m". It is at least a second and at most a
// timer's longest run, in whole milliseconds.
func parseDuration(text string) (time.Duration, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" || len(text) > maxCommandNumber {
		return 0, false
	}
	var d time.Duration
	if n, err := strconv.Atoi(text); err == nil {
		if n < script.MinTimerSeconds || n > script.MaxTimerSeconds {
			return 0, false
		}
		d = time.Duration(n) * time.Second
	} else {
		var err error
		if d, err = time.ParseDuration(text); err != nil {
			return 0, false
		}
	}
	d = d.Round(time.Millisecond)
	if d < script.MinTimerSeconds*time.Second || d > script.MaxTimerSeconds*time.Second {
		return 0, false
	}
	return d, true
}

// durationText writes d for a game master, in whole seconds: "45s",
// "5m", "1m30s", "2h5m".
func durationText(d time.Duration) string {
	d = max(d, 0).Round(time.Second)
	h, m, sec := int(d/time.Hour), int(d%time.Hour/time.Minute), int(d%time.Minute/time.Second)
	var b strings.Builder
	if h > 0 {
		b.WriteString(strconv.Itoa(h) + "h")
	}
	if m > 0 {
		b.WriteString(strconv.Itoa(m) + "m")
	}
	if sec > 0 || b.Len() == 0 {
		b.WriteString(strconv.Itoa(sec) + "s")
	}
	return b.String()
}
