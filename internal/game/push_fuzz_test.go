package game

import (
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// fuzzRoomA and fuzzRoomB use every primitive of T-0029 and T-0030: shown
// and secret timers, questions with and without timeouts, world writes and
// updates, moves, and on_timer and on_change that move or try to ask.
const fuzzRoomA = `
function on_enter(p) say("in a") end
function on_input(p, text)
  if text == "start" then timer.start("guards", 60, {show = "Guards"})
  elseif text == "secret" then timer.start("s", 2)
  elseif text == "short" then timer.start("guards", 1, {show = "Hurry"})
  elseif text == "cancel" then timer.cancel("guards")
  elseif text == "left" then say(tostring(timer.left("guards")))
  elseif text == "ask" then
    local n = ui.dialog("Fight?", {"Fight", "Flee"}, {timeout = 5})
    say("answer " .. tostring(n))
    if n == 2 then room.go("b") end
  elseif text == "type" then say(tostring(ui.ask("Name?", {timeout = 3})))
  elseif text == "pick" then
    local ns = ui.select("Pick", {"x", "y"}, {min = 0, timeout = 2})
    say(ns and #ns or "none")
  elseif text == "forever" then ui.dialog("Wait?", {"Yes"})
  elseif text == "open" then state.world.set("door", "open")
  elseif text == "shut" then state.world.set("door", nil)
  elseif text == "take" then state.world.update("crown", function(o) return o or p.name end)
  elseif text == "count" then state.world.update("n", function(n) return (n or 0) + 1 end)
  elseif text == "go" then room.go("b")
  elseif text == "many" then for i = 1, 11 do timer.start("t" .. i, i) end
  end
end
function on_click(p, id) timer.start("click", 1) state.world.set("clicked", p.name) end
function on_timer(p, name)
  say("a timer " .. name)
  if name == "s" then state.world.update("n", function(n) return (n or 0) + 1 end) end
  if name == "t3" then room.go("b") end
  if name == "click" then ui.dialog("Again?", {"Yes"}, {timeout = 1}) end
end
function on_change(p, keys) say("a changed " .. #keys) end`

const fuzzRoomB = `
function on_enter(p) say("in b") if state.world.get("door") then timer.start("back", 1) end end
function on_input(p, text) room.go("a") end
function on_timer(p, name) say("b timer " .. name) room.go("a") end
function on_change(p, keys)
  say("b changed " .. table.concat(keys, ","))
  pcall(ui.dialog, "Seen?", {"Yes"})
  if #keys > 1 then room.go("a") end
end`

var fuzzInputs = []string{"start", "secret", "short", "cancel", "left", "ask", "type", "pick",
	"forever", "open", "shut", "take", "count", "go", "many"}

// FuzzSessionOps runs random sequences of player messages, clock steps and
// closes on three players. Whatever the order: no panic, every paused hook
// belongs to a player with an open question, messages have known types, a
// close names a question the player was shown, and once everything is
// closed no timer is left.
func FuzzSessionOps(f *testing.F) {
	f.Add([]byte{0, 12, 12, 5, 13, 10, 9, 14, 41, 15, 16, 17})
	f.Add([]byte{5, 37, 69, 13, 10, 42, 74, 16, 48, 80})
	f.Add([]byte{14, 12, 12, 12, 13, 13, 13, 14, 14})
	f.Add([]byte{6, 11, 12, 7, 18, 12, 13, 8, 12, 12})
	f.Add([]byte{9, 41, 73, 11, 43, 75, 15, 14, 16, 48, 80, 13})
	f.Add([]byte{18, 12, 10, 12, 13, 19, 20, 21})
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 200 {
			ops = ops[:200]
		}
		w, c := clockWorld(t, fuzzRoomA, fuzzRoomB)
		players := make([]*fuzzPlayer, 3)
		for i := range players {
			players[i] = newFuzzPlayer(t, w)
		}
		for _, op := range ops {
			p := players[int(op>>5)%len(players)]
			var got []ws.Message
			switch code := int(op & 31); {
			case code < len(fuzzInputs):
				got = p.s.Handle(ws.Message{Type: "input", Text: fuzzInputs[code]})
			case code == 15:
				got = p.s.Handle(ws.Message{Type: "choose", ID: p.open, Option: 1, Choices: []int{1}})
			case code == 16:
				got = p.s.Handle(ws.Message{Type: "answer", ID: p.open, Text: "Ann"})
			case code == 17:
				c.Advance(time.Second)
			case code == 18:
				c.Advance(5 * time.Second)
			case code == 19:
				c.Advance(DeliverGap)
			case code == 20:
				p.s.Close()
				players[int(op>>5)%len(players)] = newFuzzPlayer(t, w)
			case code == 21:
				got = p.s.Handle(ws.Message{Type: "click", Item: "box"})
			default:
				c.Advance(0)
			}
			p.see(t, got)
			for _, q := range players {
				ms, ok := q.s.(ws.Pusher).Pending()
				if !ok {
					t.Fatalf("a player lagged")
				}
				q.see(t, ms)
			}
			w.mu.Lock()
			open := 0
			for s := range w.sessions {
				if s.pending != nil {
					open++
				}
			}
			paused := w.rt.Paused()
			w.mu.Unlock()
			if open != paused {
				t.Fatalf("after op %d: %d questions open, %d hooks paused", op, open, paused)
			}
		}
		for _, p := range players {
			p.s.Close()
		}
		c.Advance(time.Hour)
		w.Close()
		c.Advance(time.Hour)
		if n := c.running(); n != 0 {
			t.Fatalf("%d timers left after everything closed", n)
		}
		if n := w.rt.Paused(); n != 0 {
			t.Fatalf("%d hooks paused after everything closed", n)
		}
	})
}

type fuzzPlayer struct {
	s     ws.Session
	open  string
	shown map[string]bool
}

func newFuzzPlayer(t *testing.T, w *World) *fuzzPlayer {
	s, first := w.NewSession("", ws.Who{})
	p := &fuzzPlayer{s: s, shown: map[string]bool{}}
	p.see(t, first)
	return p
}

// see checks the messages p got and follows its open question.
func (p *fuzzPlayer) see(t *testing.T, ms []ws.Message) {
	t.Helper()
	for _, m := range ms {
		switch m.Type {
		case "dialog", "select", "ask":
			if m.ID == "" || p.shown[m.ID] {
				t.Fatalf("question id %q reused or empty", m.ID)
			}
			p.shown[m.ID] = true
			p.open = m.ID
		case "close":
			if !p.shown[m.ID] {
				t.Fatalf("close of %q, never shown", m.ID)
			}
			if m.ID == p.open {
				p.open = ""
			}
		case "timer":
			if m.ID == "" || m.Ms < 0 || m.Ms > 24*60*60*1000 || m.Ms > 0 && m.Title == "" {
				t.Fatalf("timer message %+v", m)
			}
		case "text", "room", "items", "error", "image":
		default:
			t.Fatalf("unknown message type %q", m.Type)
		}
	}
}
