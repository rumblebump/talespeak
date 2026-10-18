package game

import (
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

// In the corridor the guard is back after GUARD_MOVES actions and warns
// one action before.
func TestDemoGuardCatchesInCorridor(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	escapeCell(t, s)
	look := ws.Message{Type: "input", Text: "hmm"}
	for i := 1; i <= 5; i++ {
		text, _ := play(t, s, look)
		if warned := strings.Contains(text, "Footsteps on the stairs"); warned != (i == 4) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got := play(t, s, look)
	wantText(t, "caught", text, "sees you standing in the corridor")
	ended(t, s, text, got)
}

// A wrong code gets a hint on the second try; rooms keep what the player
// did when they come back; the guard comes into the guard room too.
func TestDemoGuardRoom(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	escapeCell(t, s)
	play(t, s, ws.Message{Type: "click", Item: "stone"})
	play(t, s, ws.Message{Type: "input", Text: "open the door"})
	for i := 1; i <= 2; i++ {
		_, got := play(t, s, ws.Message{Type: "input", Text: "open the box"})
		text, _ := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: "123"})
		wantText(t, "wrong code", text, "The box stays shut.")
		if hinted := strings.Contains(text, "Hint:"); hinted != (i == 2) {
			t.Fatalf("try %d: %q", i, text)
		}
	}
	_, got := play(t, s, ws.Message{Type: "click", Item: "corridor"})
	if items := lastItems(t, got); items["stone"] != "lifted stone" {
		t.Fatalf("corridor items on coming back: %v", items)
	}
	text, got := play(t, s, ws.Message{Type: "click", Item: "cell"})
	wantText(t, "cell", text, "Your old cell.")
	if items := lastItems(t, got); items["lock"] != "open cell door" {
		t.Fatalf("cell items on coming back: %v", items)
	}
	play(t, s, ws.Message{Type: "click", Item: "lock"})
	text, _ = play(t, s, ws.Message{Type: "click", Item: "door"})
	if strings.Contains(text, "Nobody is here.") {
		t.Fatalf("guard room greets twice: %q", text)
	}
	// The guard room counts its own actions: 2 codes and the way out so
	// far, so 3 more before the guard comes in, warning on the second.
	look := ws.Message{Type: "input", Text: "hmm"}
	for i := 1; i <= 3; i++ {
		text, _ := play(t, s, look)
		if warned := strings.Contains(text, "The guard is back up the stairs!"); warned != (i == 2) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got = play(t, s, look)
	wantText(t, "caught", text, "The door bangs open.")
	ended(t, s, text, got)
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
	for i := 1; i <= 6; i++ {
		text, _ := play(t, s, look)
		if warned := strings.Contains(text, "footsteps are coming closer"); warned != (i == 5) {
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
	for range 5 {
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
		want string // text, or "dialog"
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
		{"go back to the cell", "cell", "Your old cell."},
		{"back", "cell", "Your old cell."},
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
			if c.want == "dialog" {
				if len(got) != 1 || got[0].Type != "dialog" || !strings.Contains(got[0].Title, "Go down the stairs?") {
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
	for i := 1; i <= 3; i++ {
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
	for i := 2; i <= 5; i++ {
		text, _ := play(t, s, look)
		if warned := strings.Contains(text, "Footsteps on the stairs"); warned != (i == 4) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got = play(t, s, look)
	wantText(t, "caught", text, "sees you standing in the corridor")
	ended(t, s, text, got)
}

// Back in the cell, the cell's own countdown goes on from where the player
// left it, and the guard finds the open lock.
func TestDemoCaughtBackInCell(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	escapeCell(t, s) // the cell counted 3 of its 6 actions
	_, got := play(t, s, ws.Message{Type: "click", Item: "cell"})
	if items := lastItems(t, got); items["guard"] != "empty stool" || items["lock"] != "open cell door" {
		t.Fatalf("cell items on coming back: %v", items)
	}
	look := ws.Message{Type: "input", Text: "hmm"}
	for i := 1; i <= 3; i++ {
		text, _ := play(t, s, look)
		if warned := strings.Contains(text, "footsteps are coming closer"); warned != (i == 2) {
			t.Fatalf("action %d: %q", i, text)
		}
	}
	text, got := play(t, s, look)
	wantText(t, "seen", text, "He sees the open lock.")
	ended(t, s, text, got)
}

// Leaving the corridor on its last action does not save the player: the
// guard is there when they come back.
func TestDemoCorridorGuardWaitsOnReturn(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	escapeCell(t, s)
	for range 4 {
		play(t, s, ws.Message{Type: "input", Text: "hmm"})
	}
	play(t, s, ws.Message{Type: "click", Item: "door"}) // the corridor's 5th and last action
	if roomOf(s) != "guardroom" {
		t.Fatalf("in %q", roomOf(s))
	}
	play(t, s, ws.Message{Type: "click", Item: "corridor"})
	if roomOf(s) != "corridor" {
		t.Fatalf("in %q", roomOf(s))
	}
	text, got := play(t, s, ws.Message{Type: "click", Item: "door"})
	wantText(t, "caught", text, "sees you standing in the corridor")
	if roomOf(s) != "corridor" {
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
	for i := 2; i <= 5; i++ {
		text, _ := play(t, b, ws.Message{Type: "input", Text: "hmm"})
		if warned := strings.Contains(text, "Footsteps on the stairs"); warned != (i == 4) {
			t.Fatalf("b action %d: %q", i, text)
		}
	}
	// a has used 3 actions: the 4th warns.
	text, _ = play(t, a, ws.Message{Type: "input", Text: "hmm"})
	wantText(t, "a warned", text, "Footsteps on the stairs")
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
				play(t, s, ws.Message{Type: "click", Item: "cell"})
			}
			text, _ := play(t, s, ws.Message{Type: "input", Text: c.say})
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
