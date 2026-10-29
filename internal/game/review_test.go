package game

import (
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// Fixes from the T-0024/T-0029/T-0030 security review.

const bounceRoom = `
function on_input(p, text)
  if text == "ring" then state.world.set("bell", true) end
  if text == "ask" then ui.dialog("Stay?", {"Yes"}) end
end
function on_change(p, keys)
  say("heard " .. table.concat(keys, ","))
  if p.room == "a" then room.go("b") else timer.start("echo", 1) end
end
function on_timer(p, name)
  local ok, err = state.world.set("bell", false)
  say("timer: " .. tostring(err))
end`

const bounceRoomB = `
function on_enter(p)
  local ok, err = state.world.set("bell", false)
  say("enter: " .. tostring(err))
end`

// What on_change starts (room.go, timers) cannot write world state either.
func TestChangeCannotBounce(t *testing.T) {
	w, c := clockWorld(t, bounceRoom, bounceRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	a.Handle(ws.Message{Type: "input", Text: "ring"})
	c.Advance(0)
	if got := texts(pending(t, b)); got != "heard bell | items:rope | enter: state.world.set: on_change cannot change world state" {
		t.Fatalf("b moved by on_change: %s", got)
	}
	if got := pending(t, a); len(got) != 0 {
		t.Fatalf("a got a change back: %+v", got)
	}
}

func TestTimerFromChangeIsReadOnly(t *testing.T) {
	w, c := clockWorld(t, `
-- ring: a word the test types that the room shares (typed.go)
function on_input(p, text) state.world.set("bell", text) end
function on_change(p, keys) timer.start("echo", 1) end
function on_timer(p, name)
  local ok, err = state.world.set("bell", "again")
  say("timer: " .. tostring(err))
end`, bounceRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	a.Handle(ws.Message{Type: "input", Text: "ring"})
	c.Advance(0)
	c.Advance(time.Second)
	if got := texts(pending(t, b)); got != "timer: state.world.set: on_change cannot change world state" {
		t.Errorf("on_timer of a timer on_change started: %s", got)
	}
}

// Writing the value a key holds already tells no one.
func TestSameValueIsNoChange(t *testing.T) {
	w, c := clockWorld(t, worldRoom, timerRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	a.Handle(ws.Message{Type: "input", Text: "open"})
	c.Advance(0)
	pending(t, b)
	a.Handle(ws.Message{Type: "input", Text: "open"})
	c.Advance(time.Second)
	if got := pending(t, b); len(got) != 0 {
		t.Errorf("an unchanged value announced: %+v", got)
	}
}

// Deliveries come at most every DeliverGap; writes meanwhile merge.
func TestDeliveriesAreSpaced(t *testing.T) {
	w, c := clockWorld(t, worldRoom, timerRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	a.Handle(ws.Message{Type: "input", Text: "open"})
	c.Advance(0)
	pending(t, b)
	a.Handle(ws.Message{Type: "input", Text: "count"})
	a.Handle(ws.Message{Type: "input", Text: "take"})
	c.Advance(DeliverGap - time.Millisecond)
	if got := pending(t, b); len(got) != 0 {
		t.Fatalf("delivered before the gap: %+v", got)
	}
	c.Advance(time.Millisecond)
	if got := texts(pending(t, b)); !strings.HasPrefix(got, "changed: count,crown |") {
		t.Errorf("after the gap: %s", got)
	}
}

// A reply carries the pushed messages that wait, first.
func TestReplyAfterPushed(t *testing.T) {
	w, c := clockWorld(t, timerRoom, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	s.Handle(ws.Message{Type: "input", Text: "ask"})
	c.Advance(30 * time.Second) // the timeout queues close d1 and "answer nil"
	got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})
	if texts(got) != "close | answer nil | error" {
		t.Errorf("reply: %s", texts(got))
	}
	if got := pending(t, s); len(got) != 0 {
		t.Errorf("left in the queue: %+v", got)
	}
}

// The queue also counts bytes.
func TestPushedQueueBytes(t *testing.T) {
	w, c := clockWorld(t, `
function on_input(p, text) timer.start("big", 1) end
function on_timer(p, name) local x = string.rep("x", 4000) for i = 1, 40 do say(x) end timer.start("big", 1) end`, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	s.Handle(ws.Message{Type: "input", Text: "go"})
	c.Advance(time.Second)
	if ms := pending(t, s); len(ms) != 40 {
		t.Fatalf("one hook's 160 KB: %d messages", len(ms))
	}
	c.Advance(2 * time.Second) // two more, unread: 320 KB
	if ms, ok := s.(ws.Pusher).Pending(); ok {
		t.Fatalf("320 KB queued did not overflow: %d messages", len(ms))
	}
}
