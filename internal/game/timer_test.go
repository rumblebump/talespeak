package game

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// fakeClock runs timers when a test moves it forward, never by waiting.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at   time.Time
	f    func()
	done bool // fired or stopped
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := !t.done
		t.done = true
		return was
	}
}

// Advance moves the clock by d and runs the timers due meanwhile, the
// earliest first, each with the clock at its deadline.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	end := c.now.Add(d)
	for {
		var next *fakeTimer
		for _, t := range c.timers {
			if !t.done && !t.at.After(end) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			break
		}
		next.done = true
		c.now = next.at
		c.mu.Unlock()
		next.f()
		c.mu.Lock()
	}
	c.now = end
	c.mu.Unlock()
}

// running is how many timers are waiting.
func (c *fakeClock) running() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.done {
			n++
		}
	}
	return n
}

// clockWorld is roomsWorld on a fake clock.
func clockWorld(t *testing.T, a, b string, opts ...Option) (*World, *fakeClock) {
	t.Helper()
	p, err := pack.Load(roomsFS(a, b))
	if err != nil {
		t.Fatal(err)
	}
	c := newClock()
	w, err := NewWorld(p, script.DefaultLimits, quiet, append([]Option{WithClock(c.Now, c.After)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w, c
}

// pending takes what was pushed to s.
func pending(t *testing.T, s ws.Session) []ws.Message {
	t.Helper()
	ms, ok := s.(ws.Pusher).Pending()
	if !ok {
		t.Fatal("session lagged")
	}
	return ms
}

const timerRoom = `
function on_enter(p)
  say("in a")
end
function on_input(p, text)
  if text == "start" then say(tostring(timer.start("guards", 60, {show = "Guards arrive"})))
  elseif text == "secret" then say(tostring(timer.start("guards", 60)))
  elseif text == "short" then say(tostring(timer.start("guards", 5, {show = "Hurry"})))
  elseif text == "cancel" then say(tostring(timer.cancel("guards")))
  elseif text == "left" then say(tostring(timer.left("guards")))
  elseif text == "ask" then
    local n = ui.dialog("Fight or flee?", {"Fight", "Flee"}, {timeout = 30})
    say("answer " .. tostring(n))
  elseif text == "go" then room.go("b")
  elseif text == "many" then
    for i = 1, 11 do
      local ok, err = timer.start("t" .. i, 10)
      if not ok then say(err) end
    end
  end
end
function on_timer(p, name)
  say("a: time is up for " .. name)
end`

const timerRoomB = `
function on_enter(p) say("in b") end
function on_timer(p, name) say("b: " .. name) end`

func TestTimerShownRunsOut(t *testing.T) {
	w, c := clockWorld(t, timerRoom, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	got := s.Handle(ws.Message{Type: "input", Text: "start"})
	if len(got) != 2 || got[0].Type != "timer" || got[0].ID != "guards" || got[0].Title != "Guards arrive" || got[0].Ms != 60000 || got[1].Text != "true" {
		t.Fatalf("start: %+v", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "left"})); got != "60" {
		t.Errorf("left = %s", got)
	}
	c.Advance(59 * time.Second)
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("before the deadline: %+v", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "left"})); got != "1" {
		t.Errorf("left = %s", got)
	}
	c.Advance(time.Second)
	got = pending(t, s)
	if len(got) != 2 || got[0].Type != "timer" || got[0].ID != "guards" || got[0].Ms != 0 || got[1].Text != "a: time is up for guards" {
		t.Fatalf("at the deadline: %+v", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "left"})); got != "nil" {
		t.Errorf("left after = %s", got)
	}
}

