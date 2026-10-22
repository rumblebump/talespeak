package game

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of room.go (T-0022) in sessions: a target room that cannot
// start or run, room.go from on_enter at the start, the move limit, closing
// worlds and concurrent players.

// limitedRooms is roomsWorld with limits and options: rooms a and b run
// the given scripts, c has no script and one item.
func limitedRooms(t *testing.T, a, b string, limits script.Limits, opts ...Option) *World {
	t.Helper()
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml":     {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: a\n")},
		"rooms/a.yaml":  {Data: []byte("id: a\ntitle: A\ndescription: Room A.\nscript: a.lua\nitems:\n  - {id: box, label: a box}\n")},
		"rooms/b.yaml":  {Data: []byte("id: b\ntitle: B\ndescription: Room B.\nscript: b.lua\nitems:\n  - {id: rope, label: a rope}\n")},
		"rooms/c.yaml":  {Data: []byte("id: c\ntitle: C\ndescription: Room C.\nitems:\n  - {id: key, label: a key}\n")},
		"scripts/a.lua": {Data: []byte(a)},
		"scripts/b.lua": {Data: []byte(b)},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, limits, quiet, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// stepClock is a fake clock: every reading moves it on by step, so each
// hook call "takes" step.
type stepClock struct {
	t    time.Time
	step time.Duration
}

func (c *stepClock) now() time.Time {
	t := c.t
	c.t = c.t.Add(c.step)
	return t
}

// edgeA grows its state on "grow" and goes to any other room typed.
const edgeA = `
keep = {}
entered = 0
function on_enter(p) entered = entered + 1 say("A " .. entered .. " " .. p.room) end
function on_click(p, id) say("leaving A") room.go("b") end
function on_input(p, text)
  if text == "grow" then keep[#keep + 1] = string.rep("x", 1e6) .. #keep say("kept")
  else room.go(text) end
end`

// edgeB counts its on_enter calls, so a test sees whether it ran. top is
// put in front, for a top level that keeps memory.
func edgeB(top string) string {
	return top + `
entered = 0
function on_enter(p) entered = entered + 1 say("B " .. p.room) end
function on_input(p, text)
  if text == "back" then room.go("a") else say("entered " .. entered) end
end`
}

func roomOf(s ws.Session) string { return s.(*session).room }

// stayedAfterRefusedMove checks a move into b that was refused, as
// docs/lua-sandbox.md says: the old room's output, then one error, no
// items message for b, and the player still in a with a's items. Once b can
// start (free has made room), the same click moves the player and b's
// on_enter runs: want is how often it has run then.
func stayedAfterRefusedMove(t *testing.T, s ws.Session, got []ws.Message, wantErr string, free func(), want int) {
	t.Helper()
	if !strings.HasSuffix(kinds(got), "error:") || !strings.Contains(got[len(got)-1].Message, wantErr) {
		t.Fatalf("refused move = %q, want an error with %q last", texts(got), wantErr)
	}
	if strings.Contains(texts(got), "items:") {
		t.Errorf("refused move told the client b's items: %q", texts(got))
	}
	ss := s.(*session)
	if ss.room != "a" || len(ss.items) != 1 || ss.items[0].ID != "box" || ss.next != "" {
		t.Fatalf("after the refused move: room %q, items %+v, next %q; want the player in a", ss.room, ss.items, ss.next)
	}
	free()
	if got := texts(s.Handle(ws.Message{Type: "click", Item: "box"})); got != "leaving A | items:rope | B b" {
		t.Fatalf("the move once b can start = %q", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "x"})); got != fmt.Sprintf("entered %d", want) {
		t.Errorf("b's on_enter count = %q, want %d", got, want)
	}
}

