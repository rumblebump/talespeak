package game

import (
	"strconv"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// Room events (events.go) and what players type staying with them
// (typed.go).

const eventRoom = `
function on_input(p, text)
  local cmd, rest = text, ""
  local sp = string.find(text, " ", 1, true)
  if sp then cmd, rest = string.sub(text, 1, sp - 1), string.sub(text, sp + 1) end
  if cmd == "open" then
    say("You open the door.")
    room.event(p.name .. " opens the door.")
  elseif cmd == "all" then
    room.event("The bell rings " .. rest .. " times.", {all = true})
  elseif cmd == "tell" then
    local ok, a, b = pcall(room.event, rest)
    say(tostring(a) .. " " .. tostring(b))
  elseif cmd == "share" then
    local ok, err = state.world.set("note", {text = rest})
    say(tostring(ok) .. " " .. tostring(err))
  elseif cmd == "keep" then
    say(tostring(state.player.set("note", rest)))
  elseif cmd == "long" then
    local ok, err = pcall(room.event, string.rep("x", 501))
    say(tostring(err))
  elseif cmd == "spam" then
    local ok, err = pcall(function() for i = 1, 6 do room.event("event " .. i) end end)
    say(tostring(err))
  elseif cmd == "voice" then
    local ok, err = pcall(say, "hello", {from = rest})
    if not ok then say(err) end
  elseif cmd == "go" then
    room.go("b")
  elseif cmd == "reveal" then
    local ok, err = room.event("You told me: " .. rest)
    say(tostring(ok) .. " " .. tostring(err))
  elseif cmd == "timer" then
    local ok, err = timer.start("t", 60, {room = true, show = rest})
    say(tostring(ok) .. " " .. tostring(err))
  end
end
`

// eventTypes lists the events in ms as "text" or "replay:text".
func eventTypes(ms []ws.Message) string {
	var parts []string
	for _, m := range ms {
		if m.Type != "event" {
			continue
		}
		if m.Replay {
			parts = append(parts, "replay:"+m.Text)
		} else {
			parts = append(parts, m.Text)
		}
	}
	return strings.Join(parts, " | ")
}

// The other players in the room read an event at once; the player whose
// hook runs only with all; players elsewhere never.
func TestRoomEventReachesTheRoom(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	c, _ := w.NewSession("", ws.Who{})
	defer c.Close()
	input(c, "go")
	pending(t, a)
	pending(t, b)

	if got := texts(input(a, "open")); got != "You open the door." {
		t.Errorf("a: %q", got)
	}
	got := pending(t, b)
	if eventTypes(got) != "traveler opens the door." || got[0].ID == "" {
		t.Errorf("b: %+v", got)
	}
	if got := pending(t, c); len(got) != 0 {
		t.Errorf("c in another room: %+v", got)
	}
	if got := eventTypes(input(a, "all 3")); got != "The bell rings 3 times." {
		t.Errorf("a with all: %q", got)
	}
	if got := eventTypes(pending(t, b)); got != "The bell rings 3 times." {
		t.Errorf("b with all: %q", got)
	}
}

// A player who connects reads the room's last MaxRoomEvents events, the
// oldest first, marked replay, with the ids they had; /reset room forgets
// them, and the GM's /sayroom is never kept.
func TestRoomEventsReplayed(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	a, _ := w.NewSession("", ws.Who{})
	ids := map[string]bool{}
	for i := 1; i <= MaxRoomEvents+2; i++ {
		input(a, "all "+strconv.Itoa(i))
	}
	for _, m := range pending(t, gm) {
		if m.Type == "event" {
			ids[m.ID] = true
		}
	}
	if len(ids) != MaxRoomEvents+2 {
		t.Fatalf("event ids are not unique: %v", ids)
	}
	input(gm, "/sayroom here Line up, please.")
	a.Close()

	b, first := w.NewSession("", ws.Who{})
	defer b.Close()
	var want []string
	for i := 3; i <= MaxRoomEvents+2; i++ {
		want = append(want, "replay:The bell rings "+strconv.Itoa(i)+" times.")
	}
	if got := eventTypes(first); got != strings.Join(want, " | ") {
		t.Fatalf("replay = %q", got)
	}
	for _, m := range first {
		if m.Type == "event" && !ids[m.ID] {
			t.Errorf("replayed %q with a new id %q", m.Text, m.ID)
		}
		if m.Type == "notice" {
			t.Errorf("a game master's line was kept: %+v", m)
		}
	}
	if first[0].Type != "event" {
		t.Errorf("the replay comes before the room: %+v", first[0])
	}

	input(gm, "/reset room")
	c, first := w.NewSession("", ws.Who{})
	defer c.Close()
	if got := eventTypes(first); got != "" {
		t.Errorf("after /reset room: %q", got)
	}
}

// room.event refuses a seventh call in one hook, empty and long texts.
func TestRoomEventLimits(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	if got := texts(input(a, "spam")); !strings.Contains(got, "more than 5 room events") {
		t.Errorf("spam: %q", got)
	}
	if got := texts(input(a, "tell ")); !strings.Contains(got, "an event is 1 to 500 bytes") {
		t.Errorf("empty: %q", got)
	}
	if got := texts(input(a, "long")); !strings.Contains(got, "an event is 1 to 500 bytes") {
		t.Errorf("long: %q", got)
	}
}

// Words a player typed never reach the others: not in state.world, a room
// event or a room timer's label. The pack's own words and players' names
// do, and the player keeps what they typed in state.player.
func TestTypedWordsStayWithThePlayer(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	refused := "nil state.world.set: it holds words a player typed"
	for _, tc := range []struct{ line, want string }{
		{"share my number is 0151 2345678", refused},
		{"share call 2345678", refused},             // the same word, typed once before
		{"share Anna lives in Kleinstadt", refused}, // a word typed with it
		{"share the door rings", "true nil"},        // the pack's own words
		{"share traveler 2", "true nil"},            // a player's name
		{"keep Kleinstadt 0151", "true"},            // private: state.player
		{"reveal Kleinstadt", "nil room.event: it holds words a player typed"},
		{"timer Kleinstadt", "nil timer.start: it holds words a player typed"},
	} {
		if got := texts(input(a, tc.line)); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s = %q, want %q", tc.line, got, tc.want)
		}
	}
	if got := eventTypes(pending(t, b)); got != "" {
		t.Errorf("b got: %q", got)
	}
	// What b types is b's: a cannot share it either.
	input(b, "tell Zwergenweg")
	if got := texts(input(a, "share at the Zwergenweg")); !strings.HasPrefix(got, refused) {
		t.Errorf("b's word, shared by a: %q", got)
	}
}

