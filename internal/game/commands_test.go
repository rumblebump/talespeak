package game

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// cmdRoom offers a command to everyone and one to game masters, keeps a
// global and a state.player key that on_reset clears, and a room timer.
const cmdRoom = `
commands = {
  roll = { help = "roll a die" },
  skip = { help = "skip a turn", gm = true },
  ["Bad Name"] = { help = "never listed" },
  quiet = "not a table",
}
entered = 0
function on_enter(p)
  entered = entered + 1
  say("enter " .. entered .. " kept " .. tostring(state.player.get("kept")))
end
function on_input(p, text)
  if text == "keep" then state.player.set("kept", true) say("kept")
  elseif text == "ask" then say("asked " .. tostring(ui.dialog("Sure?", {"Yes", "No"})))
  elseif text == "timer" then timer.start("clock", 60, {room = true, show = "Clock"})
  else say("input " .. text) end
end
function on_command(p, name, args)
  say("command " .. name .. " [" .. args .. "] gm=" .. tostring(p.gm))
end
function on_reset(p)
  state.player.set("kept", nil)
  say("reset for " .. p.name)
end`

const cmdRoomB = `
function on_enter(p) say("in b") end
function on_input(p, text) say("b " .. text) end`

// notices lists the notices of ms as "role from: text".
func notices(ms []ws.Message) []string {
	var out []string
	for _, m := range ms {
		if m.Type == "notice" {
			s := m.Role
			if m.From != "" {
				s += " " + m.From
			}
			out = append(out, s+": "+m.Text)
		}
	}
	return out
}

