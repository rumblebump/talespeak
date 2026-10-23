package game

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of T-0031: player names, room.players across undone moves,
// on_leave (world closed, question open, no Lua time, room timers from
// it), and room timers (new players, per room, replace, failing hooks,
// Lua time, questions, close, concurrency).

// edgeParty runs in both rooms a and b: commands are "<cmd> <arg>".
// on_leave records the leaver in a, and starts a shown room timer in b.
const edgeParty = `
function on_input(p, text)
  local cmd, arg = text, ""
  local sp = string.find(text, " ", 1, true)
  if sp then cmd, arg = string.sub(text, 1, sp - 1), string.sub(text, sp + 1) end
  if cmd == "list" then
    local ids = {}
    for _, q in ipairs(room.players()) do ids[#ids + 1] = q.id end
    say(p.room .. ": " .. table.concat(ids, ","))
  elseif cmd == "who" then say(p.id .. " " .. p.name)
  elseif cmd == "start" then say(tostring(timer.start(arg, 30, {room = true, show = "T " .. arg})))
  elseif cmd == "secret" then say(tostring(timer.start(arg, 30, {room = true})))
  elseif cmd == "cancel" then say(tostring(timer.cancel(arg, {room = true})))
  elseif cmd == "left" then say(tostring(timer.left(arg, {room = true})))
  elseif cmd == "fill" then
    for i = 1, 10 do timer.start("t" .. i, 30, {room = true}) end
    local ok, why = timer.start("t11", 30, {room = true})
    say(tostring(ok) .. " " .. tostring(why))
  elseif cmd == "fail" then
    timer.start(arg, 30, {room = true, show = "Doomed"})
    error("broken")
  elseif cmd == "ask" then
    local n = ui.dialog("Stay?", {"Yes", "No"})
    state.world.set("answered", p.id)
    say("answer " .. tostring(n))
  elseif cmd == "go" then room.go(arg)
  end
end
function on_timer(p, name, kind)
  say(p.room .. ": " .. name .. " " .. tostring(kind) .. " for " .. p.id)
end
function on_change(p, keys) say(p.room .. " sees " .. table.concat(keys, ",")) end
function on_leave(p)
  if p.room == "a" then
    state.world.set("left_" .. p.id, #room.players())
  else
    timer.start("after", 10, {room = true, show = "After " .. p.id})
  end
end`

func edgeWorld(t *testing.T, opts ...Option) (*World, *fakeClock) {
	t.Helper()
	return clockWorld(t, edgeParty, edgeParty, opts...)
}

// Names are the lowest free; ids are never given again.
func TestPlayerNamesLowestFree(t *testing.T) {
	w, _ := edgeWorld(t)
	var ss []ws.Session
	for range 4 {
		s, _ := w.NewSession("", ws.Who{})
		ss = append(ss, s)
	}
	ss[0].Close()
	ss[2].Close()
	for _, want := range []string{"p5 traveler", "p6 traveler 3", "p7 traveler 5"} {
		s, _ := w.NewSession("", ws.Who{})
		ss = append(ss, s)
		if got := texts(input(s, "who")); got != want {
			t.Errorf("who = %q, want %q", got, want)
		}
	}
	for _, s := range ss {
		s.Close()
	}
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := texts(input(s, "who")); got != "p8 traveler" {
		t.Errorf("after everyone left: %q", got)
	}
}

// A world that closed still hands out sessions, which do nothing.
func TestPlayerNameAfterWorldClosed(t *testing.T) {
	w, _ := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	w.Close()
	s2, _ := w.NewSession("", ws.Who{})
	s2.Close()
	if sess := s2.(*session); sess.id != "p2" || sess.name != "traveler 2" {
		t.Errorf("session of a closed world: %q %q", sess.id, sess.name)
	}
}

// A player coming into the world into a room with a shown room timer sees
// it with the time left in the first messages; a secret one not at all.
func TestRoomTimerShownToNewPlayer(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	input(s1, "start turn")
	input(s1, "secret hidden")
	c.Advance(12 * time.Second)
	s2, first := w.NewSession("", ws.Who{})
	defer s2.Close()
	var timers []ws.Message
	for _, m := range first {
		if m.Type == "timer" {
			timers = append(timers, m)
		}
	}
	if len(timers) != 1 || timers[0].ID != "room:turn" || timers[0].Title != "T turn" || timers[0].Ms != 18000 {
		t.Fatalf("first messages of p2: %+v", first)
	}
	if got := texts(input(s2, "left hidden")); got != "18" {
		t.Errorf("the secret one runs for p2 too: %q", got)
	}
}