func TestTimerSecretCancelReplace(t *testing.T) {
	w, c := clockWorld(t, timerRoom, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := s.Handle(ws.Message{Type: "input", Text: "secret"}); texts(got) != "true" {
		t.Fatalf("a secret timer sends no countdown: %+v", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "cancel"})); got != "true" {
		t.Errorf("cancel = %s", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "cancel"})); got != "false" {
		t.Errorf("cancel again = %s", got)
	}
	c.Advance(2 * time.Minute)
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("a canceled timer fired: %+v", got)
	}

	// Starting it again replaces it: only the new deadline counts, and a
	// shown timer replaced by a secret one leaves the client's countdown.
	s.Handle(ws.Message{Type: "input", Text: "start"})
	if got := s.Handle(ws.Message{Type: "input", Text: "short"}); len(got) != 2 || got[0].Ms != 5000 {
		t.Fatalf("restart: %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "input", Text: "secret"}); len(got) != 2 || got[0].Type != "timer" || got[0].Ms != 0 {
		t.Fatalf("shown to secret: %+v", got)
	}
	c.Advance(2 * time.Minute)
	if got := texts(pending(t, s)); got != "a: time is up for guards" {
		t.Fatalf("fired = %s", got)
	}
	if n := c.running(); n != 0 {
		t.Errorf("%d timers still running", n)
	}
}

// on_timer runs in the room the player is in when the timer runs out.
func TestTimerRunsInCurrentRoom(t *testing.T) {
	w, c := clockWorld(t, timerRoom, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	s.Handle(ws.Message{Type: "input", Text: "secret"})
	s.Handle(ws.Message{Type: "input", Text: "go"})
	c.Advance(time.Minute)
	if got := texts(pending(t, s)); got != "b: guards" {
		t.Errorf("fired = %s", got)
	}
}

func TestTimerLimit(t *testing.T) {
	w, _ := clockWorld(t, timerRoom, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "many"})); got != "timer.start: more than 10 timers running" {
		t.Errorf("11 timers: %s", got)
	}
}