// A move whose room cannot start, or whose on_enter cannot run because the
// player's address used up its Lua time, leaves the player in a (the first
// version left them in b, whose on_enter then never ran).
func TestRoomGoTargetRefused(t *testing.T) {
	big := script.Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 6 << 20, StateMemory: 6 << 20}
	raiseShare := func(w *World) func() {
		return func() {
			w.mu.Lock()
			w.clientMemory = DefaultClientMemory
			w.mu.Unlock()
		}
	}
	busy := func(t *testing.T, visits int) {
		w := limitedRooms(t, edgeA, edgeB(""), script.DefaultLimits)
		clk := &stepClock{t: time.Unix(1e9, 0)}
		w.now = clk.now
		s, _ := w.NewSession("192.0.2.1", ws.Who{})
		defer s.Close()
		for range visits {
			for _, m := range []ws.Message{{Type: "click", Item: "box"}, {Type: "input", Text: "back"}} {
				if got := s.Handle(m); strings.Contains(kinds(got), "error") {
					t.Fatalf("%+v = %q", m, texts(got))
				}
			}
		}
		// The click "takes" 3 s of the 2 s burst: on a first visit b's
		// instance is refused, on a later one its on_enter.
		clk.step = 3 * time.Second
		got := s.Handle(ws.Message{Type: "click", Item: "box"})
		clk.step = 0
		stayedAfterRefusedMove(t, s, got, busyMessage, func() { clk.t = clk.t.Add(time.Hour) }, visits+1)
	}
	t.Run("client time used up, first visit", func(t *testing.T) { busy(t, 0) })
	t.Run("client time used up, later visit", func(t *testing.T) { busy(t, 1) })

	t.Run("client memory share used up (ErrClientFull)", func(t *testing.T) {
		w := limitedRooms(t, edgeA, edgeB(""), script.DefaultLimits, WithClientMemory(1))
		s, _ := w.NewSession("192.0.2.1", ws.Who{})
		defer s.Close()
		if got := texts(s.Handle(ws.Message{Type: "input", Text: "grow"})); got != "kept" {
			t.Fatalf("grow = %q", got)
		}
		got := s.Handle(ws.Message{Type: "click", Item: "box"})
		stayedAfterRefusedMove(t, s, got, "your network", raiseShare(w), 1)
	})

	t.Run("client memory share used up at the start", func(t *testing.T) {
		w := limitedRooms(t, `x = string.rep("x", 1e6) .. "!"
function on_enter() say("A") room.go("b") end
function on_click() say("leaving A") room.go("b") end`, edgeB(""), script.DefaultLimits, WithClientMemory(1))
		s, first := w.NewSession("192.0.2.1", ws.Who{})
		defer s.Close()
		if got := texts(first); !strings.HasPrefix(got, "A | error") {
			t.Errorf("first = %q", got)
		}
		stayedAfterRefusedMove(t, s, first, "your network", raiseShare(w), 1)
	})

	t.Run("world memory used up (ErrFull)", func(t *testing.T) {
		w := limitedRooms(t, edgeA, edgeB(`big = {} for i = 1, 3 do big[i] = string.rep("y", 1e6) .. i end`), big)
		other, _ := w.NewSession("", ws.Who{})
		for range 4 { // 4 MB of the 6 MiB; b's top level keeps 3 MB more
			if got := texts(other.Handle(ws.Message{Type: "input", Text: "grow"})); got != "kept" {
				t.Fatalf("grow = %q", got)
			}
		}
		s, _ := w.NewSession("", ws.Who{})
		defer s.Close()
		got := s.Handle(ws.Message{Type: "click", Item: "box"})
		stayedAfterRefusedMove(t, s, got, "full", other.Close, 1)
	})
}

// A move undone because the address is busy keeps the items the old room's
// hook set with ui.items, not the old room's YAML items.
func TestRoomGoBusyKeepsScriptItems(t *testing.T) {
	w := limitedRooms(t, `function on_click(p, id) if id == "box" then ui.items({{id = "lever", label = "a lever"}}) room.go("b") else say(id) end end`,
		edgeB(""), script.DefaultLimits)
	clk := &stepClock{t: time.Unix(1e9, 0)}
	w.now = clk.now
	s, _ := w.NewSession("192.0.2.1", ws.Who{})
	defer s.Close()
	for _, m := range []ws.Message{{Type: "click", Item: "box"}, {Type: "input", Text: "back"}} {
		s.Handle(m) // b has an instance now
	}
	clk.step = 3 * time.Second
	got := s.Handle(ws.Message{Type: "click", Item: "box"})
	clk.step, clk.t = 0, clk.t.Add(time.Hour)
	if texts(got) != "items:lever | error" || roomOf(s) != "a" {
		t.Fatalf("busy move = %q, room %q", texts(got), roomOf(s))
	}
	if got := texts(s.Handle(ws.Message{Type: "click", Item: "lever"})); got != "lever" {
		t.Errorf("lever after the undone move = %q", got)
	}
}