// Room timers are per room: the same name in two rooms are two timers, and
// a full room does not stop another room's timers.
func TestRoomTimersPerRoom(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	input(s2, "go b")
	if got := texts(input(s1, "fill")); got != "nil timer.start: more than 10 room timers running" {
		t.Fatalf("fill a = %q", got)
	}
	if got := texts(input(s2, "secret t1")); got != "true" {
		t.Fatalf("t1 in b, with a full = %q", got)
	}
	if got := texts(input(s2, "fill")); got != "nil timer.start: more than 10 room timers running" {
		t.Fatalf("fill b = %q", got)
	}
	// Replacing a running one is not refused in a full room.
	if got := texts(input(s1, "secret t3")); got != "true" {
		t.Errorf("replacing in a full room = %q", got)
	}
	if got := texts(input(s2, "cancel t1")); got != "true" {
		t.Errorf("cancel t1 in b = %q", got)
	}
	if got := texts(input(s1, "left t1")); got != "30" {
		t.Errorf("a's t1 after b's was cancelled = %q", got)
	}
	c.Advance(30 * time.Second)
	a, b := texts(pending(t, s1)), texts(pending(t, s2))
	if strings.Count(a, "room for p1") != 10 || strings.Count(b, "room for p2") != 9 {
		t.Errorf("fired in a: %q; in b: %q", a, b)
	}
	if n := c.running(); n != 0 {
		t.Errorf("%d timers left", n)
	}
	w.mu.Lock()
	left := len(w.roomTimers)
	w.mu.Unlock()
	if left != 0 {
		t.Errorf("rooms with timers after they all ran: %d", left)
	}
}

// What the others see when a room timer is replaced, once delivered: shown
// by secret removes it, secret by secret and a secret cancel say nothing.
func TestRoomTimerReplaceShownAndSecret(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	tests := []struct {
		cmd  string
		want string // what p2 is pushed: "" nothing, "show" the countdown, "gone" its removal
	}{
		{"secret x", ""},
		{"secret x", ""},
		{"start x", "show"},
		{"start x", "show"},
		{"secret x", "gone"},
		{"cancel x", ""},
		{"start x", "show"},
		{"cancel x", "gone"},
		{"cancel x", ""},
	}
	for i, tt := range tests {
		input(s1, tt.cmd)
		c.Advance(DeliverGap)
		got := pending(t, s2)
		switch tt.want {
		case "":
			if len(got) != 0 {
				t.Errorf("%d %s: p2 got %+v", i, tt.cmd, got)
			}
		case "show":
			if len(got) != 1 || got[0].ID != "room:x" || got[0].Title != "T x" || got[0].Ms <= 29000 || got[0].Ms > 30000 {
				t.Errorf("%d %s: p2 got %+v", i, tt.cmd, got)
			}
		case "gone":
			if len(got) != 1 || got[0].ID != "room:x" || got[0].Title != "" || got[0].Ms != 0 {
				t.Errorf("%d %s: p2 got %+v", i, tt.cmd, got)
			}
		}
		if got := pending(t, s1); len(got) != 0 {
			t.Errorf("%d %s: the changer got it pushed again: %+v", i, tt.cmd, got)
		}
	}
}

// Changes to room timers reach the others at most once per DeliverGap,
// with the latest state; whoever changed a timer last is not sent it
// again, and an earlier changer gets the latest.
func TestRoomTimerDeliveriesSpaced(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	input(s1, "start y")
	input(s1, "start x")
	c.Advance(0) // the first delivery has no gap before it; timers by name
	if got := pending(t, s2); len(got) != 2 || got[0].Title != "T x" || got[1].Title != "T y" {
		t.Fatalf("first delivery = %+v", got)
	}
	pending(t, s3)
	input(s1, "cancel x")
	input(s1, "start x")
	input(s2, "secret x") // p2 changes it last: shown -> secret, gone for everyone
	if got := pending(t, s3); len(got) != 0 {
		t.Fatalf("before the gap: %+v", got)
	}
	c.Advance(DeliverGap - time.Millisecond)
	if got := pending(t, s3); len(got) != 0 {
		t.Fatalf("1 ms before the gap: %+v", got)
	}
	c.Advance(time.Millisecond)
	for name, s := range map[string]ws.Session{"p1": s1, "p3": s3} {
		if got := pending(t, s); len(got) != 1 || got[0].ID != "room:x" || got[0].Ms != 0 {
			t.Errorf("%s after the gap = %+v, want one removal", name, got)
		}
	}
	if got := pending(t, s2); len(got) != 0 {
		t.Errorf("p2, who changed it last = %+v", got)
	}
	if n := c.running(); n != 2 { // the secret x and y
		t.Errorf("%d timers running, want x and y", n)
	}
}

