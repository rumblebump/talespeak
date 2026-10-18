package game

import (
	"strconv"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// play sends m and returns the texts and the ids of the questions it got.
func play(t *testing.T, s ws.Session, m ws.Message) (string, []ws.Message) {
	t.Helper()
	got := s.Handle(m)
	var texts []string
	for _, g := range got {
		if g.Type == "error" {
			t.Fatalf("%+v: error %q", m, g.Text)
		}
		if g.Type == "text" {
			if g.From != "" { // as the client shows it: the speaker, then the line
				g.Text = g.From + ": " + g.Text
			}
			texts = append(texts, g.Text)
		}
	}
	return strings.Join(texts, "\n"), got
}

// askWater opens the guard dialog and asks for water.
func askWater(t *testing.T, s ws.Session) string {
	t.Helper()
	_, got := play(t, s, ws.Message{Type: "click", Item: "guard"})
	if len(got) != 1 || got[0].Type != "dialog" {
		t.Fatalf("guard: %+v", got)
	}
	text, _ := play(t, s, ws.Message{Type: "choose", ID: got[0].ID, Option: 1})
	return text
}

func lastItems(t *testing.T, ms []ws.Message) map[string]string {
	t.Helper()
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Type == "items" {
			m := map[string]string{}
			for _, it := range ms[i].Items {
				m[it.ID] = it.Label
			}
			return m
		}
	}
	t.Fatalf("no items in %+v", ms)
	return nil
}

func wantText(t *testing.T, step, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s: %q lacks %q", step, got, want)
	}
}

// sendAway asks for water three times, which sends the guard away.
func sendAway(t *testing.T, s ws.Session) {
	t.Helper()
	wantText(t, "cup 1", askWater(t, s), "slides a cup of water")
	wantText(t, "cup 2", askWater(t, s), "\"Again?\"")
	_, got := play(t, s, ws.Message{Type: "click", Item: "guard"})
	text, got := play(t, s, ws.Message{Type: "choose", ID: got[0].ID, Option: 1})
	wantText(t, "cup 3", text, "stomps off down the corridor")
	if items := lastItems(t, got); items["guard"] != "empty stool" {
		t.Fatalf("items after the guard left: %v", items)
	}
}

func openLock(t *testing.T, s ws.Session) {
	t.Helper()
	_, got := play(t, s, ws.Message{Type: "click", Item: "lock"})
	if len(got) != 2 || got[1].Type != "ask" {
		t.Fatalf("lock: %+v", got)
	}
	text, got := play(t, s, ws.Message{Type: "answer", ID: got[1].ID, Text: "cell"})
	wantText(t, "answer", text, "The lock opens!")
	if items := lastItems(t, got); items["lock"] != "open cell door" {
		t.Fatalf("items after the lock opened: %v", items)
	}
}

// ended checks the story is over: no items left, and any action only
// says so.
func ended(t *testing.T, s ws.Session, text string, got []ws.Message) {
	t.Helper()
	wantText(t, "end", text, "THE END.")
	if items := lastItems(t, got); len(items) != 0 {
		t.Fatalf("items after the end: %v", items)
	}
	after, _ := play(t, s, ws.Message{Type: "input", Text: "talk to the guard"})
	if after != "The story has ended. Reload the page to play again." {
		t.Fatalf("after the end: %q", after)
	}
}

// escapeCell sends the guard away, opens the lock and walks out into the
// corridor.
func escapeCell(t *testing.T, s ws.Session) {
	t.Helper()
	sendAway(t, s)
	text, _ := play(t, s, ws.Message{Type: "input", Text: "ask for water"})
	wantText(t, "talk while away", text, "The guard's stool is empty.")
	openLock(t, s)
	text, got := play(t, s, ws.Message{Type: "click", Item: "lock"})
	wantText(t, "out", text, "You are out!")
	room := ""
	for _, g := range got {
		if g.Type == "room" {
			room = g.Title
		}
	}
	if room != "A dark corridor" {
		t.Fatalf("after the cell: room %q in %+v", room, got)
	}
	if items := lastItems(t, got); items["stone"] != "stone with an X" {
		t.Fatalf("corridor items: %v", items)
	}
}

