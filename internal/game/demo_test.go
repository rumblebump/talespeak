package game

import (
	"regexp"
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

// escaped checks the player got out of the castle into the clearing,
// then walks them home and checks the story is over.
func escaped(t *testing.T, s ws.Session, text string, got []ws.Message) {
	t.Helper()
	wantText(t, "escape", text, "You escaped. Well done!")
	if roomOf(s) != "clearing" {
		t.Fatalf("after the escape: in %q, want the clearing", roomOf(s))
	}
	if items := lastItems(t, got); items["path"] != "path home" {
		t.Fatalf("items in the clearing: %v", items)
	}
	text, got = play(t, s, ws.Message{Type: "click", Item: "path"})
	wantText(t, "home", text, "lights of home")
	ended(t, s, text, got)
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

// noteRE is the note under the corridor's stone, with the box code.
var noteRE = regexp.MustCompile(`BOX (\d) (\d) (\d)`)

// noteCode returns the box code the note in text shows, as the box wants
// it: "BOX 4 1 7" is "417".
func noteCode(t *testing.T, text string) string {
	t.Helper()
	m := noteRE.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no note with the box code in %q", text)
	}
	return m[1] + m[2] + m[3]
}

// boxCode returns w's box code, as a new player of w reads it under the
// stone.
func boxCode(t *testing.T, w *World) string {
	t.Helper()
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)
	text, _ := play(t, s, ws.Message{Type: "click", Item: "stone"})
	return noteCode(t, text)
}

// otherCode is a box code that is not code.
func otherCode(code string) string {
	return string('0'+(code[0]-'0'+1)%10) + code[1:]
}

func TestDemoEscape(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)
	text, got := play(t, s, ws.Message{Type: "click", Item: "stone"})
	wantText(t, "stone", text, "You lift the stone with the X.")
	code := noteCode(t, text)
	if items := lastItems(t, got); items["stone"] != "lifted stone" {
		t.Fatalf("items after the stone: %v", items)
	}
	text, _ = play(t, s, ws.Message{Type: "click", Item: "door"})
	wantText(t, "guard room", text, "Nobody is here.")
	text, _ = play(t, s, ws.Message{Type: "click", Item: "backdoor"})
	wantText(t, "locked", text, "The back door is locked.")
	_, got = play(t, s, ws.Message{Type: "click", Item: "box"})
	text, got = play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: code[:1] + "-" + code[1:2] + "-" + code[2:]})
	wantText(t, "box", text, "big iron key")
	if items := lastItems(t, got); items["box"] != "open iron box" {
		t.Fatalf("items after the box: %v", items)
	}
	text, got = play(t, s, ws.Message{Type: "click", Item: "backdoor"})
	escaped(t, s, text, got)
}

// Going down the stairs walks into the guard.
func TestDemoCorridorStairs(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
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
	s, _ := w.NewSession("", ws.Who{})
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
	s, _ := w.NewSession("", ws.Who{})
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
		s, _ := w.NewSession("", ws.Who{})
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
	code := boxCode(t, w)
	_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
	_, got = play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: code})
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
		s, _ := w.NewSession("", ws.Who{})
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
	s, _ := w.NewSession("", ws.Who{})
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
	s, _ := w.NewSession("", ws.Who{})
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
