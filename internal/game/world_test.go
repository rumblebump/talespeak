package game

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

const worldRoom = `
function on_input(p, text)
  if text == "open" then say(tostring(state.world.set("door", "open")))
  elseif text == "both" then state.world.set("door", "open") state.world.set("lamp", true)
  elseif text == "door" then say(tostring(state.world.get("door")))
  elseif text == "take" then
    local got = false
    local ok = state.world.update("crown", function(owner)
      if owner then return owner end
      got = true
      return p.name
    end)
    say(tostring(ok) .. " " .. tostring(got))
  elseif text == "count" then
    state.world.update("count", function(n) return (n or 0) + 1 end)
    say(state.world.get("count"))
  elseif text == "wait" then
    state.world.update("x", function() ui.dialog("Now?", {"Yes"}) return 1 end)
  elseif text == "nested" then
    state.world.update("x", function() state.world.update("y", function() return 1 end) return 1 end)
  elseif text == "fails" then
    local ok = pcall(state.world.update, "door", function() error("no") end)
    say(tostring(ok) .. " " .. tostring(state.world.get("door")))
  elseif text == "ask" then
    local n = ui.dialog("Stay?", {"Yes"})
    say("stay " .. n)
  end
end
function on_change(p, keys)
  say("changed: " .. table.concat(keys, ","))
  local ok, err = state.world.set("echo", 1)
  if not ok then say(err) end
  local ok2, err2 = state.world.update("echo", function() return 1 end)
  if not ok2 then say(err2) end
end`

func TestWorldStateShared(t *testing.T) {
	w, c := clockWorld(t, worldRoom, timerRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	if got := texts(a.Handle(ws.Message{Type: "input", Text: "open"})); got != "true" {
		t.Fatalf("set = %s", got)
	}
	if got := texts(b.Handle(ws.Message{Type: "input", Text: "door"})); got != "open" {
		t.Errorf("b reads %s", got)
	}
	c.Advance(0)
	want := "changed: door | state.world.set: on_change cannot change world state | state.world.update: on_change cannot change world state"
	if got := texts(pending(t, b)); got != want {
		t.Errorf("b's on_change: %s", got)
	}
	if got := pending(t, a); len(got) != 0 {
		t.Errorf("the writer got its own change: %+v", got)
	}
	if got := texts(a.Handle(ws.Message{Type: "input", Text: "door"})); got != "open" {
		t.Errorf("a reads %s", got)
	}
}

// Changes made before a delivery reach on_change once, together.
func TestWorldChangesCoalesce(t *testing.T) {
	w, c := clockWorld(t, worldRoom, timerRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	a.Handle(ws.Message{Type: "input", Text: "open"})
	a.Handle(ws.Message{Type: "input", Text: "both"})
	c.Advance(0)
	if got := texts(pending(t, b)); !strings.HasPrefix(got, "changed: door,lamp |") {
		t.Errorf("b's on_change: %s", got)
	}
	c.Advance(time.Minute)
	if got := pending(t, b); len(got) != 0 {
		t.Errorf("a second delivery: %+v", got)
	}
}

// Only one of many players gets a once item; a count counts every taker.
func TestWorldUpdate(t *testing.T) {
	w, _ := clockWorld(t, worldRoom, timerRoomB)
	var got []string
	for range 10 {
		s, _ := w.NewSession("", ws.Who{})
		defer s.Close()
		got = append(got, texts(s.Handle(ws.Message{Type: "input", Text: "take"})))
		s.Handle(ws.Message{Type: "input", Text: "count"})
	}
	if got[0] != "true true" || strings.Count(strings.Join(got, ";"), "true true") != 1 {
		t.Errorf("takers: %q", got)
	}
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "count"})); got != "11" {
		t.Errorf("count = %s", got)
	}
}

func TestWorldUpdateRefuses(t *testing.T) {
	w, _ := clockWorld(t, worldRoom, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	for _, text := range []string{"wait", "nested"} {
		if got := s.Handle(ws.Message{Type: "input", Text: text}); !isError(got) {
			t.Errorf("%s: %+v", text, got)
		}
	}
	s.Handle(ws.Message{Type: "input", Text: "open"})
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "fails"})); got != "false open" {
		t.Errorf("an error in fn: %s", got)
	}
	// The failed waits left nothing behind: questions work.
	if got := kinds(s.Handle(ws.Message{Type: "input", Text: "ask"})); got != "dialog:d1" {
		t.Errorf("after: %s", got)
	}
}

// on_change while the player has a question open runs, cannot ask, and
// leaves the question open.
func TestWorldChangeWhileAsking(t *testing.T) {
	w, c := clockWorld(t, worldRoom, timerRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	b.Handle(ws.Message{Type: "input", Text: "ask"})
	a.Handle(ws.Message{Type: "input", Text: "open"})
	c.Advance(0)
	if got := texts(pending(t, b)); !strings.HasPrefix(got, "changed: door") {
		t.Errorf("b's on_change: %s", got)
	}
	if got := texts(b.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "stay 1" {
		t.Errorf("answer: %s", got)
	}
}

// A player whose address has no Lua time left gets on_change later, with
// every key that changed meanwhile.
func TestWorldChangeWaitsForBudget(t *testing.T) {
	w, c := clockWorld(t, worldRoom, timerRoomB, WithClientBudget(time.Second, time.Second))
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("192.0.2.2", ws.Who{})
	defer b.Close()
	w.mu.Lock()
	w.budgets["192.0.2.2"].left = -0.25
	w.mu.Unlock()
	a.Handle(ws.Message{Type: "input", Text: "open"})
	c.Advance(0)
	if got := pending(t, b); len(got) != 0 {
		t.Fatalf("ran without budget: %+v", got)
	}
	a.Handle(ws.Message{Type: "input", Text: "both"})
	c.Advance(retryBusy)
	if got := texts(pending(t, b)); !strings.HasPrefix(got, "changed: door,lamp |") {
		t.Errorf("after the retry: %s", got)
	}
}

// A room without on_change, and a player who left, get nothing.
func TestWorldChangeWithoutHook(t *testing.T) {
	w, c := clockWorld(t, worldRoom, timerRoomB)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	gone, _ := w.NewSession("", ws.Who{})
	gone.Close()
	w.mu.Lock()
	b.(*session).room = "b" // room b has no on_change
	w.mu.Unlock()
	a.Handle(ws.Message{Type: "input", Text: "open"})
	c.Advance(0)
	if got := pending(t, b); len(got) != 0 {
		t.Errorf("room without on_change: %+v", got)
	}
	if got := pending(t, gone); len(got) != 0 {
		t.Errorf("a closed session: %+v", got)
	}
}

func TestWorldStateLimits(t *testing.T) {
	w, _ := clockWorld(t, `
function on_input(p, text)
  local n = tonumber(text)
  for i = 1, 50 do
    local ok, err = state.world.set("k" .. (n * 50 + i), string.rep("x", 16000))
    if not ok then say(err) return end
  end
end`, timerRoomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	for i := range 6 {
		got := texts(s.Handle(ws.Message{Type: "input", Text: strconv.Itoa(i)}))
		if i < 5 && got != "" {
			t.Fatalf("hook %d: %s", i, got)
		}
		if i == 5 && got != "state.world.set: world state larger than 4194304 bytes" {
			t.Errorf("past 4 MiB: %q", got)
		}
	}
}
