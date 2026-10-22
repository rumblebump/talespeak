package game

import (
	"errors"
	"strconv"
	"time"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Output the player did not ask for: timers that ran out, questions whose
// timeout passed. It runs on the timer's goroutine under w.mu, is queued,
// and reaches the client through ws.Pusher.

// SetNotify implements ws.Pusher.
func (s *session) SetNotify(notify func()) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	s.notify = notify
	if len(s.queue) > 0 || s.lagged || s.replaced {
		notify()
	}
}

// Pending implements ws.Pusher.
func (s *session) Pending() ([]ws.Message, bool) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	q := s.queue
	s.queue, s.queueBytes = nil, 0
	return q, !s.lagged && !s.replaced
}

// wireBytes is about what m takes on the wire.
func wireBytes(m ws.Message) int {
	n := 64 + len(m.Text) + len(m.Title) + len(m.Message) + len(m.ID) + len(m.From) + len(m.URL)
	for _, o := range m.Options {
		n += len(o) + 3
	}
	for _, it := range m.Items {
		n += len(it.ID) + len(it.Label) + 24
	}
	return n
}

// push queues ms for the connection. A player whose client does not read
// fast enough gets nothing more, and the connection closes: a client that
// read everything takes one hook's output, however long. The caller holds
// w.mu.
func (s *session) push(ms []ws.Message) {
	if len(ms) == 0 || s.closed {
		return
	}
	n := 0
	for _, m := range ms {
		n += wireBytes(m)
	}
	if len(s.queue) > 0 && (len(s.queue)+len(ms) > MaxQueued || s.queueBytes+n > MaxQueuedBytes) {
		s.lagged, s.queue, s.queueBytes = true, nil, 0
	} else if !s.lagged {
		s.queue = append(s.queue, ms...)
		s.queueBytes += n
	}
	if s.notify != nil {
		s.notify()
	}
}

// runPushed runs h in the player's current room, for something the player
// did not send, and queues its output. A hook run while a question is open
// cannot ask another, and a read-only one (on_change) never asks; what it
// starts stays read-only. It reports false when the player's address has
// used up its Lua time and nothing ran. The caller holds w.mu.
func (s *session) runPushed(h script.Hook) bool {
	s.moves = 0
	s.readOnlyWorld = h.ReadOnlyWorld
	defer func() { s.readOnlyWorld = false }()
	env, err := s.env(s.room)
	if errors.Is(err, ErrBusy) {
		return false
	}
	if err != nil {
		s.push(s.after(nil, err))
		return true
	}
	if env == nil || !env.HasHook(h.Name) {
		s.push(s.flush())
		return true
	}
	h = s.hook(h)
	h.NoWait = s.pending != nil || h.ReadOnlyWorld
	th, err := s.run1(func() (*script.Thread, error) { return s.w.rt.RunHook(s, env, h) })
	if errors.Is(err, ErrBusy) {
		return false
	}
	s.push(s.after(th, err))
	return true
}

// StartTimer, StopTimer and TimerLeft implement script.Host. They run
// during a hook, under w.mu.
func (s *session) StartTimer(name string, d time.Duration, show string) error {
	old := s.timers[name]
	if old == nil && len(s.timers) >= script.MaxTimers {
		return errors.New("more than " + strconv.Itoa(script.MaxTimers) + " timers running")
	}
	if old != nil {
		old.stop()
		if old.show != "" && show == "" {
			s.out = append(s.out, ws.Message{Type: "timer", ID: name})
		}
	}
	if s.timers == nil {
		s.timers = map[string]*timer{}
	}
	t := &timer{deadline: s.w.now().Add(d), show: show, readOnly: s.readOnlyWorld}
	t.stop = s.w.after(d, func() { s.fire(name, t) })
	s.timers[name] = t
	if show != "" {
		s.out = append(s.out, ws.Message{Type: "timer", ID: name, Title: show, Ms: max(d.Milliseconds(), 1)})
	}
	return nil
}

func (s *session) StopTimer(name string) bool {
	t := s.timers[name]
	if t == nil {
		return false
	}
	t.stop()
	delete(s.timers, name)
	if t.show != "" {
		s.out = append(s.out, ws.Message{Type: "timer", ID: name})
	}
	return true
}

func (s *session) TimerLeft(name string) (time.Duration, bool) {
	t := s.timers[name]
	if t == nil {
		return 0, false
	}
	return t.deadline.Sub(s.w.now()), true
}

// stopTimers stops the player's timers and the open question's timeout.
// The caller holds w.mu.
func (s *session) stopTimers() {
	for _, t := range s.timers {
		t.stop()
	}
	s.timers = nil
	if s.dialogTimer != nil {
		s.dialogTimer()
		s.dialogTimer = nil
	}
}

// fire runs on_timer(player, name) once t ran out, unless it was stopped
// or replaced meanwhile. When the player's address has no Lua time left,
// it tries again a little later.
func (s *session) fire(name string, t *timer) {
	w := s.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || s.closed || s.timers[name] != t {
		return
	}
	delete(s.timers, name)
	mark := len(s.out)
	if t.show != "" {
		s.out = append(s.out, ws.Message{Type: "timer", ID: name})
	}
	if !s.runPushed(script.Hook{Name: "on_timer", Args: []string{name}, ReadOnlyWorld: t.readOnly}) {
		s.out = s.out[:mark]
		s.timers[name] = t
		t.stop = w.after(retryBusy, func() { s.fire(name, t) })
	}
}

// expireAfter starts the timeout of the question just opened. The caller
// holds w.mu.
func (s *session) expireAfter(d time.Duration) {
	id := s.dialogID
	s.dialogTimer = s.w.after(d, func() { s.expire(id) })
}

// expire ends the wait of question id once its timeout passed: the
// question returns nil in the hook, which goes on.
func (s *session) expire(id string) {
	w := s.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || s.closed || s.pending == nil || s.dialogID != id {
		return
	}
	s.moves = 0
	th := s.pending
	var next *script.Thread
	err := w.timed(s.client, func() (err error) {
		next, err = w.rt.Expire(s, th)
		return err
	})
	if errors.Is(err, ErrBusy) {
		s.dialogTimer = w.after(retryBusy, func() { s.expire(id) })
		return
	}
	s.out = append([]ws.Message{{Type: "close", ID: id}}, s.out...)
	s.unpend()
	s.push(s.after(next, err))
}
