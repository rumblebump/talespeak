package game

import (
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// The demo's guide Whisp, last actions before escaping, locked doors,
// room instances and refused state.

// whispAsks checks got is Whisp's question before the solved cell, with
// its two options, and returns its id.
func whispAsks(t *testing.T, step string, got []ws.Message) string {
	t.Helper()
	for _, m := range got {
		if m.Type != "dialog" {
			continue
		}
		if m.Title != "Do you really want to get locked in again?" || m.From != "Whisp" ||
			len(m.Options) != 2 || m.Options[0] != "Yes, go back" || m.Options[1] != "No, stay here" {
			t.Fatalf("%s: Whisp's question: %+v", step, m)
		}
		return m.ID
	}
	t.Fatalf("%s: no question from Whisp in %+v", step, got)
	return ""
}

// The cell is solved with ask, not once: Whisp asks every time the player
// turns back, and every no keeps the player in the corridor.
func TestDemoWhispAsksEveryTime(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)
	for i, m := range []ws.Message{{Type: "click", Item: "cell"}, {Type: "input", Text: "back"}, {Type: "click", Item: "cell"}} {
		_, got := play(t, s, m)
		id := whispAsks(t, "turn back", got)
		text, got := play(t, s, ws.Message{Type: "choose", ID: id, Option: 2})
		if text != "Whisp: Good. Onwards!" || roomOf(s) != "corridor" {
			t.Fatalf("no %d: %q, in %q", i+1, text, roomOf(s))
		}
		for _, g := range got {
			if g.Type == "room" || g.Type == "items" {
				t.Fatalf("no %d moved or changed items: %+v", i+1, got)
			}
		}
	}
	// Three refusals cost three actions: 4 left, the third from now warns.
	for i := 1; i <= leftAfterCell-3; i++ {
		text, _ := play(t, s, ws.Message{Type: "input", Text: "hmm"})
		if warned := strings.Contains(text, "Footsteps on the stairs"); warned != (i == leftAfterCell-4) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got := play(t, s, ws.Message{Type: "input", Text: "hmm"})
	wantText(t, "caught", text, "sees you standing in the corridor")
	died(t, s, text, got)
}

// Back in the cell, its open door leads out again, into the corridor as
// the player left it; the cell stays solved.
func TestDemoOutOfCellAgain(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)                                                 // 7 left
	play(t, s, ws.Message{Type: "click", Item: "stone"})             // 6
	backToCell(t, s, ws.Message{Type: "click", Item: "cell"})        // 5
	text, got := play(t, s, ws.Message{Type: "click", Item: "lock"}) // 4
	wantText(t, "out again", text, "You push the cell door open")
	wantText(t, "corridor again", text, "The torch still smokes. Down the stairs, water is still splashing.")
	if strings.Contains(text, "You are out!") || roomOf(s) != "corridor" {
		t.Fatalf("out again: %q, in %q", text, roomOf(s))
	}
	if items := lastItems(t, got); items["stone"] != "lifted stone" {
		t.Fatalf("corridor items: %v", items)
	}
	_, got = play(t, s, ws.Message{Type: "click", Item: "cell"}) // 3
	whispAsks(t, "cell again", got)
}