// A room timer started before the hook fails keeps running, and the
// others hear of it: the failed hook's output still flushes.
func TestRoomTimerFromFailingHook(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	got := input(s1, "fail doom")
	if len(got) == 0 || got[0].Type != "error" {
		t.Fatalf("fail = %+v", got)
	}
	if m := find(got, "timer"); m == nil || m.ID != "room:doom" || m.Title != "Doomed" {
		t.Errorf("p1 does not see the timer of the failed hook: %+v", got)
	}
	c.Advance(DeliverGap)
	if got := pending(t, s2); len(got) != 1 || got[0].ID != "room:doom" {
		t.Fatalf("p2 = %+v", got)
	}
	c.Advance(30 * time.Second)
	if got := texts(pending(t, s1)); got != "timer | a: doom room for p1" {
		t.Errorf("at the deadline: %q", got)
	}
}

// The starter left the world: the room timer runs for the player there
// longest. The leaver's on_leave sees only those still here.
func TestRoomTimerStarterLeftWorld(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	input(s1, "secret turn")
	s1.Close()
	c.Advance(30 * time.Second)
	got := texts(pending(t, s2))
	if !strings.Contains(got, "a sees left_p1") || !strings.HasSuffix(got, "a: turn room for p2") {
		t.Fatalf("p2 = %q", got)
	}
	if got := texts(pending(t, s3)); got != "a sees left_p1" {
		t.Errorf("p3 = %q", got)
	}
	if got := texts(input(s3, "list")); got != "a: p2,p3" {
		t.Errorf("list = %q", got)
	}
}

