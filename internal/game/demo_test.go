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

func TestDemoEscape(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	sendAway(t, s)
	text, _ := play(t, s, ws.Message{Type: "input", Text: "ask for water"})
	wantText(t, "talk while away", text, "The guard's stool is empty.")
	openLock(t, s)
	text, got := play(t, s, ws.Message{Type: "click", Item: "lock"})
	wantText(t, "escape", text, "You escaped. Well done!")
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
