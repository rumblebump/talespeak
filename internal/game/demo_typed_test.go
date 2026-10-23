package game

import (
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// The demo played by typing: escaping with words, the corridor's and the
// guard room's words, and the key box codes.

// inGuardRoom walks a new player out of the cell, reads the code and goes
// into the guard room.
func inGuardRoom(t *testing.T, w *World) ws.Session {
	t.Helper()
	s, _ := w.NewSession("", ws.Who{})
	escapeCell(t, s)
	play(t, s, ws.Message{Type: "click", Item: "stone"})
	play(t, s, ws.Message{Type: "click", Item: "door"})
	if roomOf(s) != "guardroom" {
		t.Fatalf("not in the guard room: %q", roomOf(s))
	}
	return s
}

// The whole way out by typing only: no clicks after the guard has gone.
func TestDemoTypedEscape(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	sendAway(t, s)
	_, got := play(t, s, ws.Message{Type: "input", Text: "Open the LOCK"})
	text, _ := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "Cell"})
	wantText(t, "riddle", text, "The lock opens!")
	text, _ = play(t, s, ws.Message{Type: "input", Text: "leave"})
	wantText(t, "leave", text, "You are out!")
	steps := []struct{ say, room, want string }{
		{"dig under the X", "corridor", "You lift the stone with the X."},
		{"open the door marked GUARDS", "guardroom", "Nobody is here."},
		{"try the keyhole", "guardroom", "The back door is locked."},
	}
	note := ""
	for _, st := range steps {
		text, _ := play(t, s, ws.Message{Type: "input", Text: st.say})
		wantText(t, st.say, text, st.want)
		if note == "" {
			note = noteRE.FindString(text)
		}
		if roomOf(s) != st.room {
			t.Fatalf("%q: in %q, want %q", st.say, roomOf(s), st.room)
		}
	}
	_, got = play(t, s, ws.Message{Type: "input", Text: "turn the wheels"})
	text, _ = play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: note})
	wantText(t, "box", text, "big iron key")
	text, got = play(t, s, ws.Message{Type: "input", Text: "open the back door"})
	escaped(t, s, text, got)
}

// Typed keywords in the corridor, each from a fresh corridor.
func TestDemoCorridorWords(t *testing.T) {
	cases := []struct {
		say  string
		room string // where the player is afterwards
		want string // text, "dialog" for the stairs, or "whisp" for Whisp's question
	}{
		{"lift the stone", "corridor", "BOX "},
		{"look at the floor", "corridor", "BOX "},
		{"X", "corridor", "BOX "},
		{"look under the x", "corridor", "BOX "},
		{"open the door", "guardroom", "Nobody is here."},
		{"knock on the GUARDS door", "guardroom", "Nobody is here."},
		{"go ahead", "guardroom", "Nobody is here."},
		{"go down the stairs", "corridor", "dialog"},
		{"follow the sound of water", "corridor", "dialog"},
		{"go back to the cell", "corridor", "whisp"},
		{"back", "corridor", "whisp"},
		{"help", "corridor", "Try: look at the stone, open the door"},
		{"dance", "corridor", "You think about \"dance\", but the footsteps below make you hurry."},
	}
	for _, c := range cases {
		t.Run(c.say, func(t *testing.T) {
			w := shippedDemo(t)
			s, _ := w.NewSession("", ws.Who{})
			defer s.Close()
			escapeCell(t, s)
			text, got := play(t, s, ws.Message{Type: "input", Text: c.say})
			switch c.want {
			case "dialog":
				if len(got) != 1 || got[0].Type != "dialog" || !strings.Contains(got[0].Title, "Go down the stairs?") {
					t.Fatalf("%+v", got)
				}
			case "whisp":
				if len(got) != 1 || got[0].Type != "dialog" || !strings.Contains(got[0].Title, "locked in again?") {
					t.Fatalf("%+v", got)
				}
			default:
				wantText(t, c.say, text, c.want)
			}
			if roomOf(s) != c.room {
				t.Fatalf("in %q, want %q", roomOf(s), c.room)
			}
		})
	}
}