// A room timer due while its starter's address has no Lua time runs for
// the next player in the room; the shown countdown goes once for everyone.
func TestRoomTimerBusyStarterNextPlayer(t *testing.T) {
	w, c := edgeWorld(t, WithClientBudget(time.Second, time.Second))
	s1, _ := w.NewSession("192.0.2.1", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	input(s1, "start turn")
	c.Advance(DeliverGap)
	pending(t, s2)
	w.mu.Lock()
	w.budgets["192.0.2.1"].left = -60
	w.mu.Unlock()
	c.Advance(30 * time.Second)
	if got := pending(t, s1); len(got) != 1 || got[0].ID != "room:turn" || got[0].Ms != 0 {
		t.Errorf("busy starter = %+v, want only the removal", got)
	}
	if got := texts(pending(t, s2)); got != "timer | a: turn room for p2" {
		t.Errorf("p2 = %q", got)
	}
	if n := c.running(); n != 0 {
		t.Errorf("%d timers running", n)
	}
}

// When nobody in the room has Lua time, the room timer waits and tries
// again, starter first; while it waits it is secret (a newcomer does not
// see it) but still running, and nobody sees it go twice.
func TestRoomTimerAllBusyRetries(t *testing.T) {
	w, c := edgeWorld(t, WithClientBudget(time.Second, time.Second))
	s1, _ := w.NewSession("192.0.2.1", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("192.0.2.2", ws.Who{})
	defer s2.Close()
	input(s2, "start turn") // p2 starts it, so p2 runs it once there is time
	c.Advance(DeliverGap)
	pending(t, s1)
	w.mu.Lock()
	w.budgets["192.0.2.1"].left = -30 // at 250 ms: free again 250 ms after the deadline
	w.budgets["192.0.2.2"].left = -30
	w.mu.Unlock()
	c.Advance(30*time.Second - DeliverGap)
	for name, s := range map[string]ws.Session{"p1": s1, "p2": s2} {
		if got := pending(t, s); len(got) != 1 || got[0].ID != "room:turn" || got[0].Ms != 0 {
			t.Errorf("%s at the deadline = %+v, want only the removal", name, got)
		}
	}
	s3, first := w.NewSession("", ws.Who{}) // a newcomer would run it, but comes too late to be first
	defer s3.Close()
	if m := find(first, "timer"); m != nil {
		t.Errorf("a newcomer sees the waiting timer: %+v", m)
	}
	if got := texts(input(s3, "left turn")); got != "0" {
		t.Errorf("waiting, left = %q", got)
	}
	// The retry is starter first: p2 again, who has Lua time by then.
	c.Advance(retryBusy)
	if got := texts(pending(t, s2)); got != "a: turn room for p2" {
		t.Errorf("p2 after the retry = %q", got)
	}
	for name, s := range map[string]ws.Session{"p1": s1, "p3": s3} {
		if got := pending(t, s); len(got) != 0 {
			t.Errorf("%s after the retry = %+v", name, got)
		}
	}
	if got := texts(input(s3, "left turn")); got != "nil" {
		t.Errorf("after it ran, left = %q", got)
	}
}

// A room timer that runs out while its player has a question open runs
// on_timer, which cannot ask; the question stays open.
func TestRoomTimerWhileQuestionOpen(t *testing.T) {
	w, c := clockWorld(t, `
function on_input(p, text)
  if text == "start" then timer.start("turn", 5, {room = true, show = "Turn"})
  elseif text == "ask" then say("answer " .. tostring(ui.dialog("Stay?", {"Yes", "No"})))
  end
end
function on_timer(p, name, kind)
  local ok = pcall(ui.dialog, "Now?", {"Yes"})
  say(name .. " " .. kind .. " asked " .. tostring(ok))
end`, partyRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	input(s, "start")
	q := dialogID(t, input(s, "ask"))
	c.Advance(5 * time.Second)
	if got := texts(pending(t, s)); got != "timer | turn room asked false" {
		t.Fatalf("at the deadline: %q", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "choose", ID: q, Option: 2})); got != "answer 2" {
		t.Errorf("answer = %q", got)
	}
}

// Closing the world stops the room timers; a delivery still due does
// nothing once it closed.
func TestRoomTimersStopOnWorldClose(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	input(s1, "start x")
	input(s1, "secret y")
	w.Close()
	if n := c.running(); n != 1 {
		t.Errorf("%d timers running after the world closed, want the due delivery only", n)
	}
	c.Advance(time.Minute)
	if n := c.running(); n != 0 {
		t.Errorf("%d timers running", n)
	}
	for _, s := range []ws.Session{s1, s2} {
		if got := pending(t, s); len(got) != 0 {
			t.Errorf("after close: %+v", got)
		}
	}
}

// A player who leaves drops what was queued for them.
func TestCloseDropsQueued(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	input(s1, "start x")
	c.Advance(DeliverGap)
	s2.Close()
	if got := pending(t, s2); len(got) != 0 {
		t.Errorf("queued after close: %+v", got)
	}
	c.Advance(time.Minute) // the timer runs for p1 alone
	if got := texts(pending(t, s1)); got != "a sees left_p2 | timer | a: x room for p1" {
		t.Errorf("p1 = %q", got)
	}
}

// on_leave does not run when the world closed before the player left.
func TestOnLeaveNotWhenWorldClosed(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil))
	p, err := pack.Load(roomsFS(`function on_leave(p) error("on_leave ran for " .. p.id) end`, partyRoomB))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, log)
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := w.NewSession("", ws.Who{})
	s2, _ := w.NewSession("", ws.Who{})
	s1.Close()
	w.Close()
	s2.Close()
	mu.Lock()
	got := buf.String()
	mu.Unlock()
	if !strings.Contains(got, "on_leave ran for p1") || !strings.Contains(got, "hook=on_leave") {
		t.Errorf("the open world's on_leave error is not logged: %q", got)
	}
	if strings.Contains(got, "p2") {
		t.Errorf("on_leave ran after the world closed: %q", got)
	}
}

// A player who leaves with a question open: the waiting hook ends without
// going on, then on_leave runs.
func TestOnLeaveWithQuestionOpen(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	dialogID(t, input(s2, "ask"))
	s2.Close()
	c.Advance(DeliverGap)
	if got := texts(pending(t, s1)); got != "a sees left_p2" {
		t.Errorf("p1 = %q, want on_leave's write only", got)
	}
	w.mu.Lock()
	paused := w.rt.Paused()
	w.mu.Unlock()
	if paused != 0 {
		t.Errorf("%d hooks paused", paused)
	}
}

// on_leave runs even when the leaver's address used up its Lua time: its
// time is charged, so the budget goes further below zero. The name is
// free again and the leaver is not listed.
func TestOnLeaveWithoutLuaTime(t *testing.T) {
	clk := &stepClock{t: time.Unix(1e9, 0)}
	fake := newClock()
	w, _ := edgeWorld(t, WithClientBudget(time.Second, time.Second), WithClock(clk.now, fake.After))
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("192.0.2.9", ws.Who{})
	if got := texts(input(s2, "who")); got != "p2 traveler 2" {
		t.Fatalf("who = %q", got)
	}
	w.mu.Lock()
	b := w.budgets["192.0.2.9"]
	b.left, b.last = -5, clk.t
	w.mu.Unlock()
	if got := input(s2, "who"); !isError(got) || got[0].Message != busyMessage {
		t.Fatalf("busy player = %+v", got)
	}
	w.mu.Lock()
	b.left, b.last = -5, clk.t
	clk.step = 100 * time.Millisecond // every reading of the clock: 100 ms of Lua time
	w.mu.Unlock()
	s2.Close()
	w.mu.Lock()
	clk.step = 0
	left := b.left
	w.mu.Unlock()
	if left >= -5 {
		t.Errorf("budget after on_leave = %v, want charged below -5", left)
	}
	fake.Advance(DeliverGap)
	if got := texts(pending(t, s1)); got != "a sees left_p2" {
		t.Errorf("p1 = %q, want on_leave's write", got)
	}
	if got := texts(input(s1, "list")); got != "a: p1" {
		t.Errorf("list = %q", got)
	}
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	if got := texts(input(s3, "who")); got != "p3 traveler 2" {
		t.Errorf("who = %q", got)
	}
}