// The guard's countdown at 0 still means away (0 is true in Lua): the
// player can slip out on the very last action, and is caught on the next.
func TestDemoEscapeOnLastAction(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	sendAway(t, s)            // 10 left
	openLock(t, s)            // 9
	for i := 1; i <= 8; i++ { // 1
		text, _ := play(t, s, ws.Message{Type: "input", Text: "hmm"})
		if warned := strings.Contains(text, "footsteps are coming closer"); warned != (i == 8) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got := play(t, s, ws.Message{Type: "click", Item: "lock"}) // 0
	wantText(t, "out", text, "You are out!")
	if roomOf(s) != "corridor" {
		t.Fatalf("in %q", roomOf(s))
	}
	if items := lastItems(t, got); items["stone"] != "stone with an X" {
		t.Fatalf("corridor items: %v", items)
	}
	text, got = play(t, s, ws.Message{Type: "click", Item: "stone"})
	wantText(t, "caught", text, "sees you standing in the corridor")
	if strings.Contains(text, "BOX ") {
		t.Fatalf("the stone was lifted while caught: %q", text)
	}
	died(t, s, text, got)
}

// Turning back to the cell on the countdown's last action: Whisp's
// question costs no action of its own, and the guard is there on the next
// one, in whichever room the answer left the player.
func TestDemoWhispOnLastAction(t *testing.T) {
	cases := []struct {
		option     int
		room, want string
	}{
		{1, "cell", "He sees the open lock."},
		{2, "corridor", "sees you standing in the corridor"},
	}
	for _, c := range cases {
		t.Run(c.room, func(t *testing.T) {
			w := shippedDemo(t)
			s, _ := w.NewSession("", ws.Who{})
			defer s.Close()
			escapeCell(t, s)
			for range leftAfterCell - 1 {
				play(t, s, ws.Message{Type: "input", Text: "hmm"})
			}
			_, got := play(t, s, ws.Message{Type: "click", Item: "cell"}) // 0
			text, got := play(t, s, ws.Message{Type: "choose", ID: whispAsks(t, "last", got), Option: c.option})
			if strings.Contains(text, "SLAP!") || roomOf(s) != c.room {
				t.Fatalf("answer: %q, in %q", text, roomOf(s))
			}
			if c.room == "cell" {
				wantText(t, "cell", text, "Your old cell. Outside, the guard's stool is still empty.")
				if items := lastItems(t, got); items["guard"] != "empty stool" {
					t.Fatalf("cell items: %v", items)
				}
			}
			text, got = play(t, s, ws.Message{Type: "input", Text: "hmm"})
			wantText(t, "caught", text, c.want)
			if roomOf(s) != c.room {
				t.Fatalf("caught, then in %q", roomOf(s))
			}
			died(t, s, text, got)
		})
	}
}

// Coming back into the corridor, it says how close the guard is from the
// shared countdown.
func TestDemoCorridorAgainSaysHowClose(t *testing.T) {
	cases := []struct {
		waits int // actions in the corridor before the guard room
		want  string
	}{
		{0, "Down the stairs, water is still splashing."}, // back with 5 left
		{3, "Down the stairs, water is still splashing."}, // 2
		{4, "Footsteps are coming up the stairs!"},        // 1
		{5, "Footsteps are coming up the stairs!"},        // 0
	}
	for _, c := range cases {
		t.Run(strconv.Itoa(c.waits), func(t *testing.T) {
			w := shippedDemo(t)
			s, _ := w.NewSession("", ws.Who{})
			defer s.Close()
			escapeCell(t, s) // 7
			for range c.waits {
				play(t, s, ws.Message{Type: "input", Text: "hmm"})
			}
			play(t, s, ws.Message{Type: "click", Item: "door"})
			text, _ := play(t, s, ws.Message{Type: "click", Item: "corridor"})
			wantText(t, "corridor again", text, c.want)
			if roomOf(s) != "corridor" {
				t.Fatalf("in %q", roomOf(s))
			}
		})
	}
}

// The locked corridor: its door is gone from the items, so clicking it is
// refused by the engine; every typed way back gets Whisp, costs an action
// and moves no one; the back door still works.
func TestDemoLockedCorridorEveryWay(t *testing.T) {
	w := shippedDemo(t)
	s := inGuardRoom(t, w) // 5 left
	defer s.Close()
	code := boxCode(t, w)
	_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
	play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: code}) // 4
	got = s.Handle(ws.Message{Type: "click", Item: "corridor"})
	if len(got) != 1 || got[0].Type != "error" || got[0].Message != "no such item" {
		t.Fatalf("click the hidden door: %+v", got)
	}
	for _, say := range []string{"back", "go back"} { // 3, 2
		text, _ := play(t, s, ws.Message{Type: "input", Text: say})
		if !strings.HasPrefix(text, "Whisp: You have everything you need from the corridor.") || roomOf(s) != "guardroom" {
			t.Fatalf("%q: %q, in %q", say, text, roomOf(s))
		}
	}
	text, got := play(t, s, ws.Message{Type: "input", Text: "leave"}) // 1
	escaped(t, s, text, got)
}

// The demo keeps progress in room globals (left, opened, entered, key):
// that holds only while the player keeps every room's instance.
func TestDemoRoomsFitInstances(t *testing.T) {
	w := shippedDemo(t)
	n := 0
	for _, r := range w.pack.Rooms {
		if r.Source != "" {
			n++
		}
	}
	if n > MaxRoomInstances {
		t.Fatalf("%d scripted rooms, but a player keeps only %d instances: move room globals into state.player", n, MaxRoomInstances)
	}
}

