package game

// Tests for bugs the test-engineer found in T-0046, fixed since.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// words splits at anything but a letter or digit, and combining marks are
// neither: a word in Hindi or Thai, or a name typed with a combining
// umlaut (NFD, as some keyboards send it), falls apart into pieces shorter
// than minTypedRunes, none of them remembered, so the whole name is
// shared. Marks (unicode.IsMark) belong to their word.
func TestTypedWordsWithMarks(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	for _, name := range []string{"राहुल", "Jörg", "สมชาย"} {
		if got := texts(input(a, "share "+name)); !strings.HasPrefix(got, "nil state.world.set: it holds words a player typed") {
			t.Errorf("share %q = %q (words: %q)", name, got, words(name))
		}
	}
}

// maxTypedPlayers keeps "the latest to type", but typedOrder is the order
// players first typed in: a player who types on and on is forgotten first
// once maxTypedPlayers others typed once.
func TestTypedPlayersLatestKept(t *testing.T) {
	w := bareWorld()
	first := &session{w: w, id: "first"}
	first.typed("Quellbach")
	for i := 1; i < maxTypedPlayers; i++ {
		(&session{w: w, id: "p" + strconv.Itoa(i)}).typed("word" + strconv.Itoa(i))
	}
	first.typed("Moosgrund") // first types again: now the latest
	(&session{w: w, id: "new"}).typed("Lindenau")
	if w.typedBy["first"] == nil || w.shareable("Moosgrund") == nil {
		t.Error("the player who typed last was forgotten")
	}
}

// From the security review of T-0046.

// A whole number in state.world is checked like a string: a phone number
// typed and kept with tonumber is refused.
func TestTypedNumbersInWorld(t *testing.T) {
	const room = `
function on_input(p, text)
  local n = tonumber(text)
  local ok, err = state.world.set("tel", n and {n} or text)
  say(tostring(ok) .. " " .. tostring(err))
end`
	w, _ := clockWorld(t, room, room)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	if got := texts(input(a, "5551234567")); !strings.HasPrefix(got, "nil state.world.set: it holds words a player typed") {
		t.Errorf("a typed number: %q", got)
	}
	if got := texts(input(a, "42")); got != "true nil" {
		t.Errorf("a small number: %q", got)
	}
}

// What a player typed is forgotten when they leave; what a pack kept in
// their state.player counts as typed again when they come back.
func TestTypedForgottenOnLeave(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	a, _ := w.NewSession("", ws.Who{})
	input(a, "tell Quellbach")
	a.Close()
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.shareable("Quellbach"); err != nil || len(w.typedBy) != 0 {
		t.Errorf("after leaving: %v %v", err, w.typedBy)
	}
}

// A pack voice that starts with a player's name, or names a player who
// left, is dropped too.
func TestVoiceNamesPlayersWhoJoined(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	b.Close()
	for from, want := range map[string]string{"Traveler 2 (player)": "", "traveler 2": "", "Travelers": "Travelers", "Whisp": "Whisp"} {
		got := input(a, "voice "+from)
		if len(got) != 1 || got[0].From != want {
			t.Errorf("from %q: %+v", from, got)
		}
	}
}
