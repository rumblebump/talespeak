package game

import (
	"context"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// packWorld is a world of the pack in fsys, closed when the test ends.
func packWorld(t *testing.T, fsys fstest.MapFS, opts ...Option) *World {
	t.Helper()
	p, err := pack.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// T-0029 and T-0030 edges: queue order and limits, timers and question
// timeouts racing answers and Close, mergeKeys, and on_change edges.

func TestMergeKeys(t *testing.T) {
	many := func(prefix string, n int) []string {
		var ks []string
		for i := range n {
			ks = append(ks, prefix+strconv.Itoa(i))
		}
		return ks
	}
	tests := []struct {
		name      string
		old, keys []string
		want      []string
	}{
		{"both empty", nil, nil, nil},
		{"into nothing", nil, []string{"a", "b"}, []string{"a", "b"}},
		{"nothing new", []string{"a"}, nil, []string{"a"}},
		{"a repeated key moves to the end", []string{"a", "b", "c"}, []string{"a"}, []string{"b", "c", "a"}},
		{"repeats within keys count once", nil, []string{"a", "b", "a"}, []string{"b", "a"}},
		{"cap keeps the latest", many("o", 40), many("n", 20), append(many("o", 40)[10:], many("n", 20)...)},
		{"exactly the cap", nil, many("k", MaxChangedKeys), many("k", MaxChangedKeys)},
		{"one past the cap", many("k", MaxChangedKeys), []string{"z"}, append(many("k", MaxChangedKeys)[1:], "z")},
		{"keys alone past the cap", nil, many("k", MaxChangedKeys+7), many("k", MaxChangedKeys+7)[7:]},
		{"a repeat at the cap keeps everything", many("k", MaxChangedKeys), []string{"k0"}, append(many("k", MaxChangedKeys)[1:], "k0")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := slices.Clone(tt.keys)
			got := mergeKeys(slices.Clone(tt.old), tt.keys)
			if !slices.Equal(got, tt.want) {
				t.Errorf("mergeKeys = %q, want %q", got, tt.want)
			}
			if len(got) > MaxChangedKeys {
				t.Errorf("%d keys, more than %d", len(got), MaxChangedKeys)
			}
			if !slices.Equal(tt.keys, keys) {
				t.Errorf("keys changed to %q", tt.keys)
			}
		})
	}
}

// changeRoom writes many world keys on demand and reports what on_change
// gets.
const changeRoom = `
function on_input(p, text)
  local n = tonumber(text)
  if n then
    for i = n, n + 29 do state.world.set("k" .. i, i) end
  elseif text == "drop" then
    state.world.update("k1", function(v) return nil end)
  elseif text == "drop-missing" then
    state.world.update("never", function(v) return nil end)
  elseif text == "same" then
    state.world.set("k1", state.world.get("k1"))
  elseif text == "fail" then
    state.world.set("half", true)
    error("broken after a write")
  elseif text == "ask" then
    state.world.set("asked", true)
    local n = ui.dialog("?", {"a"})
    state.world.set("answered", n)
  elseif text == "timer" then
    timer.start("w", 1)
  end
end
function on_timer(p, name) state.world.set("by-timer", name) say("timer wrote") end
function on_change(p, keys) say(#keys .. ": " .. table.concat(keys, ",")) end`

// A player waiting for Lua time while 60 keys change gets the latest 50.
func TestWorldChangedKeysCapped(t *testing.T) {
	w, c := clockWorld(t, changeRoom, timerRoomB, WithClientBudget(time.Second, time.Second))
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("192.0.2.3", ws.Who{})
	defer b.Close()
	w.mu.Lock()
	w.budgets["192.0.2.3"].left = -0.25
	w.mu.Unlock()
	a.Handle(ws.Message{Type: "input", Text: "1"})
	c.Advance(0)
	a.Handle(ws.Message{Type: "input", Text: "31"})
	c.Advance(retryBusy)
	var want []string
	for i := 11; i <= 60; i++ {
		want = append(want, "k"+strconv.Itoa(i))
	}
	if got := texts(pending(t, b)); got != "50: "+strings.Join(want, ",") {
		t.Errorf("on_change = %s", got)
	}
}

// What counts as a change: removing a key through update does, removing a
// missing one does not; a write before an error or a question does.
func TestWorldWhatAnnounces(t *testing.T) {
	tests := []struct {
		text, want string
	}{
		{"drop", "1: k1"},
		{"drop-missing", ""},
		{"fail", "1: half"},
		{"ask", "1: asked"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			w, c := clockWorld(t, changeRoom, timerRoomB)
			a, _ := w.NewSession("", ws.Who{})
			defer a.Close()
			b, _ := w.NewSession("", ws.Who{})
			defer b.Close()
			a.Handle(ws.Message{Type: "input", Text: "1"})
			c.Advance(0)
			pending(t, b)
			a.Handle(ws.Message{Type: "input", Text: tt.text})
			c.Advance(DeliverGap)
			if got := texts(pending(t, b)); got != tt.want {
				t.Errorf("on_change = %q, want %q", got, tt.want)
			}
		})
	}
}