// A target room whose on_enter keeps too much is reset, not the room the
// player came from: going back finds a's globals.
func TestRoomGoTargetStateMemoryReset(t *testing.T) {
	limits := script.Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 2 << 20, StateMemory: 64 << 20}
	w := limitedRooms(t, edgeA, `keep = {}
function on_enter() for i = 1, 3 do keep[i] = string.rep("z", 1e6) .. i end say("B") end
function on_input(p, text) say("B has " .. #keep) room.go(text) end`, limits)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	got := s.Handle(ws.Message{Type: "click", Item: "box"})
	if !strings.HasSuffix(kinds(got), "error:") || !strings.Contains(got[len(got)-1].Message, "limits") {
		t.Fatalf("click = %q", texts(got))
	}
	ss := s.(*session)
	if ss.envs["b"] != nil || ss.envs["a"] == nil {
		t.Errorf("after b's reset: a kept = %v, b kept = %v; want only b's instance freed", ss.envs["a"] != nil, ss.envs["b"] != nil)
	}
	// b starts again with a new instance; a's counter is where it was.
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "a"})); got != "B has 0 | items:box | A 2 a" {
		t.Errorf("back to a = %q", got)
	}
}

// A hook that calls room.go and then keeps too much fails, so it moves no
// one, and its own room is reset.
func TestRoomGoFailingHookStateMemory(t *testing.T) {
	limits := script.Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 2 << 20, StateMemory: 64 << 20}
	w := limitedRooms(t, `keep = {}
function on_click() room.go("b") for i = 1, 3 do keep[i] = string.rep("z", 1e6) .. i end end
function on_input() say("A has " .. #keep) end`, edgeB(""), limits)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	got := s.Handle(ws.Message{Type: "click", Item: "box"})
	if kinds(got) != "error:" {
		t.Fatalf("click = %q", texts(got))
	}
	if roomOf(s) != "a" || s.(*session).next != "" {
		t.Errorf("room = %q, next = %q; want a failing hook to move no one", roomOf(s), s.(*session).next)
	}
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "x"})); got != "A has 0" {
		t.Errorf("a after its reset = %q", got)
	}
}

// room.go from on_enter during NewSession moves the new player at once:
// the first messages hold both rooms' output, and the second room's items.
func TestRoomGoAtStart(t *testing.T) {
	tests := []struct{ to, want string }{
		{"b", "A | items:rope | B b"},
		{"c", "A | items:key | room:c"},
		{"a", "A | items:box | A"}, // the second on_enter does not move again
	}
	for _, tt := range tests {
		t.Run(tt.to, func(t *testing.T) {
			w := limitedRooms(t, `n = 0 function on_enter() n = n + 1 say("A") if n == 1 then room.go("`+tt.to+`") end end`, edgeB(""), script.DefaultLimits)
			s, first := w.NewSession("192.0.2.1", ws.Who{})
			defer s.Close()
			if got := texts(first); got != tt.want {
				t.Fatalf("first = %q, want %q", got, tt.want)
			}
			if roomOf(s) != tt.to {
				t.Errorf("room = %q", roomOf(s))
			}
			if s.(*session).moves != 1 {
				t.Errorf("moves = %d", s.(*session).moves)
			}
		})
	}
}

// player.room is the room the hook runs in: the hook that calls room.go
// still sees its own room after the call, the new room's on_enter its own.
func TestPlayerRoomAcrossMoves(t *testing.T) {
	w := limitedRooms(t, `function on_input(p, text) room.go(text) say("still " .. p.room) end
function on_enter(p) say("A " .. p.room) end`,
		`function on_enter(p) say("B " .. p.room) end
function on_click(p, id) say(id .. " in " .. p.room) end
function on_input(p, text) local s = ui.ask("?") say(s .. " in " .. p.room) room.go(s) end`, script.DefaultLimits)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	steps := []struct {
		m    ws.Message
		want string
	}{
		{ws.Message{Type: "input", Text: "b"}, "still a | items:rope | B b"},
		{ws.Message{Type: "click", Item: "rope"}, "rope in b"},
		{ws.Message{Type: "input", Text: "x"}, "ask"},
		{ws.Message{Type: "answer", ID: "d1", Text: "a"}, "a in b | items:box | A a"},
	}
	for _, st := range steps {
		if got := texts(s.Handle(st.m)); got != st.want {
			t.Errorf("%+v = %q, want %q", st.m, got, st.want)
		}
	}
}

