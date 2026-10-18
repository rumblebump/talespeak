package game

import (
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// state.player is the player's, in every room, and only theirs.
func TestStatePlayerAcrossRooms(t *testing.T) {
	w := roomsWorld(t, `
function on_enter() say("A sees " .. tostring(state.player.get("nail"))) end
function on_click() state.player.set("nail", true) room.go("b") end`,
		`function on_enter() say("B sees " .. tostring(state.player.get("nail"))) end`)
	s1, first := w.NewSession("192.0.2.1/32", ws.Who{})
	defer s1.Close()
	if texts(first) != "A sees nil" {
		t.Fatalf("first = %q", texts(first))
	}
	if got := texts(s1.Handle(ws.Message{Type: "click", Item: "box"})); got != "items:rope | B sees true" {
		t.Errorf("click = %q", got)
	}
	s2, first := w.NewSession("192.0.2.2/32", ws.Who{})
	defer s2.Close()
	if texts(first) != "A sees nil" {
		t.Errorf("another player sees %q", texts(first))
	}
	w.mu.Lock()
	total, client := w.stateTotal, w.clientState["192.0.2.1/32"]
	w.mu.Unlock()
	if total <= 0 || client != total {
		t.Errorf("stateTotal = %d, client = %d", total, client)
	}
	s1.Close()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stateTotal != 0 || len(w.clientState) != 0 {
		t.Errorf("after leaving: stateTotal = %d, clientState = %v", w.stateTotal, w.clientState)
	}
}

// Player state counts toward the world and client memory limits.
func TestStatePlayerSharesMemoryLimits(t *testing.T) {
	src := `function on_enter()
  local ok, why = state.player.set("big", string.rep("x", 16000))
  say(tostring(ok) .. " " .. tostring(why))
end`
	limits := script.DefaultLimits
	limits.StateMemory = 40000
	w, err := NewWorld(packWith(src), limits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var said []string
	for i := range 3 {
		s, first := w.NewSession("", ws.Who{})
		defer s.Close()
		if len(first) != 1 {
			t.Fatalf("player %d: %+v", i, first)
		}
		said = append(said, first[0].Text)
	}
	if said[0] != "true nil" || said[1] != "true nil" || !strings.Contains(said[2], "this world is full right now") {
		t.Errorf("said = %q", said)
	}

	w2, err := NewWorld(packWith(src), script.DefaultLimits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	a, first := w2.NewSession("192.0.2.1/32", ws.Who{})
	defer a.Close()
	if first[0].Text != "true nil" {
		t.Fatalf("first player: %+v", first)
	}
	// Room for a second instance of the script and half a value.
	w2.mu.Lock()
	w2.clientMemory = w2.clientKept("192.0.2.1/32") + a.(*session).state.Size()/2
	w2.mu.Unlock()
	b, first := w2.NewSession("192.0.2.1/32", ws.Who{})
	defer b.Close()
	if len(first) != 1 || !strings.Contains(first[0].Text, "too many players from this network") {
		t.Errorf("second player of the address: %+v", first)
	}
}
