package game

import (
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// The game master's turn commands in the demo's clearing (lib/round.lua):
// /turns sets an order, /skip ends the current turn, /free stops turns;
// players cannot use them, and the others hear of each change.
func TestClearingGMTurnCommands(t *testing.T) {
	w, c := clearingWorld(t)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	ann, _ := w.NewSession("", ws.Who{})
	defer ann.Close()
	bob, _ := w.NewSession("", ws.Who{})
	defer bob.Close()

	if n := notices(input(ann, "/turns")); len(n) != 1 || !strings.Contains(n[0], "no command /turns") {
		t.Errorf("a player's /turns = %q", n)
	}
	if got := strings.Join(notices(input(ann, "/help")), ""); strings.Contains(got, "/skip") {
		t.Errorf("a player's /help lists /skip: %q", got)
	}
	if got := strings.Join(notices(input(gm, "/help")), ""); !strings.Contains(got, "/skip: end the turn of whoever is acting now (game master)") {
		t.Errorf("GM's /help = %q", got)
	}

	// Names after /turns set the order; unknown ones are said.
	got := texts(input(gm, "/turns traveler 3, Traveler 2, zed"))
	wantText(t, "/turns", got, "Not here: zed")
	wantText(t, "/turns", got, "Round 1. Order: traveler 3 (acting), traveler 2.")
	heardBob, _ := heard(t, c, bob)
	wantText(t, "bob's turn", heardBob, "Your turn!")
	if n := notices(pending(t, ann)); len(n) != 1 || n[0] != "gm traveler: used /turns traveler 3, Traveler 2, zed" {
		t.Errorf("ann's mark = %q", n)
	}

	got = texts(input(gm, "/skip"))
	wantText(t, "/skip", got, "traveler 3 was skipped by the game master. Round 1. Order: traveler 3, traveler 2 (acting).")
	heardAnn, _ := heard(t, c, ann)
	wantText(t, "ann's turn", heardAnn, "Your turn!")

	got = texts(input(gm, "/free"))
	wantText(t, "/free", got, "Everyone acts at once now.")
	if got := texts(click(bob, "dice")); strings.Contains(got, "Wait for your turn") {
		t.Errorf("after /free bob waits: %q", got)
	}
}