func TestHelpListsWhatThePlayerMayUse(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()

	got := strings.Join(notices(input(player, "/help")), "\n")
	for _, want := range []string{"/help: what you can type here", "In this room:", "/roll: roll a die"} {
		if !strings.Contains(got, want) {
			t.Errorf("player's /help lacks %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"/say", "/reset", "/skip", "/audit", "Bad Name", "quiet", "game master here"} {
		if strings.Contains(got, not) {
			t.Errorf("player's /help shows %q:\n%s", not, got)
		}
	}
	got = strings.Join(notices(input(gm, "/HELP")), "\n") // names in any case
	for _, want := range []string{"/say TEXT", "/reset room [ROOM]", "/go ROOM", "/players", "/audit", "/skip: skip a turn (game master)", "/roll"} {
		if !strings.Contains(got, want) {
			t.Errorf("GM's /help lacks %q:\n%s", want, got)
		}
	}
}

func TestCommandsNeverReachOnInput(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	for line, want := range map[string]string{
		"/reset room": "server: There is no command /reset here. Type /help to see what you can type.",
		"/say hi":     "server: There is no command /say here. Type /help to see what you can type.",
		"/skip":       "server: There is no command /skip here. Type /help to see what you can type.",
		"/nope":       "server: There is no command /nope here. Type /help to see what you can type.",
		"/":           "server: That is not a command. Type /help to see what you can type.",
		"/<b>":        "server: That is not a command. Type /help to see what you can type.",
	} {
		ms := input(player, line)
		if n := notices(ms); len(n) != 1 || n[0] != want {
			t.Errorf("%q = %q, want %q", line, n, want)
		}
		if strings.Contains(texts(ms), "input") {
			t.Errorf("%q reached on_input: %s", line, texts(ms))
		}
	}
	// A line with a slash elsewhere is ordinary input.
	if got := texts(input(player, "a/b")); got != "input a/b" {
		t.Errorf("a/b = %q", got)
	}
}

func TestRoomCommands(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	gm, _ := w.NewSession("", ws.Who{GM: true, Name: "ignored without a store"})
	defer gm.Close()
	elsewhere, _ := w.NewSession("", ws.Who{})
	defer elsewhere.Close()
	input(elsewhere, "/go b") // not a GM: refused, stays in a
	if got := texts(input(elsewhere, "where")); got != "input where" {
		t.Fatalf("a player moved with /go: %q", got)
	}

	if got := texts(input(player, "/Roll  2d6 ")); got != "command roll [2d6] gm=false" {
		t.Errorf("player /roll = %q", got)
	}
	if got := texts(input(gm, "/skip anna")); got != "command skip [anna] gm=true" {
		t.Errorf("GM /skip = %q", got)
	}
	// The others in the room see that the GM acted; the log has it.
	for _, s := range []ws.Session{player, elsewhere} {
		if n := notices(pending(t, s)); len(n) != 1 || n[0] != "gm traveler 2: used /skip anna" {
			t.Errorf("mark = %q", n)
		}
	}
	if got := strings.Join(notices(input(gm, "/audit")), "\n"); !strings.Contains(got, `traveler 2: /skip "anna"`) {
		t.Errorf("/audit = %q", got)
	}
	// While a question is open, a room's command waits for the answer; the
	// engine's do not.
	input(player, "ask")
	if ms := input(player, "/roll"); !isError(ms) || ms[0].Message != "Answer the open question first." {
		t.Errorf("/roll with a question open = %+v", ms)
	}
	if n := notices(input(player, "/help")); len(n) != 1 {
		t.Errorf("/help with a question open = %q", n)
	}
}

func TestSayReachesEveryPlayer(t *testing.T) {
	w, c := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	near, _ := w.NewSession("", ws.Who{})
	defer near.Close()
	far, _ := w.NewSession("", ws.Who{})
	defer far.Close()
	input(far, "/go b") // refused: far is no GM
	if n := notices(input(gm, "/say  Hello all ")); len(n) != 1 || n[0] != "gm traveler: Hello all" {
		t.Fatalf("GM's own copy = %q", n)
	}
	for _, s := range []ws.Session{near, far} {
		if n := notices(pending(t, s)); len(n) != 1 || n[0] != "gm traveler: Hello all" {
			t.Errorf("pushed = %q", n)
		}
	}
	if n := notices(input(gm, "/say")); len(n) != 1 || !strings.Contains(n[0], "Type /say and") {
		t.Errorf("/say alone = %q", n)
	}
	// A held-down key: five at once, then one a second.
	for i := range 4 {
		if n := notices(input(gm, "/say again")); len(n) != 1 || n[0] != "gm traveler: again" {
			t.Fatalf("say %d = %q", i, n)
		}
	}
	if n := notices(input(gm, "/say too fast")); len(n) != 1 || n[0] != "server: "+slowDownMessage {
		t.Errorf("sixth = %q", n)
	}
	c.Advance(time.Second)
	if n := notices(input(gm, "/say later")); len(n) != 1 || n[0] != "gm traveler: later" {
		t.Errorf("a second later = %q", n)
	}
}

// A room's GM commands reach the others in the room as a mark, so they
// count toward the same limit as /say; the mark shows a short version of
// the arguments, the audit log all of them.
func TestRoomGMCommandsSlowedAndShortened(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	long := strings.Repeat("é", maxMarkArgs) // two bytes each: the cut falls inside one
	if got := texts(input(gm, "/skip "+long)); got != "command skip ["+long+"] gm=true" {
		t.Errorf("/skip long = %q", got)
	}
	want := "gm traveler: used /skip " + strings.Repeat("é", maxMarkArgs/2) + "…"
	if n := notices(pending(t, player)); len(n) != 1 || n[0] != want {
		t.Errorf("mark = %q, want %q", n, want)
	}
	for i := range broadcastBurst - 1 {
		if got := texts(input(gm, "/skip")); got != "command skip [] gm=true" {
			t.Fatalf("skip %d = %q", i, got)
		}
	}
	ms := input(gm, "/skip")
	if n := notices(ms); len(n) != 1 || n[0] != "server: "+slowDownMessage || strings.Contains(texts(ms), "command skip") {
		t.Errorf("sixth = %+v", ms)
	}
	if got := strings.Join(notices(input(gm, "/audit")), "\n"); !strings.Contains(got, strconv.Quote(long)) {
		t.Errorf("the log lost the arguments: %q", got)
	}
}

// The limit belongs to the account: connecting again does not refill it.
func TestBroadcastLimitOutlivesReconnect(t *testing.T) {
	w, c := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{Account: 7, GM: true})
	for range broadcastBurst {
		input(gm, "/say hi")
	}
	gm.Close()
	again, _ := w.NewSession("", ws.Who{Account: 7, GM: true})
	defer again.Close()
	if n := notices(input(again, "/say hi")); len(n) != 1 || n[0] != "server: "+slowDownMessage {
		t.Errorf("after reconnecting = %q", n)
	}
	other, _ := w.NewSession("", ws.Who{Account: 8, GM: true})
	defer other.Close()
	if n := notices(input(other, "/say hi")); len(n) != 1 || n[0] == "server: "+slowDownMessage {
		t.Errorf("another game master = %q", n)
	}
	// Full again, a game master's count is forgotten when a new one starts.
	c.Advance(time.Hour)
	input(again, "/say hi")
	third, _ := w.NewSession("", ws.Who{Account: 9, GM: true})
	defer third.Close()
	input(third, "/say hi")
	w.mu.Lock()
	n := len(w.broadcasts)
	w.mu.Unlock()
	if n != 2 {
		t.Errorf("counts kept = %d, want 2 (7 and 9)", n)
	}
}

func TestResetRoom(t *testing.T) {
	w, c := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	away, _ := w.NewSession("", ws.Who{GM: true})
	defer away.Close()
	input(away, "/go b")
	input(player, "keep")
	input(player, "timer")
	pending(t, gm)
	input(player, "ask") // a question open: the reset closes it
	if got := texts(input(gm, "/reset")); got != "notice" {
		t.Errorf("/reset alone = %q", got)
	}
	ms := input(gm, "/reset room")
	if got := texts(ms); got != "notice | reset for traveler | enter 1 kept nil | timer" { // the room timer is gone
		t.Errorf("GM's reset = %q", got)
	}
	got := pending(t, player)
	if s := texts(got); !strings.Contains(s, "close | notice | reset for traveler 2 | enter 1 kept nil") {
		t.Errorf("player's reset = %q", s)
	}
	if n := notices(got); len(n) != 1 || n[0] != "gm traveler: started this room over." {
		t.Errorf("player's mark = %q", n)
	}
	c.Advance(DeliverGap)
	gone := false
	for _, m := range append(pending(t, player), got...) {
		if m.Type == "timer" && m.ID == "room:clock" && m.Ms == 0 {
			gone = true
		}
	}
	if !gone {
		t.Error("the room's timer was not removed for the player")
	}
	if s := texts(input(player, "ask")); s != "dialog" {
		t.Errorf("the player can ask again: %q", s)
	}
	// The player in room b is untouched.
	if ms, _ := away.(ws.Pusher).Pending(); len(ms) != 0 {
		t.Errorf("away got %s", texts(ms))
	}
	// Another room, by name, from elsewhere; an unknown one is refused.
	if n := notices(input(away, "/reset room nowhere")); len(n) != 1 || !strings.Contains(n[0], `no room "nowhere"`) {
		t.Errorf("unknown room = %q", n)
	}
	ms = input(away, "/reset room A")
	if n := notices(ms); len(n) != 1 || !strings.Contains(n[0], "started A (a) over for the 2 players in it.") {
		t.Errorf("reset from elsewhere = %q", n)
	}
	if got := texts(pending(t, gm)); !strings.Contains(got, "enter 1 kept nil") {
		t.Errorf("the GM in a = %q", got)
	}
}

func TestGoCommand(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if n := notices(input(gm, "/go")); len(n) != 1 || !strings.Contains(n[0], "/rooms") {
		t.Errorf("/go alone = %q", n)
	}
	if got := texts(input(gm, "/go B")); got != "items:rope | in b" {
		t.Errorf("/go B = %q", got)
	}
	if got := texts(input(gm, "hello")); got != "b hello" {
		t.Errorf("after /go = %q", got)
	}
	if got := strings.Join(notices(input(gm, "/rooms")), ""); !strings.Contains(got, "B (b) (you are here)") || !strings.Contains(got, "C (c)") {
		t.Errorf("/rooms = %q", got)
	}
	if got := strings.Join(notices(input(gm, "/players")), ""); !strings.Contains(got, "1 online:\ntraveler in B (b) (game master)") {
		t.Errorf("/players = %q", got)
	}
}

func TestAuditInMemory(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if n := notices(input(gm, "/audit")); len(n) != 1 || !strings.Contains(n[0], "No game master has done anything") {
		t.Errorf("empty /audit = %q", n)
	}
	input(gm, "/say one")
	input(gm, "/go b")
	got := strings.Join(notices(input(gm, "/audit")), "")
	if i, j := strings.Index(got, `go "b"`), strings.Index(got, `say "one"`); i < 0 || j < 0 || i > j {
		t.Errorf("/audit, the latest first = %q", got)
	}
	if !strings.Contains(got, "2026-10-18 12:00 traveler") {
		t.Errorf("/audit time and name = %q", got)
	}
}

// failingAudit refuses every write.
type failingAudit struct{}

func (failingAudit) AddAudit(context.Context, store.AuditEntry) error { return errors.New("disk full") }
func (failingAudit) AuditLog(context.Context, int64, int) ([]store.AuditEntry, error) {
	return nil, errors.New("disk full")
}

func TestActionTheLogCannotRecordDoesNotHappen(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB, WithAudit(failingAudit{}))
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	for _, line := range []string{"/say hi", "/reset room", "/go b", "/skip"} {
		if ms := input(gm, line); !isError(ms) || ms[0].Message != auditFailed {
			t.Errorf("%s = %s", line, texts(ms))
		}
	}
	if ms, _ := player.(ws.Pusher).Pending(); len(ms) != 0 {
		t.Errorf("the player got %s", texts(ms))
	}
	if ms := input(gm, "/audit"); !isError(ms) {
		t.Errorf("/audit = %s", texts(ms))
	}
}

