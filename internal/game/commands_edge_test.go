package game

import (
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of slash commands, /reset room, the audit log and
// RoleChanged (T-0011), beyond commands_test.go.

func TestSplitCommand(t *testing.T) {
	for _, tt := range []struct{ line, name, args string }{
		{"/help", "help", ""},
		{"/HeLp", "help", ""},
		{"/reset room cell", "reset", "room cell"},
		{"/go   b  ", "go", "b"},
		{"/say  two  spaces inside ", "say", "two  spaces inside"},
		{"/", "", ""},
		{"/ go b", "", "go b"},
		{"//say hi", "/say", "hi"},
		{"help", "help", ""}, // without the slash: the caller checks isCommand first
		{"/Ünïcode x", "ünïcode", "x"},
	} {
		name, args := splitCommand(tt.line)
		if name != tt.name || args != tt.args {
			t.Errorf("splitCommand(%q) = %q, %q; want %q, %q", tt.line, name, args, tt.name, tt.args)
		}
	}
	for line, want := range map[string]bool{"/x": true, "/": true, "x/": false, " /x": false, "": false} {
		if isCommand(line) != want {
			t.Errorf("isCommand(%q) = %v", line, !want)
		}
	}
}

// What the player types is cleaned before it is split: a tab or a newline
// between the name and its arguments is a space, and leading blanks go.
func TestCommandLineCleaned(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if got := texts(input(gm, "  /go\tB\n")); got != "items:rope | in b" {
		t.Errorf("/go with a tab = %q", got)
	}
	for _, line := range []string{"/ go a", "//go a", "/go-a"} {
		ms := input(gm, line)
		if n := notices(ms); len(n) != 1 || !strings.HasPrefix(n[0], "server: ") || roomOf(gm) != "b" {
			t.Errorf("%q = %q, in %s", line, n, roomOf(gm))
		}
	}
	// A line of only blanks after cleaning is nothing at all.
	if ms := input(gm, " \t "); len(ms) != 0 {
		t.Errorf("blank line = %+v", ms)
	}
}

// coreShadowRoom offers commands named like the engine's: they are never
// listed or run, for players or GMs, and on_command never sees them.
const coreShadowRoom = `
commands = {
  say = { help = "the room's say" },
  help = { help = "the room's help" },
  go = { help = "the room's go" },
  dance = { help = "dance" },
}
function on_enter(p) say("in a") end
function on_command(p, name, args) say("room command " .. name) end`

func TestCoreCommandsCannotBeReplaced(t *testing.T) {
	w, _ := clockWorld(t, coreShadowRoom, cmdRoomB)
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()

	help := strings.Join(notices(input(player, "/help")), "\n")
	if strings.Contains(help, "the room's") || !strings.Contains(help, "/dance: dance") {
		t.Errorf("player's /help = %q", help)
	}
	// A player's /say is the GM's /say: hidden, though the room offers one.
	ms := input(player, "/say hi")
	if n := notices(ms); len(n) != 1 || !strings.Contains(n[0], "no command /say") || strings.Contains(texts(ms), "room command") {
		t.Errorf("player's /say = %q", texts(ms))
	}
	if got := texts(input(player, "/dance")); got != "room command dance" {
		t.Errorf("/dance = %q", got)
	}
	if n := notices(input(gm, "/say hi")); len(n) != 1 || n[0] != "gm traveler 2: hi" {
		t.Errorf("GM's /say = %q", n)
	}
	if got := strings.Join(notices(input(gm, "/help")), "\n"); strings.Contains(got, "the room's") {
		t.Errorf("GM's /help = %q", got)
	}
}

// A room's command needs a commands entry and on_command; a room without
// a script has none.
func TestRoomCommandNeedsOnCommand(t *testing.T) {
	w, _ := clockWorld(t, `
commands = { roll = { help = "roll" } }
function on_enter(p) say("in a") end
function on_input(p, text) say("input " .. text) end`, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	ms := input(gm, "/roll")
	if n := notices(ms); len(n) != 1 || !strings.Contains(n[0], "no command /roll") {
		t.Errorf("/roll without on_command = %q", texts(ms))
	}
	// The listing still shows it: /help runs no Lua and reads the table.
	if got := strings.Join(notices(input(gm, "/help")), ""); !strings.Contains(got, "/roll: roll") {
		t.Errorf("/help = %q", got)
	}
	input(gm, "/go c") // no script
	if n := notices(input(gm, "/roll")); len(n) != 1 || !strings.Contains(n[0], "no command /roll") {
		t.Errorf("/roll in a room without a script = %q", n)
	}
	if got := strings.Join(notices(input(gm, "/help")), ""); strings.Contains(got, "In this room") {
		t.Errorf("/help in a room without a script = %q", got)
	}
}

// A GM's room command with nothing after it is marked without a trailing
// space, and logged with an empty detail.
func TestRoomGMCommandWithoutArgs(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	if got := texts(input(gm, "/SKIP")); got != "command skip [] gm=true" {
		t.Errorf("/SKIP = %q", got)
	}
	if n := notices(pending(t, player)); len(n) != 1 || n[0] != "gm traveler: used /skip" {
		t.Errorf("mark = %q", n)
	}
	w.mu.Lock()
	log := append([]store.AuditEntry(nil), w.auditLog...)
	w.mu.Unlock()
	if len(log) != 1 || log[0].Action != "/skip" || log[0].Detail != "" || log[0].Name != "traveler" {
		t.Errorf("audit = %+v", log)
	}
}

// Without an audit store the world keeps the last MaxAuditShown entries;
// /audit lists them, the latest first.
func TestAuditInMemoryCap(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	const n = MaxAuditShown + 5
	for i := range n {
		room := "a"
		if i%2 == 0 {
			room = "b"
		}
		input(gm, "/go "+room)
	}
	w.mu.Lock()
	kept := len(w.auditLog)
	w.mu.Unlock()
	if kept != MaxAuditShown {
		t.Errorf("kept %d entries, want %d", kept, MaxAuditShown)
	}
	got := notices(input(gm, "/audit"))
	if len(got) != 1 {
		t.Fatalf("/audit = %q", got)
	}
	lines := strings.Split(got[0], "\n")
	if len(lines) != 1+MaxAuditShown {
		t.Errorf("/audit has %d lines, want %d:\n%s", len(lines), 1+MaxAuditShown, got[0])
	}
	if !strings.HasSuffix(lines[1], `go "b"`) { // the last of n, i = n-1 = 24: even
		t.Errorf("latest = %q", lines[1])
	}
}

// /say and /reset share one budget of broadcasts; a refused or malformed
// one is not logged, and one with a wrong argument costs nothing.
func TestBroadcastBudgetShared(t *testing.T) {
	w, c := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	input(gm, "/reset nothing")      // malformed: free
	input(gm, "/reset room nowhere") // unknown room: free
	input(gm, "/say")                // empty: free
	for i := range broadcastBurst - 1 {
		if n := notices(input(gm, "/say "+strconv.Itoa(i))); len(n) != 1 || !strings.HasPrefix(n[0], "gm ") {
			t.Fatalf("say %d = %q", i, n)
		}
	}
	if got := texts(input(gm, "/reset room")); !strings.Contains(got, "enter 1") {
		t.Fatalf("fifth broadcast, a reset = %q", got)
	}
	if n := notices(input(gm, "/reset room")); len(n) != 1 || n[0] != "server: "+slowDownMessage {
		t.Errorf("sixth broadcast, a reset = %q", n)
	}
	w.mu.Lock()
	logged := len(w.auditLog)
	w.mu.Unlock()
	if logged != broadcastBurst {
		t.Errorf("logged %d, want %d", logged, broadcastBurst)
	}
	// Commands that reach nobody else are never slowed down.
	for range 10 {
		if n := notices(input(gm, "/players")); len(n) != 1 || strings.Contains(n[0], slowDownMessage) {
			t.Fatalf("/players = %q", n)
		}
	}
	// The budget refills by one a second, up to broadcastBurst.
	c.Advance(time.Hour)
	for i := range broadcastBurst {
		if n := notices(input(gm, "/say again")); len(n) != 1 || n[0] != "gm traveler: again" {
			t.Fatalf("after an hour, say %d = %q", i, n)
		}
	}
	if n := notices(input(gm, "/say over")); len(n) != 1 || n[0] != "server: "+slowDownMessage {
		t.Errorf("the budget refilled past the burst: %q", n)
	}
}

// /go closes an open question first, and /go to the room the GM is in
// enters it again.
func TestGoCommandEdges(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if got := texts(input(gm, "ask")); got != "dialog" {
		t.Fatalf("ask = %q", got)
	}
	if got := texts(input(gm, "/go b")); !strings.HasPrefix(got, "close | ") || !strings.HasSuffix(got, "in b") {
		t.Errorf("/go with a question open = %q", got)
	}
	if got := texts(input(gm, "x")); got != "b x" {
		t.Errorf("the question is gone: %q", got)
	}
	if got := texts(input(gm, "/go b")); !strings.HasSuffix(got, "in b") {
		t.Errorf("/go to the same room = %q", got)
	}
	if n := notices(input(gm, "/go ../a")); len(n) != 1 || !strings.Contains(n[0], "/rooms") {
		t.Errorf("/go to a path = %q", n)
	}
}

// After the world closed, the engine's commands that run no Lua still
// answer; the others say the world has closed.
func TestCommandsAfterWorldClosed(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	w.Close()
	if got := strings.Join(notices(input(gm, "/help")), ""); !strings.Contains(got, "/say TEXT") || strings.Contains(got, "In this room") {
		t.Errorf("/help = %q", got)
	}
	for _, line := range []string{"/go b", "/reset room", "/roll"} {
		if ms := input(gm, line); len(ms) == 0 || ms[len(ms)-1].Type != "error" || ms[len(ms)-1].Message != "This world has closed." {
			t.Errorf("%s = %+v", line, ms)
		}
	}
}

// resetRoom is a room whose on_reset fails in the way its mode says; the
// reset happens all the same, with a new instance and on_enter.
const resetRoom = `
entered = 0
mode = "ok"
function on_enter(p)
  entered = entered + 1
  say("enter " .. entered .. " mode " .. mode)
end
function on_input(p, text)
  mode = text
  say("mode " .. text)
end
function on_reset(p)
  say("on_reset " .. mode)
  if mode == "error" then error("broken reset")
  elseif mode == "ask" then ui.dialog("Really?", {"Yes"})
  elseif mode == "move" then room.go("b")
  elseif mode == "loop" then while true do end
  end
end`

func TestOnResetFailures(t *testing.T) {
	for _, mode := range []string{"ok", "error", "ask", "move", "loop"} {
		t.Run(mode, func(t *testing.T) {
			w, _ := clockWorld(t, resetRoom, cmdRoomB)
			gm, _ := w.NewSession("", ws.Who{GM: true})
			defer gm.Close()
			player, _ := w.NewSession("", ws.Who{})
			defer player.Close()
			pending(t, gm)
			input(player, mode)
			got := input(gm, "/reset room")
			if s := texts(got); !strings.HasSuffix(s, "enter 1 mode ok") {
				t.Errorf("GM's reset = %q", s)
			}
			ms := pending(t, player)
			pushed := texts(ms)
			if !strings.HasSuffix(pushed, "enter 1 mode ok") || lastOf(ms, "error") != nil || lastOf(ms, "dialog") != nil {
				t.Errorf("player's reset = %q", pushed)
			}
			if roomOf(player) != "a" {
				t.Errorf("on_reset moved the player to %s", roomOf(player))
			}
			if s := texts(input(player, "next")); s != "mode next" {
				t.Errorf("after the reset = %q", s)
			}
		})
	}
}

// A GM resets a room with nobody in it, or one without a script.
func TestResetRoomEmptyOrWithoutScript(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if n := notices(input(gm, "/reset room B")); len(n) != 1 || n[0] != "gm traveler: started B (b) over for the 0 players in it." {
		t.Errorf("empty room = %q", n)
	}
	input(gm, "/go c")
	if got := texts(input(gm, "/reset room")); got != "notice | items:key | room:c" {
		t.Errorf("room without a script = %q", got)
	}
}

// A player whose address used up its Lua time is still reset: on_reset
// runs (its time charged), the question closes and the old instance goes.
// The new instance cannot start then, so the player is told to wait, and
// on_enter does not run for them (see the report: docs/gm-tools.md says
// on_enter runs again).
func TestResetRoomBusyPlayer(t *testing.T) {
	w, c := clockWorld(t, cmdRoom, cmdRoomB, WithClientBudget(time.Second, time.Second))
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	player, _ := w.NewSession("192.0.2.1", ws.Who{})
	defer player.Close()
	input(player, "keep")
	input(player, "ask")
	pending(t, gm)
	w.mu.Lock()
	w.budgets["192.0.2.1"].left = -60
	w.mu.Unlock()

	if got := texts(input(gm, "/reset room")); got != "notice | reset for traveler | enter 1 kept nil" {
		t.Errorf("GM's reset = %q", got)
	}
	got := pending(t, player)
	if s := texts(got); s != "close | notice | reset for traveler 2 | error" || got[len(got)-1].Message != busyMessage {
		t.Errorf("busy player's reset = %q %+v", s, got)
	}
	sess := player.(*session)
	w.mu.Lock()
	_, kept := sess.state.Encoded("kept")
	open, env := sess.pending, sess.envs["a"]
	w.mu.Unlock()
	if kept || open != nil || env != nil {
		t.Errorf("after the reset: kept %v, question %v, instance %v", kept, open, env)
	}
	if roomOf(player) != "a" {
		t.Errorf("busy player moved to %s", roomOf(player))
	}
	// Once the time is back, the player plays on in a new instance.
	c.Advance(2 * time.Minute)
	if s := texts(input(player, "x")); s != "input x" {
		t.Errorf("later = %q", s)
	}
}

// edgeRoleRoom keeps a count of on_leave in state.world, has a timed
// question and the player's own timer.
const edgeRoleRoom = `
function on_enter(p) say("in") end
function on_input(p, text)
  if text == "timed" then say("got " .. tostring(ui.dialog("Quick?", {"A", "B"}, {timeout = 5})))
  elseif text == "mine" then timer.start("own", 10) say("started")
  elseif text == "left" then say("left " .. tostring(state.world.get("left")))
  else say("input " .. text) end
end
function on_timer(p, name) say("timer " .. name) end
function on_leave(p) state.world.set("left", (state.world.get("left") or 0) + 1) end`

// RoleChanged ends a session like a leave: on_leave runs once, its
// question and timers stop, its notify is called, and closing it later
// changes nothing.
func TestRoleChangedLikeALeave(t *testing.T) {
	w, c := clockWorld(t, edgeRoleRoom, edgeRoleRoom)
	watcher, _ := w.NewSession("", ws.Who{Account: 1})
	defer watcher.Close()
	gm, _ := w.NewSession("", ws.Who{Account: 7, GM: true})
	notified := 0
	gm.(ws.Pusher).SetNotify(func() { notified++ })
	input(gm, "mine")
	if got := texts(input(gm, "timed")); got != "dialog" {
		t.Fatalf("timed = %q", got)
	}
	if n := w.RoleChanged(7, ""); n != 1 {
		t.Fatalf("RoleChanged ended %d", n)
	}
	if notified != 1 {
		t.Errorf("notify called %d times", notified)
	}
	sess := gm.(*session)
	w.mu.Lock()
	timers, question, envs := len(sess.timers), sess.dialogTimer != nil || sess.pending != nil, len(sess.envs)
	w.mu.Unlock()
	if timers != 0 || question || envs != 0 {
		t.Errorf("after RoleChanged: %d timers, question %v, %d instances", timers, question, envs)
	}
	c.Advance(time.Minute)
	gm.Close()
	gm.Close()
	if n := w.RoleChanged(7, ""); n != 0 {
		t.Errorf("a second RoleChanged ended %d", n)
	}
	if got := texts(input(watcher, "left")); got != "left 1" {
		t.Errorf("on_leave ran: %q", got)
	}
	if ms, ok := gm.(ws.Pusher).Pending(); ok || len(ms) != 1 || ms[0].Message != roleChangedMessage {
		t.Errorf("ended session: %+v, %v", ms, ok)
	}
	if w.Online() != 1 {
		t.Errorf("online = %d", w.Online())
	}
}

// RoleChanged on a closed world, and for sessions without an account,
// does nothing harmful.
func TestRoleChangedClosedWorld(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	s, _ := w.NewSession("", ws.Who{Account: 7, GM: true})
	defer s.Close()
	anon, _ := w.NewSession("", ws.Who{GM: true})
	defer anon.Close()
	w.Close()
	if n := w.RoleChanged(7, store.MemberPlayer); n != 1 {
		t.Errorf("closed world: ended %d", n)
	}
	if n := w.RoleChanged(0, store.MemberPlayer); n != 0 {
		t.Errorf("account 0: ended %d", n)
	}
	w.End("bye")
	if n := w.RoleChanged(7, ""); n != 0 {
		t.Errorf("after End: ended %d", n)
	}
}

// RoleChanged races players' messages, their leaving and other
// RoleChanged calls: every session of the account ends exactly once,
// nothing else does, and the race detector stays quiet.
func TestRoleChangedRacesHandle(t *testing.T) {
	w, c := clockWorld(t, cmdRoom, cmdRoomB)
	const n = 6
	var mine []ws.Session
	for i := range n {
		s, _ := w.NewSession("", ws.Who{Account: 7, GM: i%2 == 0})
		mine = append(mine, s)
	}
	other, _ := w.NewSession("", ws.Who{Account: 8, GM: true})
	defer other.Close()

	lines := []string{"/help", "/roll 1", "/skip x", "keep", "ask", "/players", "/say hi", "/reset room", "/go b", "/audit", "timer"}
	var wg sync.WaitGroup
	for i, s := range append(mine, other) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 40 {
				s.Handle(ws.Message{Type: "input", Text: lines[(i+j)%len(lines)]})
				s.(ws.Pusher).Pending()
			}
		}()
	}
	ended := make(chan int, 4)
	for _, role := range []string{store.MemberPlayer, store.MemberGM, "", store.MemberPlayer} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ended <- w.RoleChanged(7, role)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 20 {
			c.Advance(time.Second)
		}
	}()
	wg.Add(1)
	go func() { // one of them leaves meanwhile
		defer wg.Done()
		mine[1].Close()
	}()
	wg.Wait()
	close(ended)
	total := 0
	for k := range ended {
		total += k
	}
	if total < n-1 || total > n {
		t.Errorf("RoleChanged ended %d sessions in all, want %d or %d", total, n-1, n)
	}
	if w.RoleChanged(7, "") != 0 {
		t.Error("a session of account 7 is still playing")
	}
	for i, s := range mine {
		if ms := s.Handle(ws.Message{Type: "input", Text: "/help"}); !isError(ms) || ms[0].Message != endedMessage {
			t.Errorf("session %d still answers: %+v", i, ms)
		}
		s.Close()
	}
	if w.Online() != 1 {
		t.Errorf("online = %d, want 1", w.Online())
	}
	if ms := other.Handle(ws.Message{Type: "input", Text: "/players"}); len(ms) == 0 || isError(ms) {
		t.Errorf("the other GM: %+v", ms)
	}
}