// A write after a question announces when the hook ends; on_timer's
// writes announce too.
func TestWorldAnnouncesFromAnswersAndTimers(t *testing.T) {
	w, c := clockWorld(t, changeRoom, timerRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	a.Handle(ws.Message{Type: "input", Text: "ask"})
	c.Advance(0)
	pending(t, b)
	a.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})
	c.Advance(DeliverGap)
	if got := texts(pending(t, b)); got != "1: answered" {
		t.Errorf("after the answer: %q", got)
	}
	a.Handle(ws.Message{Type: "input", Text: "timer"})
	c.Advance(time.Second)
	if got := texts(pending(t, a)); got != "timer wrote" {
		t.Errorf("a's on_timer: %q", got)
	}
	c.Advance(DeliverGap)
	if got := texts(pending(t, b)); got != "1: by-timer" {
		t.Errorf("b after a's timer: %q", got)
	}
	if got := pending(t, a); len(got) != 0 {
		t.Errorf("the timer's writer got its own change: %+v", got)
	}
}

// A player who leaves between a change and its delivery is skipped; the
// others still get it, and a player who joins after the change does not.
func TestWorldChangeSessionLeavesBeforeDelivery(t *testing.T) {
	w, c := clockWorld(t, changeRoom, timerRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	d, _ := w.NewSession("", ws.Who{})
	defer d.Close()
	a.Handle(ws.Message{Type: "input", Text: "1"})
	b.Close()
	late, _ := w.NewSession("", ws.Who{})
	defer late.Close()
	c.Advance(0)
	if got := pending(t, b); len(got) != 0 {
		t.Errorf("the closed session: %+v", got)
	}
	if got := texts(pending(t, d)); !strings.HasPrefix(got, "30: k1,") {
		t.Errorf("the one who stayed: %q", got)
	}
	if got := pending(t, late); len(got) != 0 {
		t.Errorf("the one who joined after the change: %+v", got)
	}
	// The world closing before a delivery: nothing runs.
	a.Handle(ws.Message{Type: "input", Text: "31"})
	w.Close()
	c.Advance(time.Minute)
	if got := pending(t, d); len(got) != 0 {
		t.Errorf("after the world closed: %+v", got)
	}
}

// Pushed messages wait in order, and a reply carries what was pushed
// before it, first.
func TestPushedOrder(t *testing.T) {
	w, c := clockWorld(t, `
function on_input(p, text)
  if text == "two" then timer.start("first", 1) timer.start("second", 2) else say("reply") end
end
function on_timer(p, name) say(name) end`, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	s.Handle(ws.Message{Type: "input", Text: "two"})
	c.Advance(time.Second)
	c.Advance(time.Second)
	if got := texts(pending(t, s)); got != "first | second" {
		t.Errorf("pending = %q", got)
	}
	s.Handle(ws.Message{Type: "input", Text: "two"})
	c.Advance(2 * time.Second)
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "x"})); got != "first | second | reply" {
		t.Errorf("reply = %q", got)
	}
	if got := pending(t, s); len(got) != 0 {
		t.Errorf("pushed twice: %+v", got)
	}
}

