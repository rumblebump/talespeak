package game

import (
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// The demo's on_reset (T-0011): a game master's /reset room puts the
// guard back in the cell, opens the corridor again and stops the dice game
// in the clearing.

func TestDemoResetCell(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	sendAway(t, s)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if roomOf(gm) != "cell" {
		t.Fatalf("GM in %s", roomOf(gm))
	}
	input(gm, "/reset room")
	got := pending(t, s)
	if n := notices(got); len(n) != 1 || n[0] != "gm traveler 2: started this room over." {
		t.Errorf("mark = %q", n)
	}
	wantText(t, "reset", texts(got), "A guard leans against the bars, half asleep.")
	if m := lastOf(got, "room"); m == nil || len(m.Items) == 0 || hasItemLabel(m.Items, "empty stool") {
		t.Errorf("the guard is still away: %+v", m)
	}
	sess := s.(*session)
	w.mu.Lock()
	_, away := sess.state.Encoded("guard_away")
	_, solved := sess.state.Encoded("solved_cell")
	w.mu.Unlock()
	if away || solved {
		t.Errorf("after the reset: guard_away %v, solved_cell %v", away, solved)
	}
	// The first cup again, not the fourth.
	wantText(t, "cup 1", askWater(t, s), "slides a cup of water")
}

func TestDemoResetCorridor(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	escapeCell(t, s)
	if roomOf(s) != "corridor" {
		t.Fatalf("in %s", roomOf(s))
	}
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if n := notices(input(gm, "/reset room corridor")); len(n) != 1 || !strings.Contains(n[0], "for the 1 players in it") {
		t.Errorf("reset from the cell = %q", n)
	}
	got := texts(pending(t, s))
	wantText(t, "reset", got, "You are out!")
	sess := s.(*session)
	w.mu.Lock()
	_, solved := sess.state.Encoded("solved_corridor")
	w.mu.Unlock()
	if solved {
		t.Error("the corridor is still solved")
	}
}

func TestDemoResetClearingStopsDice(t *testing.T) {
	w, c := clearingWorld(t)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	ann, _ := w.NewSession("", ws.Who{})
	defer ann.Close()
	bob, _ := w.NewSession("", ws.Who{})
	defer bob.Close()
	wantText(t, "start", texts(click(ann, "dice")), "You pick up the dice")
	heard(t, c, bob)
	heard(t, c, gm)
	if got := texts(click(bob, "dice")); !strings.HasPrefix(got, "Wait for your turn") {
		t.Fatalf("bob before the reset = %q", got)
	}

	ms := input(gm, "/reset room")
	if m := lastOf(ms, "timer"); m == nil || m.ID != "room:round" || m.Ms != 0 {
		t.Errorf("GM: the turn timer is not removed: %s", texts(ms))
	}
	heardBob, got := heard(t, c, bob)
	if n := notices(got); len(n) != 1 || n[0] != "gm traveler: started this room over." {
		t.Errorf("bob's mark = %q", n)
	}
	if m := lastOf(got, "timer"); m == nil || m.ID != "room:round" || m.Ms != 0 {
		t.Errorf("bob: the turn timer is not removed: %s", heardBob)
	}
	if got := texts(click(bob, "dice")); strings.Contains(got, "Wait for your turn") || !strings.Contains(got, "You pick up the dice") {
		t.Errorf("bob after the reset = %q", got)
	}
}

func hasItemLabel(items []ws.Item, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}
