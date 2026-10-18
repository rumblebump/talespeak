package game

import (
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// partyRoom lists who is here, starts and stops room timers, and tells of
// players leaving through state.world.
const partyRoom = `
function on_input(p, text)
  if text == "who" then
    say(p.id .. " " .. p.name)
  elseif text == "list" then
    local ids = {}
    for _, q in ipairs(room.players()) do ids[#ids + 1] = q.id .. "=" .. q.name end
    say(table.concat(ids, ","))
  elseif text == "start" then
    say(tostring(timer.start("turn", 30, {room = true, show = "Turn"})))
  elseif text == "secret" then
    say(tostring(timer.start("turn", 30, {room = true})))
  elseif text == "again" then
    for i = 1, 5 do timer.start("turn", 10 * i, {room = true, show = "Turn " .. i}) end
  elseif text == "cancel" then
    say(tostring(timer.cancel("turn", {room = true})))
  elseif text == "left" then
    say(tostring(timer.left("turn", {room = true})) .. " " .. tostring(timer.left("turn")))
  elseif text == "many" then
    for i = 1, 11 do
      local ok, err = timer.start("t" .. i, 10, {room = true})
      if not ok then say(err) end
    end
  elseif text == "go" then
    room.go("b")
  end
end
function on_timer(p, name, kind)
  say("a: " .. name .. " " .. tostring(kind) .. " for " .. p.id)
  state.world.set("fired", name)
end
function on_change(p, keys)
  say("a sees " .. table.concat(keys, ",") .. ": " .. tostring(state.world.get(keys[1])))
end
function on_leave(p)
  local here = {}
  for _, q in ipairs(room.players()) do here[#here + 1] = q.id end
  say("nobody hears this")
  room.go("b")
  state.world.set("left", p.id .. " left " .. p.name .. ", here: " .. table.concat(here, ","))
end`

const partyRoomB = `
function on_input(p, text)
  if text == "back" then room.go("a") end
  if text == "list" then
    local ids = {}
    for _, q in ipairs(room.players()) do ids[#ids + 1] = q.id end
    say("b: " .. table.concat(ids, ","))
  end
end
function on_change(p, keys) end`

func input(s ws.Session, text string) []ws.Message {
	return s.Handle(ws.Message{Type: "input", Text: text})
}

func TestPlayerIDsAndNames(t *testing.T) {
	w, _ := clockWorld(t, partyRoom, partyRoomB)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	for s, want := range map[ws.Session]string{s1: "p1 traveler", s2: "p2 traveler 2", s3: "p3 traveler 3"} {
		if got := texts(input(s, "who")); got != want {
			t.Errorf("who = %q, want %q", got, want)
		}
	}
	s2.Close()
	s4, _ := w.NewSession("", ws.Who{})
	defer s4.Close()
	if got := texts(input(s4, "who")); got != "p4 traveler 2" {
		t.Errorf("a new player gets a new id and the free name: %q", got)
	}
}

func TestRoomPlayersInOrder(t *testing.T) {
	w, _ := clockWorld(t, partyRoom, partyRoomB)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	if got := texts(input(s2, "list")); got != "p1=traveler,p2=traveler 2,p3=traveler 3" {
		t.Fatalf("list = %q", got)
	}
	input(s1, "go")
	if got := texts(input(s3, "list")); got != "p2=traveler 2,p3=traveler 3" {
		t.Errorf("after p1 left the room: %q", got)
	}
	if got := texts(input(s1, "list")); got != "b: p1" {
		t.Errorf("in b: %q", got)
	}
	input(s1, "back")
	if got := texts(input(s3, "list")); got != "p2=traveler 2,p3=traveler 3,p1=traveler" {
		t.Errorf("p1 came back last: %q", got)
	}
}

func TestOnLeave(t *testing.T) {
	w, c := clockWorld(t, partyRoom, partyRoomB)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	s2.Close()
	c.Advance(DeliverGap)
	got := texts(pending(t, s1))
	if got != "a sees left: p2 left traveler 2, here: p1" {
		t.Fatalf("pushed to p1 = %q", got)
	}
	if roomOf(s2) != "a" {
		t.Errorf("room.go in on_leave moved the leaver to %q", roomOf(s2))
	}
	// A leaver whose room has no on_leave changes nothing.
	s3, _ := w.NewSession("", ws.Who{})
	input(s3, "go")
	s3.Close()
	c.Advance(time.Second)
	if got := pending(t, s1); len(got) != 0 {
		t.Errorf("leaving b: %+v", got)
	}
}

func TestOnLeaveCannotAsk(t *testing.T) {
	w, c := clockWorld(t, `
function on_input(p, text) end
function on_leave(p)
  state.world.set("before", true)
  ui.dialog("Stay?", {"Yes"})
  state.world.set("after", true)
end
function on_change(p, keys) say(table.concat(keys, ",")) end`, partyRoomB)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	s2.Close()
	c.Advance(DeliverGap)
	if got := texts(pending(t, s1)); got != "before" {
		t.Fatalf("pushed = %q, want only the write before the question", got)
	}
}

func TestRoomTimerShownToTheRoom(t *testing.T) {
	w, c := clockWorld(t, partyRoom, partyRoomB)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	input(s3, "go") // in b: sees nothing of a's timers

	got := input(s1, "start")
	if len(got) != 2 || got[0].Text != "true" || got[1].Type != "timer" || got[1].ID != "room:turn" || got[1].Title != "Turn" || got[1].Ms != 30000 {
		t.Fatalf("start = %+v", got)
	}
	if got := pending(t, s2); len(got) != 0 {
		t.Fatalf("pushed to p2 before the delivery = %+v", got)
	}
	c.Advance(DeliverGap)
	if got := pending(t, s2); len(got) != 1 || got[0].ID != "room:turn" || got[0].Ms != 30000 {
		t.Fatalf("pushed to p2 = %+v", got)
	}
	if got := pending(t, s3); len(got) != 0 {
		t.Fatalf("pushed to p3 in b = %+v", got)
	}
	if got := texts(input(s2, "left")); got != "29.75 nil" {
		t.Errorf("left = %q: the room's timer, and no player timer of that name", got)
	}

	// Coming into the room shows it with the time left; leaving removes it.
	c.Advance(10 * time.Second)
	got = input(s3, "back")
	if m := find(got, "timer"); m == nil || m.ID != "room:turn" || m.Ms != 19750 {
		t.Fatalf("entering a: %+v", got)
	}
	got = input(s2, "go")
	if m := find(got, "timer"); m == nil || m.ID != "room:turn" || m.Ms != 0 {
		t.Fatalf("leaving a: %+v", got)
	}

	// It runs out once, for the player who started it; everyone in the
	// room sees it go.
	c.Advance(20 * time.Second)
	if got := texts(pending(t, s1)); got != "timer | a: turn room for p1" {
		t.Fatalf("p1 at the deadline = %q", got)
	}
	if got := pending(t, s3); len(got) != 2 || got[0].ID != "room:turn" || got[0].Ms != 0 || got[1].Text != "a sees fired: turn" {
		t.Fatalf("p3 at the deadline = %+v", got)
	}
	if got := pending(t, s2); len(got) != 0 {
		t.Fatalf("p2 in b at the deadline = %+v", got)
	}
}

func find(ms []ws.Message, typ string) *ws.Message {
	for i := range ms {
		if ms[i].Type == typ {
			return &ms[i]
		}
	}
	return nil
}

func TestRoomTimerRunsForTheFirstHere(t *testing.T) {
	w, c := clockWorld(t, partyRoom, partyRoomB)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	input(s3, "secret")
	input(s3, "go")
	c.Advance(30 * time.Second)
	if got := texts(pending(t, s1)); got != "a: turn room for p1" {
		t.Fatalf("p1 = %q", got)
	}
	if got := pending(t, s3); len(got) != 0 {
		t.Fatalf("p3, the starter, moved away: %+v", got)
	}
}

func TestRoomTimerEmptyRoomDropsIt(t *testing.T) {
	w, c := clockWorld(t, partyRoom, partyRoomB)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	input(s1, "start")
	input(s1, "go")
	c.Advance(time.Minute)
	if got := pending(t, s1); len(got) != 0 {
		t.Fatalf("an empty room's timer ran: %+v", got)
	}
	if n := c.running(); n != 0 {
		t.Errorf("%d timers left", n)
	}
	input(s1, "back")
	if got := texts(input(s1, "left")); got != "nil nil" {
		t.Errorf("left = %q", got)
	}
}

func TestRoomTimerCancelLimitCoalesce(t *testing.T) {
	w, c := clockWorld(t, partyRoom, partyRoomB)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	input(s1, "again")
	input(s1, "again") // within one delivery: still one message
	c.Advance(DeliverGap)
	if got := pending(t, s2); len(got) != 1 || got[0].Title != "Turn 5" || got[0].Ms != 50000 {
		t.Fatalf("ten starts in two hooks = %+v, want one message", got)
	}
	if got := pending(t, s1); len(got) != 0 {
		t.Fatalf("the player who changed it last got it again: %+v", got)
	}
	if got := texts(input(s2, "cancel")); got != "true | timer" {
		t.Fatalf("cancel = %q", got)
	}
	c.Advance(DeliverGap)
	if got := pending(t, s1); len(got) != 1 || got[0].ID != "room:turn" || got[0].Ms != 0 {
		t.Fatalf("p1 after cancel = %+v", got)
	}
	if got := texts(input(s2, "cancel")); got != "false" {
		t.Errorf("cancel again = %q", got)
	}
	got := texts(input(s1, "many"))
	if !strings.Contains(got, "timer.start: more than 10 room timers running") {
		t.Errorf("many = %q", got)
	}
	c.Advance(time.Minute)
}

// A room timer started where state.world is read-only stays read-only.
func TestRoomTimerReadOnly(t *testing.T) {
	w, c := clockWorld(t, `
function on_input(p, text)
  if text == "set" then state.world.set("k", 1) end
end
function on_change(p, keys)
  timer.start("ro", 5, {room = true})
end
function on_timer(p, name, kind)
  local ok, err = state.world.set("x", 1)
  say(tostring(ok) .. " " .. tostring(err))
end`, partyRoomB)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	input(s1, "set")
	c.Advance(DeliverGap)
	c.Advance(5 * time.Second)
	if got := texts(pending(t, s2)); got != "nil state.world.set: on_change cannot change world state" {
		t.Fatalf("on_timer = %q", got)
	}
}