// question returns the id of the ui.ask in ms.
func question(t *testing.T, ms []ws.Message) string {
	t.Helper()
	for _, m := range ms {
		if m.Type == "ask" {
			return m.ID
		}
	}
	t.Fatalf("no question in %+v", ms)
	return ""
}

func TestDemoEscape(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	escapeCell(t, s)
	text, got := play(t, s, ws.Message{Type: "click", Item: "stone"})
	wantText(t, "stone", text, "BOX 4 1 7")
	if items := lastItems(t, got); items["stone"] != "lifted stone" {
		t.Fatalf("items after the stone: %v", items)
	}
	text, _ = play(t, s, ws.Message{Type: "click", Item: "door"})
	wantText(t, "guard room", text, "Nobody is here.")
	text, _ = play(t, s, ws.Message{Type: "click", Item: "backdoor"})
	wantText(t, "locked", text, "The back door is locked.")
	_, got = play(t, s, ws.Message{Type: "click", Item: "box"})
	text, got = play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "4-1-7"})
	wantText(t, "box", text, "big iron key")
	if items := lastItems(t, got); items["box"] != "open iron box" {
		t.Fatalf("items after the box: %v", items)
	}
	text, got = play(t, s, ws.Message{Type: "click", Item: "backdoor"})
	wantText(t, "escape", text, "You escaped. Well done!")
	ended(t, s, text, got)
}

// Going down the stairs walks into the guard.
func TestDemoCorridorStairs(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	escapeCell(t, s)
	_, got := play(t, s, ws.Message{Type: "input", Text: "go down the stairs"})
	if len(got) != 1 || got[0].Type != "dialog" {
		t.Fatalf("stairs: %+v", got)
	}
	text, got := play(t, s, ws.Message{Type: "choose", ID: got[0].ID, Option: 1})
	wantText(t, "well", text, "straight into the guard")
	wantText(t, "die", text, "After a few days, you die.")
	ended(t, s, text, got)
}

// leftAfterCell is how many actions the guard's countdown has left when
// escapeCell is done: AWAY_MOVES (10) minus one question while he is
// away, opening the lock and walking out.
const leftAfterCell = 7

// backToCell sends m, which asks to go back into the solved cell, and says
// yes to Whisp's question.
func backToCell(t *testing.T, s ws.Session, m ws.Message) (string, []ws.Message) {
	t.Helper()
	_, got := play(t, s, m)
	if len(got) != 1 || got[0].Type != "dialog" || got[0].Title != "Do you really want to get locked in again?" || got[0].From != "Whisp" {
		t.Fatalf("%+v: %+v", m, got)
	}
	return play(t, s, ws.Message{Type: "choose", ID: got[0].ID, Option: 1})
}