// A move replaces items set with ui.items in the old room.
func TestRoomGoReplacesScriptItems(t *testing.T) {
	w := limitedRooms(t, `function on_click() ui.items({{id = "lever", label = "a lever"}}) room.go("b") end`, edgeB(""), script.DefaultLimits)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := texts(s.Handle(ws.Message{Type: "click", Item: "box"})); got != "items:lever | items:rope | B b" {
		t.Fatalf("click = %q", got)
	}
	if got := s.Handle(ws.Message{Type: "click", Item: "lever"}); !isError(got) {
		t.Errorf("old room's ui.items after the move = %+v", got)
	}
}

// An answer to ui.ask naming a room that does not exist fails the hook:
// the question is closed and the player stays.
func TestRoomGoUnknownAfterAnswer(t *testing.T) {
	w := limitedRooms(t, `function on_input() room.go(ui.ask("Where?")) end`, edgeB(""), script.DefaultLimits)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := kinds(s.Handle(ws.Message{Type: "input", Text: "x"})); got != "ask:d1" {
		t.Fatalf("input = %q", got)
	}
	if got := s.Handle(ws.Message{Type: "answer", ID: "d1", Text: "attic"}); kinds(got) != "error:" {
		t.Errorf("answer attic = %+v", got)
	}
	if roomOf(s) != "a" || s.(*session).pending != nil {
		t.Errorf("room = %q, pending = %v", roomOf(s), s.(*session).pending != nil)
	}
	if got := s.Handle(ws.Message{Type: "answer", ID: "d1", Text: "b"}); !isError(got) {
		t.Errorf("answer after the failed hook = %+v", got)
	}
}

// One message moves through MaxMoves rooms, not one more, and the count
// starts again with each message.
func TestRoomGoMaxMovesBoundary(t *testing.T) {
	w := limitedRooms(t, `
function on_input(p, text) left = tonumber(text) room.go("a") end
function on_enter() if left and left > 1 then left = left - 1 room.go("a") end end`, edgeB(""), script.DefaultLimits)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	for _, tt := range []struct {
		moves int
		fails bool
	}{{1, false}, {MaxMoves, false}, {MaxMoves + 1, true}, {MaxMoves, false}, {100, true}} {
		got := s.Handle(ws.Message{Type: "input", Text: fmt.Sprint(tt.moves)})
		failed := strings.HasSuffix(kinds(got), "error:")
		if failed != tt.fails {
			t.Errorf("%d moves: failed = %v, want %v (%q)", tt.moves, failed, tt.fails, texts(got))
		}
		if n := strings.Count(kinds(got), "items:"); n != min(tt.moves, MaxMoves) {
			t.Errorf("%d moves: %d items messages", tt.moves, n)
		}
		if roomOf(s) != "a" || s.(*session).next != "" {
			t.Errorf("%d moves: room %q, next %q", tt.moves, roomOf(s), s.(*session).next)
		}
	}
}

// A world that closed refuses moves: the player stays, and nothing runs.
func TestRoomGoWorldClosed(t *testing.T) {
	w := limitedRooms(t, edgeA, edgeB(""), script.DefaultLimits)
	s, _ := w.NewSession("", ws.Who{})
	waiting, _ := w.NewSession("", ws.Who{})
	if got := texts(waiting.Handle(ws.Message{Type: "input", Text: "b"})); got != "items:rope | B b" {
		t.Fatalf("waiting = %q", got)
	}
	w.Close()
	for _, m := range []ws.Message{{Type: "click", Item: "box"}, {Type: "input", Text: "b"}} {
		got := s.Handle(m)
		if kinds(got) != "error:" || !strings.Contains(got[0].Message, "closed") {
			t.Errorf("%+v = %+v", m, got)
		}
		if roomOf(s) != "a" {
			t.Errorf("%+v moved the player to %q", m, roomOf(s))
		}
	}
	if got := waiting.Handle(ws.Message{Type: "input", Text: "back"}); kinds(got) != "error:" || roomOf(waiting) != "b" {
		t.Errorf("back = %+v, room %q", got, roomOf(waiting))
	}
	s.Close()
	waiting.Close()
	// A new player on a closed world gets the error, not a half started room.
	ns, first := w.NewSession("", ws.Who{})
	defer ns.Close()
	if kinds(first) != "error:" {
		t.Errorf("NewSession on a closed world = %+v", first)
	}
}