// SetNotify tells at once about messages pushed before it, and about a
// session that lagged meanwhile.
func TestSetNotifyLate(t *testing.T) {
	w, c := clockWorld(t, `
function on_input(p, text) timer.start("t", 1) end
function on_timer(p, name) say(name) end`, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	p := s.(ws.Pusher)
	s.Handle(ws.Message{Type: "input", Text: "x"})
	c.Advance(time.Second)
	n := 0
	p.SetNotify(func() { n++ })
	if n != 1 {
		t.Errorf("SetNotify with a message waiting: notified %d times", n)
	}
	pending(t, s)

	quiet := 0
	p.SetNotify(func() { quiet++ })
	if quiet != 0 {
		t.Errorf("SetNotify with nothing waiting: notified %d times", quiet)
	}

	w.mu.Lock()
	s.(*session).lagged = true
	w.mu.Unlock()
	lag := 0
	p.SetNotify(func() { lag++ })
	if lag != 1 {
		t.Errorf("SetNotify on a lagged session: notified %d times", lag)
	}
	if _, ok := p.Pending(); ok {
		t.Error("a lagged session's Pending is ok")
	}
	// After lagging, nothing more is queued.
	s.Handle(ws.Message{Type: "input", Text: "x"})
	c.Advance(time.Second)
	if ms, _ := p.Pending(); len(ms) != 0 {
		t.Errorf("queued after lagging: %+v", ms)
	}
}

// A closed session queues nothing and notifies no one.
func TestPushAfterClose(t *testing.T) {
	w, _ := clockWorld(t, timerRoom, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	n := 0
	s.(ws.Pusher).SetNotify(func() { n++ })
	s.Close()
	w.mu.Lock()
	s.(*session).push([]ws.Message{{Type: "text", Text: "late"}})
	w.mu.Unlock()
	if ms := pending(t, s); len(ms) != 0 || n != 0 {
		t.Errorf("after Close: %+v, notified %d", ms, n)
	}
}

// A question's timeout and the player's answer arrive at once: exactly
// one of them wins, and the loser gets an error or nothing.
func TestExpireRacesAnswer(t *testing.T) {
	for i := range 30 {
		w, c := clockWorld(t, timerRoom, timerRoomB)
		s, _ := w.NewSession("", ws.Who{})
		if got := kinds(s.Handle(ws.Message{Type: "input", Text: "ask"})); got != "dialog:d1" {
			t.Fatalf("question: %s", got)
		}
		var reply []ws.Message
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); reply = s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}) }()
		go func() { defer wg.Done(); c.Advance(30 * time.Second) }()
		wg.Wait()
		// Either the answer ran (and the timeout found nothing to do), or
		// the timeout ran first and the answer was refused; a reply then
		// carries the pushed messages before its error.
		all := texts(append(reply, pending(t, s)...))
		if all != "answer 1" && all != "close | answer nil | error" {
			t.Fatalf("run %d: got %q: want exactly one winner", i, all)
		}
		if n := w.rt.Paused(); n != 0 {
			t.Fatalf("run %d: %d hooks paused", i, n)
		}
		s.Close()
		w.Close()
	}
}

// A timer or a timeout firing while the session or the world closes: no
// output, nothing left paused or running.
func TestFireRacesClose(t *testing.T) {
	for i := range 30 {
		w, c := clockWorld(t, timerRoom, timerRoomB)
		s, _ := w.NewSession("", ws.Who{})
		s.Handle(ws.Message{Type: "input", Text: "short"})
		s.Handle(ws.Message{Type: "input", Text: "ask"})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); c.Advance(time.Minute) }()
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				s.Close()
			} else {
				w.Close()
			}
		}()
		wg.Wait()
		s.Close()
		w.Close()
		c.Advance(time.Hour)
		if n := c.running(); n != 0 {
			t.Fatalf("run %d: %d timers running", i, n)
		}
		if n := w.rt.Paused(); n != 0 {
			t.Fatalf("run %d: %d hooks paused", i, n)
		}
		// Once closed, nothing more arrives.
		pending(t, s)
		c.Advance(time.Hour)
		if ms := pending(t, s); len(ms) != 0 {
			t.Fatalf("run %d: after close: %+v", i, ms)
		}
	}
}

// fastClock runs every timer of the world a thousand times sooner, on real
// goroutines, so timers race answers, moves and Close for real.
func fastClock() Option {
	return WithClock(time.Now, func(d time.Duration, f func()) func() bool {
		return time.AfterFunc(d/1000, f).Stop
	})
}

// Many players with timers, question timeouts and world writes at once,
// on real timer goroutines. Run with -race.
func TestConcurrentTimersAndWorld(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real timers")
	}
	p := roomsFS(timerRoom+`
function on_change(p, keys) say("changed " .. #keys) end`, timerRoomB)
	p["scripts/a.lua"].Data = append(p["scripts/a.lua"].Data, []byte(`
local base = on_input
function on_input(p, text)
  if text == "count" then
    state.world.update("count", function(n) return (n or 0) + 1 end)
    say("counted")
  elseif text == "get" then say(tostring(state.world.get("count")))
  else base(p, text) end
end`)...)
	w := packWorld(t, p, fastClock())
	const players, rounds = 6, 40
	var wg sync.WaitGroup
	var countMu sync.Mutex
	counted := 0
	for i := range players {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _ := w.NewSession("", ws.Who{})
			defer s.Close()
			pusher := s.(ws.Pusher)
			var mu sync.Mutex
			notified := 0
			pusher.SetNotify(func() { mu.Lock(); notified++; mu.Unlock() })
			for r := range rounds {
				switch (i + r) % 5 {
				case 0:
					s.Handle(ws.Message{Type: "input", Text: "short"})
				case 1:
					got := s.Handle(ws.Message{Type: "input", Text: "ask"})
					for _, m := range got {
						if m.Type == "dialog" && r%2 == 0 {
							s.Handle(ws.Message{Type: "choose", ID: m.ID, Option: 1})
						}
					}
				case 2:
					// Refused while a question is open: only what counted counts.
					if strings.Contains(texts(s.Handle(ws.Message{Type: "input", Text: "count"})), "counted") {
						countMu.Lock()
						counted++
						countMu.Unlock()
					}
				case 3:
					s.Handle(ws.Message{Type: "input", Text: "cancel"})
				case 4:
					if ms, ok := pusher.Pending(); !ok {
						t.Errorf("player %d lagged with %d messages", i, len(ms))
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	want := "nil"
	if counted > 0 {
		want = strconv.Itoa(counted)
	}
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "get"})); got != want {
		t.Errorf("count = %s, want %s", got, want)
	}
	w.mu.Lock()
	n := len(w.sessions)
	w.mu.Unlock()
	if n != 1 {
		t.Errorf("%d sessions left, want 1", n)
	}
	w.Close()
}