// FuzzCommandLine types any line as a player and as a game master. Whatever
// the line: no panic; a line with a slash never reaches on_input; a
// player's line never reaches other players, is never marked as a game
// master's, and never writes the audit log; what reaches the client is
// valid UTF-8.
func FuzzCommandLine(f *testing.F) {
	for _, s := range []string{"/help", "/HELP", "/say hi", "/reset room", "/reset room b", "/go b", "/go", "/skip x",
		"/roll 2d6", "/", "//", "/ ", "/audit", "/players", "/rooms", "/\u212Aelp", "/sKiP", "/reset\troom", "/say \xff\xfe",
		"/Bad Name", "/quiet", "/" + strings.Repeat("a", 21), "/a\u2028b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		name, args := splitCommand(line)
		if strings.Contains(name, " ") || args != strings.TrimSpace(args) || strings.ToLower(name) != name {
			t.Fatalf("splitCommand(%q) = %q, %q", line, name, args)
		}
		w, _ := clockWorld(t, cmdRoom, cmdRoomB)
		player, _ := w.NewSession("", ws.Who{})
		defer player.Close()
		gm, _ := w.NewSession("", ws.Who{GM: true})
		defer gm.Close()
		watcher, _ := w.NewSession("", ws.Who{})
		defer watcher.Close()
		pending(t, player)
		pending(t, watcher)

		got := input(player, line)
		slash := strings.HasPrefix(cleanInput(line), "/")
		for _, m := range got {
			if m.Type == "notice" && m.Role != NoticeServer {
				t.Errorf("player's %q got a %q notice: %+v", line, m.Role, m)
			}
			if slash && m.Type == "text" && strings.HasPrefix(m.Text, "input ") {
				t.Errorf("player's %q reached on_input: %q", line, m.Text)
			}
			if !utf8.ValidString(m.Text) {
				t.Errorf("player's %q: invalid UTF-8 %q", line, m.Text)
			}
		}
		if ms := pending(t, watcher); slash && len(ms) != 0 {
			t.Errorf("player's %q reached another player: %+v", line, ms)
		}
		w.mu.Lock()
		logged := len(w.auditLog)
		w.mu.Unlock()
		if logged != 0 {
			t.Errorf("player's %q was logged", line)
		}

		for _, m := range input(gm, line) {
			if !utf8.ValidString(m.Text) {
				t.Errorf("GM's %q: invalid UTF-8 %q", line, m.Text)
			}
			if slash && m.Type == "text" && strings.HasPrefix(m.Text, "input ") {
				t.Errorf("GM's %q reached on_input: %q", line, m.Text)
			}
		}
		for _, m := range pending(t, watcher) {
			if m.Type == "notice" && (m.Role != NoticeGM || m.From != "traveler 2") {
				t.Errorf("GM's %q: watcher got %+v", line, m)
			}
		}
		// Only the engine's commands and the room's GM command are logged.
		if name, _ := splitCommand(cleanInput(line)); slash && findCore(name) == nil && name != "skip" {
			w.mu.Lock()
			logged = len(w.auditLog)
			w.mu.Unlock()
			if logged != 0 {
				t.Errorf("GM's unknown %q was logged", line)
			}
		}
	})
}