// Players moving between rooms at once, some sharing a client budget, while
// the world closes under them: no race, no panic, no hang; every message
// gets an answer, and after the close every answer is an error.
func TestConcurrentMovesAndClose(t *testing.T) {
	w := limitedRooms(t, edgeA, edgeB(""), script.DefaultLimits, WithClientBudget(time.Second, time.Hour))
	const players, rounds = 8, 60
	var ready, done sync.WaitGroup
	ready.Add(players)
	done.Add(players)
	errs := make(chan error, players)
	for i := range players {
		go func() {
			defer done.Done()
			var once sync.Once
			markReady := func() { once.Do(ready.Done) }
			defer markReady() // also when a player stops early
			client := ""
			if i%2 == 1 {
				client = "192.0.2.1"
			}
			s, first := w.NewSession(client, ws.Who{})
			defer s.Close()
			if len(first) == 0 {
				errs <- fmt.Errorf("player %d: no first messages", i)
			}
			msgs := []ws.Message{{Type: "click", Item: "box"}, {Type: "input", Text: "back"}, {Type: "input", Text: "b"}, {Type: "input", Text: "back"}}
			closed := false
			for r := range rounds {
				if r == rounds/3 {
					markReady()
				}
				m := msgs[r%len(msgs)]
				got := s.Handle(m)
				if len(got) == 0 {
					errs <- fmt.Errorf("player %d round %d: no answer to %+v", i, r, m)
					return
				}
				last := got[len(got)-1]
				if closed && last.Type != "error" {
					errs <- fmt.Errorf("player %d round %d: %q after the world closed", i, r, texts(got))
					return
				}
				if last.Type == "error" && strings.Contains(last.Message, "closed") {
					closed = true
				}
				if room := roomOf(s); room != "a" && room != "b" && room != "c" {
					errs <- fmt.Errorf("player %d: room %q", i, room)
					return
				}
			}
		}()
	}
	ready.Wait()
	w.Close()
	finished := make(chan struct{})
	go func() { done.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("players still running 30 s after the world closed")
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// FuzzRoomGoSession sends any typed line to a room that goes where it is
// told: a room of the pack moves the player there with its items first;
// anything else is an error that leaves the player where they were.
func FuzzRoomGoSession(f *testing.F) {
	for _, s := range []string{"a", "b", "c", " b ", "attic", "", "\x00b", "b\n", strings.Repeat("a", 65), "B", "../b"} {
		f.Add(s)
	}
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml":     {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: a\n")},
		"rooms/a.yaml":  {Data: []byte("id: a\ntitle: A\ndescription: Room A.\nscript: a.lua\nitems:\n  - {id: box, label: a box}\n")},
		"rooms/b.yaml":  {Data: []byte("id: b\ntitle: B\ndescription: Room B.\nscript: b.lua\nitems:\n  - {id: rope, label: a rope}\n")},
		"rooms/c.yaml":  {Data: []byte("id: c\ntitle: C\ndescription: Room C.\nitems:\n  - {id: key, label: a key}\n")},
		"scripts/a.lua": {Data: []byte(`function on_input(p, text) room.go(text) end`)},
		"scripts/b.lua": {Data: []byte(`function on_enter(p) say("B") end`)},
	})
	if err != nil {
		f.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(w.Close)
	f.Fuzz(func(t *testing.T, text string) {
		s, _ := w.NewSession("", ws.Who{})
		defer s.Close()
		got := s.Handle(ws.Message{Type: "input", Text: text})
		line := cleanInput(text)
		switch {
		case len(text) > MaxInputBytes || line == "":
			if roomOf(s) != "a" {
				t.Fatalf("%q moved the player to %q", text, roomOf(s))
			}
		case p.Rooms[line] != nil:
			if roomOf(s) != line || len(got) == 0 || got[0].Type != "items" {
				t.Fatalf("%q: room %q, out %q", text, roomOf(s), texts(got))
			}
		default:
			if kinds(got) != "error:" || roomOf(s) != "a" {
				t.Fatalf("%q: room %q, out %q", text, roomOf(s), texts(got))
			}
		}
		if s.(*session).next != "" {
			t.Fatalf("%q: next = %q after the message", text, s.(*session).next)
		}
		if n := w.rt.Paused(); n != 0 {
			t.Fatalf("%d hooks paused", n)
		}
	})
}