// In the corridor the guard is back when the cell's countdown runs out,
// and warns one action before.
func TestDemoGuardCatchesInCorridor(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	escapeCell(t, s)
	look := ws.Message{Type: "input", Text: "hmm"}
	for i := 1; i <= leftAfterCell; i++ {
		text, _ := play(t, s, look)
		if warned := strings.Contains(text, "Footsteps on the stairs"); warned != (i == leftAfterCell-1) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got := play(t, s, look)
	wantText(t, "caught", text, "sees you standing in the corridor")
	ended(t, s, text, got)
}

// A wrong code gets a hint on the second try; the corridor keeps what the
// player did; Whisp asks before the solved cell, and no keeps the player
// out; the guard's one countdown goes on in every room.
func TestDemoGuardRoom(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	escapeCell(t, s)                                             // 7 left
	play(t, s, ws.Message{Type: "click", Item: "stone"})         // 6
	play(t, s, ws.Message{Type: "input", Text: "open the door"}) // 5
	for i := 1; i <= 2; i++ {                                    // 4, 3
		_, got := play(t, s, ws.Message{Type: "input", Text: "open the box"})
		text, _ := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "123"})
		wantText(t, "wrong code", text, "The box stays shut.")
		if hinted := strings.Contains(text, "Hint:"); hinted != (i == 2) {
			t.Fatalf("try %d: %q", i, text)
		}
	}
	_, got := play(t, s, ws.Message{Type: "click", Item: "corridor"}) // 2
	if items := lastItems(t, got); items["stone"] != "lifted stone" {
		t.Fatalf("corridor items on coming back: %v", items)
	}
	text, got := play(t, s, ws.Message{Type: "click", Item: "cell"}) // 1
	wantText(t, "warning", text, "Footsteps on the stairs")
	if len(got) != 2 || got[1].Type != "dialog" || got[1].Title != "Do you really want to get locked in again?" || got[1].From != "Whisp" {
		t.Fatalf("cell: %+v", got)
	}
	text, _ = play(t, s, ws.Message{Type: "choose", ID: got[1].ID, Option: 2})
	if text != "Whisp: Good. Onwards!" || roomOf(s) != "corridor" {
		t.Fatalf("no to the cell: %q, in %q", text, roomOf(s))
	}
	text, _ = play(t, s, ws.Message{Type: "click", Item: "door"}) // 0
	if roomOf(s) != "guardroom" || strings.Contains(text, "Nobody is here.") {
		t.Fatalf("guard room again: %q, in %q", text, roomOf(s))
	}
	text, got = play(t, s, ws.Message{Type: "input", Text: "hmm"})
	wantText(t, "caught", text, "The door bangs open.")
	ended(t, s, text, got)
}

// Back in the cell after saying yes to Whisp, the cell is as the player
// left it.
func TestDemoBackInCell(t *testing.T) {
	for _, m := range []ws.Message{{Type: "click", Item: "cell"}, {Type: "input", Text: "go back to the cell"}, {Type: "input", Text: "open the cell door"}} {
		w := shippedDemo(t)
		s, _ := w.NewSession("")
		escapeCell(t, s)
		text, got := backToCell(t, s, m)
		wantText(t, "cell", text, "Your old cell.")
		if roomOf(s) != "cell" {
			t.Fatalf("%+v: in %q", m, roomOf(s))
		}
		if items := lastItems(t, got); items["lock"] != "open cell door" || items["guard"] != "empty stool" {
			t.Fatalf("cell items on coming back: %v", items)
		}
		s.Close()
	}
}

// With the key, the corridor is solved and locked: its door is gone, and
// typing it gets Whisp's answer instead of a move.
func TestDemoCorridorLockedWithKey(t *testing.T) {
	w := shippedDemo(t)
	s := inGuardRoom(t, w)
	defer s.Close()
	_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
	_, got = play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "417"})
	items := lastItems(t, got)
	if _, ok := items["corridor"]; ok || items["box"] != "open iron box" {
		t.Fatalf("items with the key: %v", items)
	}
	for _, say := range []string{"go back to the corridor", "corridor"} {
		text, got := play(t, s, ws.Message{Type: "input", Text: say})
		if text != "Whisp: You have everything you need from the corridor. The way out is the back door." || roomOf(s) != "guardroom" {
			t.Fatalf("%q: %q, in %q", say, text, roomOf(s))
		}
		for _, m := range got {
			if m.Type == "dialog" {
				t.Fatalf("%q: locked room asks: %+v", say, got)
			}
		}
	}
}