// A game master in GM mode may share what they type.
func TestTypedWordsOfGameMaster(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if got := texts(input(gm, "share Treasure under Hollowoak")); got != "true nil" {
		t.Errorf("gm: %q", got)
	}
}

// A pack's speaker cannot be the game master or staff (a script error),
// nor a player of the world (the line goes without the name).
func TestVoiceNames(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	for _, from := range []string{"GM", "Game Master", "gm Carl", "Admin", "Teacher", "System"} {
		if got := texts(input(a, "voice "+from)); !strings.Contains(got, "from cannot name the game master") {
			t.Errorf("from %q: %q", from, got)
		}
	}
	for from, want := range map[string]string{"Whisp": "Whisp", "Traveler 2": "", "traveler": "", "Holmes": "Holmes"} {
		got := input(a, "voice "+from)
		if len(got) != 1 || got[0].Type != "text" || got[0].From != want {
			t.Errorf("from %q: %+v", from, got)
		}
	}
}

// /sayroom reaches the players in that room, marked as the game
// master's; it is a GM tool, unknown to players.
func TestSayRoom(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	input(b, "go")
	pending(t, a)
	pending(t, gm)

	got := input(gm, "/sayroom b Meet at the gate.")
	if len(got) != 1 || got[0].Role != NoticeGM || !strings.Contains(got[0].Text, "Meet at the gate.") {
		t.Errorf("gm: %+v", got)
	}
	if got := pending(t, b); len(got) != 1 || got[0].Type != "notice" || got[0].Role != NoticeGM || got[0].Text != "Meet at the gate." {
		t.Errorf("b: %+v", got)
	}
	if got := pending(t, a); len(got) != 0 {
		t.Errorf("a elsewhere: %+v", got)
	}
	if got := input(gm, "/sayroom nowhere hi"); len(got) != 1 || !strings.HasPrefix(got[0].Text, "Type /sayroom") {
		t.Errorf("no such room: %+v", got)
	}
	if got := input(a, "/sayroom here hi"); len(got) != 1 || !strings.Contains(got[0].Text, "no command /sayroom") {
		t.Errorf("a player: %+v", got)
	}
}
