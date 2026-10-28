package game

import (
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The demo's death setting (lib/death.lua, T-0018): respawn by default,
// revive with the game master's /revive, or stay dead.

// deathDemo is the shipped demo with its death setting at mode, on a fake
// clock.
func deathDemo(t *testing.T, mode string) (*World, *fakeClock) {
	t.Helper()
	p, err := pack.Load(content.Demo())
	if err != nil {
		t.Fatal(err)
	}
	c := newClock()
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithClock(c.Now, c.After))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	if mode != "" {
		setSetting(t, w, "death", mode)
	}
	return w, c
}

// caughtAtDoor tries the cell door while the guard watches.
func caughtAtDoor(t *testing.T, s ws.Session) string {
	t.Helper()
	_, got := play(t, s, ws.Message{Type: "click", Item: "lock"})
	if len(got) != 1 || got[0].Type != "dialog" {
		t.Fatalf("lock: %+v", got)
	}
	text, _ := play(t, s, ws.Message{Type: "choose", ID: got[0].ID, Option: 1})
	wantText(t, "die", text, "After a few days, you die.")
	return text
}

// By default the player wakes up in the cell, which starts over.
func TestDemoDeathRespawn(t *testing.T) {
	w, _ := deathDemo(t, "")
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	text := caughtAtDoor(t, s)
	wantText(t, "respawn", text, "You wake up on the straw of your cell.")
	wantText(t, "cell again", text, "A guard leans against the bars, half asleep.")
	if roomOf(s) != "cell" {
		t.Fatalf("in %q", roomOf(s))
	}
	text, _ = play(t, s, ws.Message{Type: "input", Text: "search the straw"})
	if strings.Contains(text, "dead") {
		t.Errorf("after the respawn: %q", text)
	}
}

// In revive mode the player waits until a game master revives them.
func TestDemoDeathRevive(t *testing.T) {
	w, c := deathDemo(t, "revive")
	mira, _ := w.NewSession("", ws.Who{})
	defer mira.Close()
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	text := caughtAtDoor(t, mira)
	wantText(t, "dead", text, "You are dead. Wait: someone may still bring you back.")
	if after, _ := play(t, mira, ws.Message{Type: "input", Text: "search the straw"}); after != "You are dead. Wait: someone may still bring you back." {
		t.Fatalf("while dead: %q", after)
	}
	name := mira.(*session).name
	if got := texts(gm.Handle(ws.Message{Type: "input", Text: "/revive " + name})); !strings.Contains(got, name+" lives again.") {
		t.Fatalf("/revive = %q", got)
	}
	c.Advance(DeliverGap)
	ms, _ := mira.(ws.Pusher).Pending()
	if got := texts(ms); !strings.Contains(got, "You are alive again!") || !strings.Contains(got, "room:cell") {
		t.Fatalf("revived: %q", got)
	}
	if after, _ := play(t, mira, ws.Message{Type: "input", Text: "search the straw"}); strings.Contains(after, "dead") {
		t.Errorf("after reviving: %q", after)
	}
}

// In stay mode nobody comes back, also when the game master asks.
func TestDemoDeathStay(t *testing.T) {
	w, _ := deathDemo(t, "stay")
	mira, _ := w.NewSession("", ws.Who{})
	defer mira.Close()
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	caughtAtDoor(t, mira)
	name := mira.(*session).name
	if got := texts(gm.Handle(ws.Message{Type: "input", Text: "/revive " + name})); !strings.Contains(got, "in this world, the dead stay dead") {
		t.Fatalf("/revive = %q", got)
	}
	// The game master switches the world to respawn: the next action
	// wakes the player up.
	gm.Handle(ws.Message{Type: "input", Text: "/set death respawn"})
	if text, _ := play(t, mira, ws.Message{Type: "input", Text: "look"}); !strings.Contains(text, "You wake up on the straw") {
		t.Errorf("after /set death respawn: %q", text)
	}
}