func TestDemoCaughtAtTheDoor(t *testing.T) {
	for _, try := range []ws.Message{{Type: "click", Item: "lock"}, {Type: "input", Text: "open the door"}} {
		w := shippedDemo(t)
		s, _ := w.NewSession("")
		_, got := play(t, s, try)
		if len(got) != 1 || got[0].Type != "dialog" || !strings.Contains(got[0].Title, "The guard is watching you") {
			t.Fatalf("%+v: %+v", try, got)
		}
		text, got := play(t, s, ws.Message{Type: "choose", ID: got[0].ID, Option: 1})
		wantText(t, "slap", text, "SLAP!")
		wantText(t, "well", text, "deep well")
		wantText(t, "die", text, "After a few days, you die.")
		ended(t, s, text, got)
		s.Close()
	}
}

// The guard is back after AWAY_MOVES actions, warns one action before, and
// can be sent away again.
func TestDemoGuardComesBack(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	sendAway(t, s)
	look := ws.Message{Type: "input", Text: "hmm"}
	for i := 1; i <= 10; i++ {
		text, _ := play(t, s, look)
		if warned := strings.Contains(text, "footsteps are coming closer"); warned != (i == 9) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got := play(t, s, look)
	wantText(t, "back", text, "The guard is back with a full bottle.")
	if items := lastItems(t, got); items["guard"] != "sleepy guard" {
		t.Fatalf("items after the guard came back: %v", items)
	}
	sendAway(t, s)
}

// Coming back to an open lock, the guard catches the player.
func TestDemoGuardSeesOpenLock(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	sendAway(t, s)
	openLock(t, s)
	look := ws.Message{Type: "input", Text: "hmm"}
	for range 9 {
		play(t, s, look)
	}
	text, got := play(t, s, ws.Message{Type: "click", Item: "lock"})
	wantText(t, "seen", text, "He sees the open lock.")
	wantText(t, "slap", text, "SLAP!")
	ended(t, s, text, got)
}

// inGuardRoom walks a new player out of the cell, reads the code and goes
// into the guard room.
func inGuardRoom(t *testing.T, w *World) ws.Session {
	t.Helper()
	s, _ := w.NewSession("")
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
	s, _ := w.NewSession("")
	defer s.Close()
	sendAway(t, s)
	_, got := play(t, s, ws.Message{Type: "input", Text: "Open the LOCK"})
	text, _ := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "Cell"})
	wantText(t, "riddle", text, "The lock opens!")
	text, _ = play(t, s, ws.Message{Type: "input", Text: "leave"})
	wantText(t, "leave", text, "You are out!")
	steps := []struct{ say, room, want string }{
		{"dig under the X", "corridor", "BOX 4 1 7"},
		{"open the door marked GUARDS", "guardroom", "Nobody is here."},
		{"try the keyhole", "guardroom", "The back door is locked."},
	}
	for _, st := range steps {
		text, _ := play(t, s, ws.Message{Type: "input", Text: st.say})
		wantText(t, st.say, text, st.want)
		if roomOf(s) != st.room {
			t.Fatalf("%q: in %q, want %q", st.say, roomOf(s), st.room)
		}
	}
	_, got = play(t, s, ws.Message{Type: "input", Text: "turn the wheels"})
	text, _ = play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "BOX 4 1 7"})
	wantText(t, "box", text, "big iron key")
	text, got = play(t, s, ws.Message{Type: "input", Text: "open the back door"})
	wantText(t, "escape", text, "You escaped. Well done!")
	ended(t, s, text, got)
}

// Typed keywords in the corridor, each from a fresh corridor.
func TestDemoCorridorWords(t *testing.T) {
	cases := []struct {
		say  string
		room string // where the player is afterwards
		want string // text, "dialog" for the stairs, or "whisp" for Whisp's question
	}{
		{"lift the stone", "corridor", "BOX 4 1 7"},
		{"look at the floor", "corridor", "BOX 4 1 7"},
		{"X", "corridor", "BOX 4 1 7"},
		{"look under the x", "corridor", "BOX 4 1 7"},
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
			s, _ := w.NewSession("")
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

// digits() keeps only ASCII digits: any spacing or punctuation around 4 1 7
// opens the box, anything else does not.
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
		{"714", false},
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
			_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
			text, got := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: c.answer})
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

