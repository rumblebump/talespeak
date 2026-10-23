package game

import (
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// The demo across rooms and players: repeated actions, players apart,
// words reaching the right room.

// Doing a thing twice: the stone repeats the code, the empty box says so,
// and every wrong code after the first gets the hint.
func TestDemoRepeats(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)
	text, _ := play(t, s, ws.Message{Type: "click", Item: "stone"})
	code := noteCode(t, text)
	text, got := play(t, s, ws.Message{Type: "click", Item: "stone"})
	wantText(t, "stone again", text, "only dust now. The note said: BOX "+code[:1]+" "+code[1:2]+" "+code[2:]+".")
	for _, m := range got {
		if m.Type == "items" {
			t.Fatalf("items again after the stone: %+v", got)
		}
	}
	play(t, s, ws.Message{Type: "click", Item: "door"})
	for i := 1; i <= 2; i++ {
		_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
		text, _ := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: otherCode(code)})
		if hinted := strings.Contains(text, "Hint:"); hinted != (i >= 2) {
			t.Fatalf("try %d: %q", i, text)
		}
	}
	_, got = play(t, s, ws.Message{Type: "click", Item: "box"})
	play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: code})
	text, got = play(t, s, ws.Message{Type: "click", Item: "box"})
	wantText(t, "box again", text, "The box is empty. The big key is in your hand.")
	for _, m := range got {
		if m.Type == "ask" {
			t.Fatalf("box asks again after it opened: %+v", got)
		}
	}
}

// Saying no at the stairs keeps the story going, and still costs an action.
func TestDemoCorridorStayUp(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)
	_, got := play(t, s, ws.Message{Type: "click", Item: "stairs"})
	text, got := play(t, s, ws.Message{Type: "choose", ID: got[0].ID, Option: 2})
	if text != "You stay in the corridor." || len(got) != 1 {
		t.Fatalf("stay: %q %+v", text, got)
	}
	look := ws.Message{Type: "input", Text: "hmm"}
	for i := 2; i <= leftAfterCell; i++ {
		text, _ := play(t, s, look)
		if warned := strings.Contains(text, "Footsteps on the stairs"); warned != (i == leftAfterCell-1) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got = play(t, s, look)
	wantText(t, "caught", text, "sees you standing in the corridor")
	ended(t, s, text, got)
}

// Back in the cell, the countdown goes on from where it was, and the guard
// finds the open lock.
func TestDemoCaughtBackInCell(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)
	backToCell(t, s, ws.Message{Type: "click", Item: "cell"}) // asking costs one action
	look := ws.Message{Type: "input", Text: "hmm"}
	for i := 1; i <= leftAfterCell-1; i++ {
		text, _ := play(t, s, look)
		if warned := strings.Contains(text, "footsteps are coming closer"); warned != (i == leftAfterCell-2) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got := play(t, s, look)
	wantText(t, "seen", text, "He sees the open lock.")
	ended(t, s, text, got)
}

// The guard's countdown is one for all rooms: leaving the corridor on its
// last action, the player meets him in the guard room.
func TestDemoGuardCountsInEveryRoom(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)
	for range leftAfterCell - 1 {
		play(t, s, ws.Message{Type: "input", Text: "hmm"})
	}
	play(t, s, ws.Message{Type: "click", Item: "door"}) // the last action
	if roomOf(s) != "guardroom" {
		t.Fatalf("in %q", roomOf(s))
	}
	text, got := play(t, s, ws.Message{Type: "click", Item: "corridor"})
	wantText(t, "caught", text, "The door bangs open.")
	if roomOf(s) != "guardroom" {
		t.Fatalf("caught, then moved to %q", roomOf(s))
	}
	ended(t, s, text, got)
}

// Every player has their own rooms: one player's stone and countdown do
// not touch another's.
func TestDemoPlayersApart(t *testing.T) {
	w := shippedDemo(t)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	escapeCell(t, a)
	play(t, a, ws.Message{Type: "click", Item: "stone"})
	play(t, a, ws.Message{Type: "input", Text: "hmm"})
	play(t, a, ws.Message{Type: "input", Text: "hmm"})
	escapeCell(t, b)
	text, _ := play(t, b, ws.Message{Type: "click", Item: "stone"})
	wantText(t, "b's stone", text, "You lift the stone with the X.")
	for i := 2; i <= leftAfterCell-1; i++ {
		text, _ := play(t, b, ws.Message{Type: "input", Text: "hmm"})
		if warned := strings.Contains(text, "Footsteps on the stairs"); warned != (i == leftAfterCell-1) {
			t.Fatalf("b action %d: %q", i, text)
		}
	}
	// a has used 3 actions of the corridor's: the 6th warns.
	for i := 4; i <= leftAfterCell-1; i++ {
		text, _ = play(t, a, ws.Message{Type: "input", Text: "hmm"})
		if warned := strings.Contains(text, "Footsteps on the stairs"); warned != (i == leftAfterCell-1) {
			t.Fatalf("a action %d: %q", i, text)
		}
	}
}

// Typed lines go to the room they name, the most specific word first.
func TestDemoWordsGoWhereMeant(t *testing.T) {
	cases := []struct{ from, say, want string }{
		{"corridor", "open the cell door", "cell"},
		{"corridor", "go back to the guard room", "guardroom"},
		{"corridor", "enter the guard room", "guardroom"},
		{"guardroom", "open the backdoor", "guardroom"},
		{"guardroom", "open the back-door", "guardroom"},
		{"cell", "go back to the corridor", "corridor"},
		{"corridor", "go back through the cell door", "cell"},
		{"guardroom", "unlock the back door with the key from the box", "guardroom"},
	}
	for _, c := range cases {
		t.Run(c.from+"/"+c.say, func(t *testing.T) {
			w := shippedDemo(t)
			s, _ := w.NewSession("", ws.Who{})
			defer s.Close()
			escapeCell(t, s)
			switch c.from {
			case "guardroom":
				play(t, s, ws.Message{Type: "click", Item: "door"})
			case "cell":
				backToCell(t, s, ws.Message{Type: "click", Item: "cell"})
			}
			var text string
			if c.want == "cell" {
				text, _ = backToCell(t, s, ws.Message{Type: "input", Text: c.say})
			} else {
				text, _ = play(t, s, ws.Message{Type: "input", Text: c.say})
			}
			if roomOf(s) != c.want {
				t.Errorf("in %q, want %q: %q", roomOf(s), c.want, text)
			}
		})
	}
}

// Lines that name no room do what they say.
func TestDemoWordsInRooms(t *testing.T) {
	cases := []struct{ from, say, want string }{
		{"corridor", "x marks the spot", "BOX "},
		{"corridor", "talk about it", "You think about"},
		{"guardroom", "unlock the back door with the key from the box", "The back door is locked."},
		{"guardroom", "go out", "The back door is locked."},
		{"guardroom", "open the door", "Which door"},
		{"guardroom", "417", "Numbers for the box?"},
	}
	for _, c := range cases {
		t.Run(c.from+"/"+c.say, func(t *testing.T) {
			w := shippedDemo(t)
			s, _ := w.NewSession("", ws.Who{})
			defer s.Close()
			escapeCell(t, s)
			if c.from == "guardroom" {
				play(t, s, ws.Message{Type: "click", Item: "door"})
			}
			text, _ := play(t, s, ws.Message{Type: "input", Text: c.say})
			wantText(t, c.say, text, c.want)
		})
	}
}