// Typed keywords in the guard room, each from a fresh guard room.
func TestDemoGuardRoomWords(t *testing.T) {
	cases := []struct {
		say  string
		room string
		want string // text, or "ask"
	}{
		{"open the box", "guardroom", "ask"},
		{"turn the wheels", "guardroom", "ask"},
		{"enter the CODE", "guardroom", "ask"},
		{"look at the cards", "guardroom", "Hans owes me three silver."},
		{"eat the sausage", "guardroom", "Hans owes me three silver."},
		{"search the table", "guardroom", "Hans owes me three silver."},
		{"open the back door", "guardroom", "The back door is locked."},
		{"look at the keyhole", "guardroom", "The back door is locked."},
		{"escape", "guardroom", "The back door is locked."},
		{"go back to the corridor", "corridor", "The torch still smokes."},
		{"corridor", "corridor", "The torch still smokes."},
		{"help", "guardroom", "Try: open the box, look at the cards"},
		{"sing", "guardroom", "You think about \"sing\", but there is no time for that."},
	}
	for _, c := range cases {
		t.Run(c.say, func(t *testing.T) {
			w := shippedDemo(t)
			s := inGuardRoom(t, w)
			defer s.Close()
			text, got := play(t, s, ws.Message{Type: "input", Text: c.say})
			if c.want == "ask" {
				if len(got) != 1 || got[0].Type != "ask" || !got[0].Strict || !strings.Contains(got[0].Title, "Three number wheels") {
					t.Fatalf("%+v", got)
				}
			} else {
				wantText(t, c.say, text, c.want)
			}
			if roomOf(s) != c.room {
				t.Fatalf("in %q, want %q", roomOf(s), c.room)
			}
		})
	}
}

// digits() keeps only ASCII digits: any spacing or punctuation around the
// code's digits opens the box, anything else does not. The answers are
// written for the code 417; each world has its own, put in for 4, 1 and 7.
func TestDemoBoxCodes(t *testing.T) {
	cases := []struct {
		answer string
		open   bool
	}{
		{"417", true},
		{"4 1 7", true},
		{"4-1-7", true},
		{"4, 1, 7", true},
		{"BOX 4 1 7", true},
		{"  4 1 7  ", true},
		{"other", false}, // another code
		{"41", false},
		{"0417", false},
		{"4170", false},
		{"4 1 7 0", false},
		{"four one seven", false},
		{"４１７", false}, // full-width digits are not ASCII
		{"box", false},
	}
	for _, c := range cases {
		t.Run(c.answer, func(t *testing.T) {
			w := shippedDemo(t)
			s := inGuardRoom(t, w)
			defer s.Close()
			code := boxCode(t, w)
			answer := strings.NewReplacer("4", code[:1], "1", code[1:2], "7", code[2:],
				"４", string('０'+rune(code[0]-'0')), "１", string('０'+rune(code[1]-'0')), "７", string('０'+rune(code[2]-'0'))).Replace(c.answer)
			if c.answer == "other" {
				answer = otherCode(code)
			}
			_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
			text, got := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: answer})
			if opened := strings.Contains(text, "big iron key"); opened != c.open {
				t.Fatalf("opened %v, want %v: %q", opened, c.open, text)
			}
			if !c.open {
				wantText(t, "shut", text, "The box stays shut.")
				if strings.Contains(text, "Hint:") {
					t.Fatalf("hint on the first try: %q", text)
				}
				return
			}
			if items := lastItems(t, got); items["box"] != "open iron box" {
				t.Fatalf("items: %v", items)
			}
		})
	}
}

// Every world picks its own box code: all its players read the same one,
// and new worlds get new ones.
func TestDemoBoxCodePerWorld(t *testing.T) {
	codes := map[string]bool{}
	for range 8 {
		w := shippedDemo(t)
		code := boxCode(t, w)
		if again := boxCode(t, w); again != code {
			t.Fatalf("two players of one world read %q and %q", code, again)
		}
		codes[code] = true
	}
	if len(codes) < 2 { // 8 times the same of 1000 codes: once in 10^21 runs
		t.Fatalf("8 worlds, one code: %v", codes)
	}
}

// The code is picked by whoever needs it first: a player who tries the box
// before anyone lifted the stone gets the code the note shows later.
func TestDemoBoxBeforeStone(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)
	play(t, s, ws.Message{Type: "click", Item: "door"})
	if roomOf(s) != "guardroom" {
		t.Fatalf("not in the guard room: %q", roomOf(s))
	}
	_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
	text, _ := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "1 2 3"})
	code := boxCode(t, w)
	if code == "123" {
		wantText(t, "box", text, "big iron key")
		return
	}
	wantText(t, "box", text, "The box stays shut.")
	_, got = play(t, s, ws.Message{Type: "click", Item: "box"})
	text, _ = play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: code})
	wantText(t, "box", text, "big iron key")
}