// A timer running out while a question is open: on_timer runs, cannot ask
// another question, and the open one stays answerable.
func TestTimerWhileQuestionOpen(t *testing.T) {
	w, c := clockWorld(t, timerRoom, `
function on_enter(p) say("in b") end
function on_timer(p, name)
  say("b: " .. name)
  if name == "ask" then ui.dialog("Another?", {"Yes"}) end
  if name == "move" then room.go("a") end
end
function on_input(p, text)
  timer.start(text, 10)
  local n = ui.dialog("Stay?", {"Yes", "No"})
  say("stay " .. n)
end`)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	s.Handle(ws.Message{Type: "input", Text: "go"})

	got := s.Handle(ws.Message{Type: "input", Text: "say"})
	if kinds(got) != "dialog:d1" {
		t.Fatalf("question: %+v", got)
	}
	c.Advance(10 * time.Second)
	if got := texts(pending(t, s)); got != "b: say" {
		t.Fatalf("on_timer while asking: %s", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "stay 1" {
		t.Fatalf("answer after the timer: %s", got)
	}

	s.Handle(ws.Message{Type: "input", Text: "ask"})
	c.Advance(10 * time.Second)
	if got := texts(pending(t, s)); got != "b: ask | error" {
		t.Fatalf("on_timer asking while a question is open: %s", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "choose", ID: "d2", Option: 2})); got != "stay 2" {
		t.Fatalf("the open question after: %s", got)
	}

	// room.go from on_timer closes the open question.
	s.Handle(ws.Message{Type: "input", Text: "move"})
	c.Advance(10 * time.Second)
	got = pending(t, s)
	if texts(got) != "b: move | close | items:box | in a" || got[1].ID != "d3" {
		t.Fatalf("on_timer moving: %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "choose", ID: "d3", Option: 1}); kinds(got) != "error:" {
		t.Errorf("the closed question: %+v", got)
	}
}

func TestQuestionTimeout(t *testing.T) {
	w, c := clockWorld(t, timerRoom, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	got := s.Handle(ws.Message{Type: "input", Text: "ask"})
	if kinds(got) != "dialog:d1" || got[0].Ms != 30000 {
		t.Fatalf("question: %+v", got)
	}
	c.Advance(30 * time.Second)
	got = pending(t, s)
	if texts(got) != "close | answer nil" || got[0].ID != "d1" {
		t.Fatalf("timeout: %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}); kinds(got) != "error:" {
		t.Errorf("answer after the timeout: %+v", got)
	}

	// An answer in time stops the timeout.
	s.Handle(ws.Message{Type: "input", Text: "ask"})
	if got := texts(s.Handle(ws.Message{Type: "choose", ID: "d2", Option: 2})); got != "answer 2" {
		t.Fatalf("answer in time: %s", got)
	}
	c.Advance(time.Minute)
	if got := pending(t, s); len(got) != 0 {
		t.Errorf("after an answer in time: %+v", got)
	}
	if n := c.running(); n != 0 {
		t.Errorf("%d timers still running", n)
	}
}

// Closing the session or the world stops its timers; a late fire does
// nothing.
func TestTimersStopOnClose(t *testing.T) {
	w, c := clockWorld(t, timerRoom, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	s.Handle(ws.Message{Type: "input", Text: "start"})
	s.Handle(ws.Message{Type: "input", Text: "ask"})
	s.Close()
	if n := c.running(); n != 0 {
		t.Errorf("after Close: %d timers running", n)
	}

	s2, _ := w.NewSession("", ws.Who{})
	s2.Handle(ws.Message{Type: "input", Text: "start"})
	sess := s2.(*session)
	w.mu.Lock()
	fire := sess.timers["guards"]
	w.mu.Unlock()
	w.Close()
	if n := c.running(); n != 0 {
		t.Errorf("after the world closed: %d timers running", n)
	}
	sess.fire("guards", fire) // a timer that fired just as the world closed
	sess.expire("d1")
	if got := pending(t, s2); len(got) != 0 {
		t.Errorf("late fire: %+v", got)
	}
	s2.Close()
}

// A timer whose player's address used up its Lua time tries again later.
func TestTimerWaitsForBudget(t *testing.T) {
	w, c := clockWorld(t, timerRoom, timerRoomB, WithClientBudget(time.Second, time.Second))
	s, _ := w.NewSession("192.0.2.1", ws.Who{})
	defer s.Close()
	s.Handle(ws.Message{Type: "input", Text: "secret"})
	w.mu.Lock()
	w.budgets["192.0.2.1"].left = -60.25 // used up until 250 ms after the deadline
	w.mu.Unlock()
	c.Advance(time.Minute)
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("ran without budget: %+v", got)
	}
	w.mu.Lock()
	waiting := s.(*session).timers["guards"] != nil
	w.mu.Unlock()
	if !waiting {
		t.Error("the timer is gone while it waits for Lua time")
	}
	c.Advance(retryBusy)
	if got := texts(pending(t, s)); got != "a: time is up for guards" {
		t.Errorf("after the retry: %s", got)
	}
}

// A client that stops reading is cut off instead of queueing forever.
func TestPushedQueueOverflows(t *testing.T) {
	w, c := clockWorld(t, `
function on_input(p, text) for i = 1, 10 do timer.start("t" .. i, 1) end end
function on_timer(p, name) for i = 1, 60 do say(name) end timer.start(name, 1) end`, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	notified := 0
	s.(ws.Pusher).SetNotify(func() { notified++ })
	s.Handle(ws.Message{Type: "input", Text: "go"})
	c.Advance(time.Second)
	if notified == 0 {
		t.Fatal("never notified")
	}
	if _, ok := s.(ws.Pusher).Pending(); ok {
		t.Fatal("600 queued messages did not overflow")
	}
}

// Pushed output reaches the client over the WebSocket, in order with the
// replies.
func TestTimerOverWebSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	w, c := clockWorld(t, timerRoom, timerRoomB)
	srv := httptest.NewServer(ws.New("test", quiet, ws.WithGame(w)))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	read := func(n int) []ws.Message {
		var ms []ws.Message
		for range n {
			var m ws.Message
			if err := wsjson.Read(ctx, conn, &m); err != nil {
				t.Fatal(err)
			}
			ms = append(ms, m)
		}
		return ms
	}
	read(2) // welcome, "in a"
	if err := wsjson.Write(ctx, conn, ws.Message{Type: "input", Text: "short"}); err != nil {
		t.Fatal(err)
	}
	if got := read(2); got[0].Type != "timer" || got[1].Text != "true" {
		t.Fatalf("start: %+v", got)
	}
	c.Advance(5 * time.Second)
	got := read(2)
	if !slices.Equal([]string{got[0].Type, got[1].Text}, []string{"timer", "a: time is up for guards"}) {
		t.Fatalf("pushed: %+v", got)
	}
}