func TestRoleChangedEndsSessions(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{Account: 7, GM: true})
	other, _ := w.NewSession("", ws.Who{Account: 8})
	defer other.Close()
	if n := w.RoleChanged(7, store.MemberGM); n != 0 {
		t.Errorf("same role ended %d", n)
	}
	if n := w.RoleChanged(0, ""); n != 0 {
		t.Errorf("account 0 ended %d", n)
	}
	if n := w.RoleChanged(7, store.MemberPlayer); n != 1 {
		t.Fatalf("demotion ended %d", n)
	}
	ms, ok := gm.(ws.Pusher).Pending()
	if ok || len(ms) != 1 || ms[0].Message != roleChangedMessage {
		t.Errorf("demoted session: %+v, %v", ms, ok)
	}
	if ms := input(gm, "/say still here?"); !isError(ms) {
		t.Errorf("the ended session still acts: %s", texts(ms))
	}
	if w.Online() != 1 {
		t.Errorf("online = %d", w.Online())
	}
	// Removed from the world: every role ends.
	if n := w.RoleChanged(8, ""); n != 1 {
		t.Errorf("removal ended %d", n)
	}
	// Promotion of a player ends their session too.
	p, _ := w.NewSession("", ws.Who{Account: 9})
	defer p.Close()
	if n := w.RoleChanged(9, store.MemberGM); n != 1 {
		t.Errorf("promotion ended %d", n)
	}
}