// Every room loads the shared helpers from lib/rooms.lua instead of
// carrying its own copy.
func TestDemoSharedHelpersSame(t *testing.T) {
	w := shippedDemo(t)
	lib := w.pack.Modules["rooms"]
	helpers := []string{"function whisp(", "function whisp_ask(", "function keep(", "function guard_away(", "function set_guard_away(", "function solve(", "function solved(", "function go("}
	for _, fn := range helpers {
		if !strings.Contains(lib, fn) {
			t.Errorf("lib/rooms.lua lacks %q", fn)
		}
	}
	for _, id := range []string{"cell", "corridor", "guardroom"} {
		src := w.pack.Rooms[id].Source
		if !strings.Contains(src, `require("rooms")`) {
			t.Errorf("%s does not require rooms", id)
		}
		for _, fn := range helpers {
			if strings.Contains(src, fn) {
				t.Errorf("%s still has its own %q", id, fn)
			}
		}
	}
}

// With the key, a plain "door" in the guard room is the back door: the
// corridor's door is gone.
func TestDemoDoorWithKey(t *testing.T) {
	w := shippedDemo(t)
	s := inGuardRoom(t, w)
	defer s.Close()
	text, _ := play(t, s, ws.Message{Type: "input", Text: "open the door"})
	wantText(t, "which door", text, "Which door")
	code := boxCode(t, w)
	_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
	play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: code})
	text, got = play(t, s, ws.Message{Type: "input", Text: "open the door"})
	escaped(t, s, text, got)
}

// refuseState makes s's state.player refuse every write that grows it
// while the returned flag is set, as a full server does: keep in
// lib/rooms.lua then tells the player and returns false.
func refuseState(s ws.Session) *atomic.Bool {
	var full atomic.Bool
	st := &s.(*session).state
	reserve := st.Reserve
	st.Reserve = func(delta int64) error {
		if full.Load() && delta > 0 {
			return errors.New("full")
		}
		return reserve(delta)
	}
	return &full
}

// When the server refuses a write, keep (lib/rooms.lua) says so and the
// helpers built on it report false: the guard stays, the cell is not left
// and the box stays shut. Once writes work again, the same action works.
func TestDemoKeepRefused(t *testing.T) {
	const refused = "(The game cannot remember that right now. Try again in a moment.)"
	cases := []struct {
		name    string
		session func(t *testing.T, w *World) ws.Session
		act     func(t *testing.T, w *World, s ws.Session) (string, []ws.Message)
		room    string // where the player is after a refused act
		done    string // what the act says once it works
	}{
		{
			name: "set_guard_away",
			session: func(t *testing.T, w *World) ws.Session {
				s, _ := w.NewSession("", ws.Who{})
				askWater(t, s)
				askWater(t, s)
				return s
			},
			act: func(t *testing.T, _ *World, s ws.Session) (string, []ws.Message) {
				return play(t, s, ws.Message{Type: "choose", ID: guardDialog(t, s), Option: 1})
			},
			room: "cell",
			done: "stomps off down the corridor",
		},
		{
			name: "solve ask",
			session: func(t *testing.T, w *World) ws.Session {
				s, _ := w.NewSession("", ws.Who{})
				sendAway(t, s)
				openLock(t, s)
				return s
			},
			act: func(t *testing.T, _ *World, s ws.Session) (string, []ws.Message) {
				return play(t, s, ws.Message{Type: "click", Item: "lock"})
			},
			room: "cell",
			done: "You are out!",
		},
		{
			name:    "solve locked",
			session: inGuardRoom,
			act: func(t *testing.T, w *World, s ws.Session) (string, []ws.Message) {
				code := boxCode(t, w)
				_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
				return play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: code})
			},
			room: "guardroom",
			done: "big iron key",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := shippedDemo(t)
			s := c.session(t, w)
			defer s.Close()
			full := refuseState(s)
			full.Store(true)
			text, _ := c.act(t, w, s)
			wantText(t, "refused", text, refused)
			if strings.Contains(text, c.done) {
				t.Fatalf("refused write still did it: %q", text)
			}
			if roomOf(s) != c.room {
				t.Fatalf("after the refused write in %q, want %q", roomOf(s), c.room)
			}
			full.Store(false)
			text, _ = c.act(t, w, s)
			wantText(t, "again", text, c.done)
			if strings.Contains(text, refused) {
				t.Fatalf("write refused after the server had room: %q", text)
			}
		})
	}
}

// guardDialog opens the guard dialog in the cell and returns its id.
func guardDialog(t *testing.T, s ws.Session) string {
	t.Helper()
	_, got := play(t, s, ws.Message{Type: "click", Item: "guard"})
	if len(got) != 1 || got[0].Type != "dialog" {
		t.Fatalf("guard: %+v", got)
	}
	return got[0].ID
}