// on_leave can start a room timer: the others see it, and it runs for the
// player there longest. Started by the last to leave, it is dropped.
func TestOnLeaveStartsRoomTimer(t *testing.T) {
	w, c := edgeWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	input(s1, "go b")
	input(s2, "go b")
	pending(t, s1)
	s2.Close()
	c.Advance(DeliverGap)
	got := pending(t, s1)
	if len(got) != 1 || got[0].ID != "room:after" || got[0].Title != "After p2" || got[0].Ms != 10000 {
		t.Fatalf("p1 = %+v", got)
	}
	c.Advance(10 * time.Second)
	if got := texts(pending(t, s1)); got != "timer | b: after room for p1" {
		t.Errorf("at the deadline: %q", got)
	}

	s1.Close()
	w.mu.Lock()
	n := len(w.roomTimers["b"])
	w.mu.Unlock()
	if n != 1 {
		t.Fatalf("the last leaver's room timer: %d timers in b", n)
	}
	c.Advance(10 * time.Second)
	if n := c.running(); n != 0 {
		t.Errorf("%d timers left in an empty world", n)
	}
	if n := runningRoomTimers(w); n != 0 {
		t.Errorf("room timers still running: %d, want the one left waiting for a player", n)
	}
}

// A move undone because the new room's on_enter has no Lua time keeps the
// player's place in room.players and shows nothing of the new room.
func TestRoomPlayersAfterUndoneMove(t *testing.T) {
	clk := &stepClock{t: time.Unix(1e9, 0)}
	fake := newClock()
	w, _ := edgeWorld(t, WithClock(clk.now, fake.After))
	s1, _ := w.NewSession("192.0.2.1", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	input(s3, "go b")
	input(s3, "start bt") // a shown timer in b
	clk.step = 3 * time.Second
	got := input(s1, "go b")
	clk.step, clk.t = 0, clk.t.Add(time.Hour)
	if roomOf(s1) != "a" || !isError(got) || find(got, "timer") != nil {
		t.Fatalf("busy move: room %q, %+v", roomOf(s1), got)
	}
	if got := texts(input(s2, "list")); got != "a: p1,p2" {
		t.Errorf("list in a = %q", got)
	}
	if got := texts(input(s3, "list")); got != "b: p3" {
		t.Errorf("list in b = %q", got)
	}
}

// Players join, leave, move and start room timers while the clock runs:
// no race, no deadlock, and everything stops once all have left.
func TestConcurrentPartyOps(t *testing.T) {
	w, c := edgeWorld(t)
	var wg sync.WaitGroup
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
				c.Advance(5 * time.Second)
			}
		}
	}()
	cmds := []string{"start x", "secret y", "cancel x", "left x", "list", "go b", "go a", "start x", "fill", "ask"}
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := range 5 {
				s, _ := w.NewSession("", ws.Who{})
				for i := range 12 {
					s.Handle(ws.Message{Type: "input", Text: cmds[(g+round+i)%len(cmds)]})
					s.(ws.Pusher).Pending()
				}
				s.Close()
			}
		}()
	}
	wg.Wait()
	close(stop)
	<-stopped
	c.Advance(time.Hour)
	c.Advance(time.Hour)
	n := runningRoomTimers(w)
	w.mu.Lock()
	sessions, paused := len(w.sessions), w.rt.Paused()
	w.mu.Unlock()
	if n != 0 || sessions != 0 || paused != 0 {
		t.Errorf("after all left: %d rooms with timers, %d sessions, %d paused", n, sessions, paused)
	}
	if n := c.running(); n != 0 {
		t.Errorf("%d timers running", n)
	}
}

// dialogID is the id of the ui.dialog question in ms.
func dialogID(t *testing.T, ms []ws.Message) string {
	t.Helper()
	if m := find(ms, "dialog"); m != nil {
		return m.ID
	}
	t.Fatalf("no dialog in %+v", ms)
	return ""
}