// on_change across players on the real clock: every other player's room
// hears every count, and the count is exact.
func TestRealClockWorldChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real timers")
	}
	w := packWorld(t, roomsFS(`
function on_input(p, text) state.world.update("count", function(n) return (n or 0) + 1 end) end
function on_change(p, keys) say(table.concat(keys, ",") .. " " .. state.world.get("count")) end`, timerRoomB))
	const players, rounds = 5, 20
	sessions := make([]ws.Session, players)
	for i := range sessions {
		sessions[i], _ = w.NewSession("", ws.Who{})
		defer sessions[i].Close()
	}
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				s.Handle(ws.Message{Type: "input", Text: "+"})
			}
		}()
	}
	wg.Wait()
	want := "count " + strconv.Itoa(players*rounds)
	atFull := 0
	for i, s := range sessions {
		var last string
		deadline := time.Now().Add(2 * time.Second)
		for !strings.HasSuffix(last, want) && time.Now().Before(deadline) {
			for _, m := range pending(t, s) {
				if m.Type != "text" {
					t.Errorf("player %d got %+v", i, m)
				}
				last = m.Text
			}
			if !strings.HasSuffix(last, want) {
				time.Sleep(time.Millisecond) // polling real timers, with a deadline
			}
		}
		if last == "" {
			t.Errorf("player %d heard no change", i)
		}
		if strings.HasSuffix(last, want) {
			atFull++
		}
	}
	// Only the player who wrote last may not hear the final count.
	if atFull < players-1 {
		t.Errorf("%d of %d players heard %q", atFull, players, want)
	}
}

// Two players over real WebSockets on the real clock: one opens the door,
// the other hears it without sending anything, and its next reply comes
// after it.
func TestWorldChangeOverWebSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	w := packWorld(t, roomsFS(worldRoom, timerRoomB))
	srv := httptest.NewServer(ws.New("test", quiet, ws.WithGame(w)))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func() *websocket.Conn {
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		var m ws.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil || m.Type != "welcome" {
			t.Fatalf("welcome: %+v, %v", m, err)
		}
		return conn
	}
	read := func(conn *websocket.Conn) ws.Message {
		var m ws.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b := dial(), dial()
	defer a.CloseNow()
	defer b.CloseNow()
	// roomsFS's room a has a script without on_enter: it says nothing on
	// arrival. Ping both so the push goroutines are surely running.
	for _, conn := range []*websocket.Conn{a, b} {
		if err := wsjson.Write(ctx, conn, ws.Message{Type: "ping"}); err != nil {
			t.Fatal(err)
		}
		if m := read(conn); m.Type != "pong" {
			t.Fatalf("pong: %+v", m)
		}
	}
	if err := wsjson.Write(ctx, a, ws.Message{Type: "input", Text: "open"}); err != nil {
		t.Fatal(err)
	}
	if m := read(a); m.Text != "true" {
		t.Fatalf("a's reply: %+v", m)
	}
	if m := read(b); m.Text != "changed: door" {
		t.Fatalf("b's push: %+v", m)
	}
	for _, want := range []string{"state.world.set: on_change cannot change world state", "state.world.update: on_change cannot change world state"} {
		if m := read(b); m.Text != want {
			t.Fatalf("b's push: %+v, want %q", m, want)
		}
	}
	if err := wsjson.Write(ctx, b, ws.Message{Type: "input", Text: "door"}); err != nil {
		t.Fatal(err)
	}
	if m := read(b); m.Text != "open" {
		t.Fatalf("b's reply: %+v", m)
	}
}