// A room's GM command shares the broadcast budget with /say and /reset;
// the mark others see cuts long arguments on a letter's boundary, the log
// keeps them whole; connecting again with the same account does not
// refill the budget.
func TestRoomGMCommandSlowedAndMarkCut(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{GM: true, Account: 5})
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	long := strings.Repeat("a", maxMarkArgs-1) + "éé" // é across the cut
	if got := texts(input(gm, "/skip "+long)); !strings.HasPrefix(got, "command skip [") {
		t.Fatalf("/skip = %q", got)
	}
	if n := notices(pending(t, player)); len(n) != 1 || n[0] != "gm traveler: used /skip "+strings.Repeat("a", maxMarkArgs-1)+"…" {
		t.Errorf("mark = %q", n)
	}
	w.mu.Lock()
	detail := w.auditLog[len(w.auditLog)-1].Detail
	w.mu.Unlock()
	if detail != long {
		t.Errorf("logged %q", detail)
	}
	for range broadcastBurst - 2 {
		input(gm, "/say x")
	}
	gm.Close()
	again, _ := w.NewSession("", ws.Who{GM: true, Account: 5})
	defer again.Close()
	if n := notices(input(again, "/skip")); len(n) != 0 {
		// the fifth broadcast: still allowed
		t.Fatalf("fifth = %q", n)
	}
	if n := notices(input(again, "/skip")); len(n) != 1 || n[0] != "server: "+slowDownMessage {
		t.Errorf("sixth, after reconnecting = %q", n)
	}
}

// runCore knows every core command, and refuses a name it does not know
// instead of running one of them (it once ran /audit for any other name).
func TestRunCoreEveryCommand(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	s, _ := w.NewSession("", ws.Who{Account: 7, GM: true})
	defer s.Close()
	sess := s.(*session)
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range coreCommands {
		got := sess.runCore(c.name, "")
		if len(got) == 0 || reflect.DeepEqual(got, unknownCommand(c.name)) {
			t.Errorf("runCore(%q) = %+v", c.name, got)
		}
	}
	for _, name := range []string{"nosuch", "Audit", "", "audit2"} {
		if got, want := sess.runCore(name, ""), unknownCommand(name); !reflect.DeepEqual(got, want) {
			t.Errorf("runCore(%q) = %+v, want %+v", name, got, want)
		}
	}
}