// Doing a thing twice: the stone repeats the code, the empty box says so,
// and every wrong code after the first gets the hint.
func TestDemoRepeats(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	escapeCell(t, s)
	play(t, s, ws.Message{Type: "click", Item: "stone"})
	text, got := play(t, s, ws.Message{Type: "click", Item: "stone"})
	wantText(t, "stone again", text, "only dust now. The note said: BOX 4 1 7.")
	for _, m := range got {
		if m.Type == "items" {
			t.Fatalf("items again after the stone: %+v", got)
		}
	}
	play(t, s, ws.Message{Type: "click", Item: "door"})
	for i := 1; i <= 2; i++ {
		_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
		text, _ := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "999"})
		if hinted := strings.Contains(text, "Hint:"); hinted != (i >= 2) {
			t.Fatalf("try %d: %q", i, text)
		}
	}
	_, got = play(t, s, ws.Message{Type: "click", Item: "box"})
	play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "417"})
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
	s, _ := w.NewSession("")
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
	s, _ := w.NewSession("")
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
	s, _ := w.NewSession("")
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
	a, _ := w.NewSession("")
	defer a.Close()
	b, _ := w.NewSession("")
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
			s, _ := w.NewSession("")
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
		{"corridor", "x marks the spot", "BOX 4 1 7"},
		{"corridor", "talk about it", "You think about"},
		{"guardroom", "unlock the back door with the key from the box", "The back door is locked."},
		{"guardroom", "go out", "The back door is locked."},
		{"guardroom", "open the door", "Which door"},
		{"guardroom", "417", "Numbers for the box?"},
	}
	for _, c := range cases {
		t.Run(c.from+"/"+c.say, func(t *testing.T) {
			w := shippedDemo(t)
			s, _ := w.NewSession("")
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
	s, _ := w.NewSession("")
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
	ended(t, s, text, got)
}

// Back in the cell, its open door leads out again, into the corridor as
// the player left it; the cell stays solved.
func TestDemoOutOfCellAgain(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
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
	s, _ := w.NewSession("")
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
	if strings.Contains(text, "BOX 4 1 7") {
		t.Fatalf("the stone was lifted while caught: %q", text)
	}
	ended(t, s, text, got)
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
			s, _ := w.NewSession("")
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
			ended(t, s, text, got)
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
			s, _ := w.NewSession("")
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
	_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
	play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "417"}) // 4
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
	wantText(t, "escape", text, "You escaped. Well done!")
	ended(t, s, text, got)
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

// The shared helpers (whisp, the countdown, solved rooms) live in one
// module, lib/rooms.lua, which every room requires; no room keeps a copy.
func TestDemoSharedHelpersInOneModule(t *testing.T) {
	w := shippedDemo(t)
	helpers := []string{"function whisp(", "function whisp_ask(", "function guard_away(", "function set_guard_away(", "function solve(", "function solved(", "function go("}
	lib := w.pack.Modules["rooms"]
	for _, fn := range helpers {
		if !strings.Contains(lib, fn) {
			t.Errorf("lib/rooms.lua lacks %q", fn)
		}
	}
	for _, id := range []string{"cell", "corridor", "guardroom"} {
		src := w.pack.Rooms[id].Source
		if !strings.Contains(src, `require("rooms")`) {
			t.Errorf("%s does not require the shared helpers", id)
		}
		for _, fn := range helpers {
			if strings.Contains(src, fn) {
				t.Errorf("%s keeps its own %q", id, fn)
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
	_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
	play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "417"})
	text, got = play(t, s, ws.Message{Type: "input", Text: "open the door"})
	wantText(t, "escape", text, "You escaped. Well done!")
	ended(t, s, text, got)
}
