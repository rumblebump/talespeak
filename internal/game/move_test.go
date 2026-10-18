package game

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// roomsWorld is a pack with rooms a and b, which run the given scripts,
// and c, which has no script and one item.
func roomsWorld(t *testing.T, a, b string) *World {
	t.Helper()
	p, err := pack.Load(roomsFS(a, b))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// roomsFS is the pack of roomsWorld.
func roomsFS(a, b string) fstest.MapFS {
	return fstest.MapFS{
		"pack.yaml":     {Data: []byte("id: t\ntitle: T\nstart: a\n")},
		"rooms/a.yaml":  {Data: []byte("id: a\ntitle: A\ndescription: Room A.\nscript: a.lua\nitems:\n  - {id: box, label: a box}\n")},
		"rooms/b.yaml":  {Data: []byte("id: b\ntitle: B\ndescription: Room B.\nscript: b.lua\nitems:\n  - {id: rope, label: a rope}\n")},
		"rooms/c.yaml":  {Data: []byte("id: c\ntitle: C\ndescription: Room C.\nitems:\n  - {id: key, label: a key}\n")},
		"scripts/a.lua": {Data: []byte(a)},
		"scripts/b.lua": {Data: []byte(b)},
	}
}

// texts lists the types of ms, with the text of text messages and the
// room id of room messages.
func texts(ms []ws.Message) string {
	var parts []string
	for _, m := range ms {
		switch m.Type {
		case "text":
			parts = append(parts, m.Text)
		case "room":
			parts = append(parts, "room:"+m.ID)
		case "items":
			var ids []string
			for _, it := range m.Items {
				ids = append(ids, it.ID)
			}
			parts = append(parts, "items:"+strings.Join(ids, ","))
		default:
			parts = append(parts, m.Type)
		}
	}
	return strings.Join(parts, " | ")
}

const roomA = `
entered = 0
function on_enter(player) entered = entered + 1 say("A " .. entered .. " " .. player.room) end
function on_click(player, id) say("leaving A") room.go("b") end
function on_input(player, text)
  if text == "stay" then room.go("b") room.go("a")
  elseif text == "wait" then room.go("b") ui.dialog("?", {"x"})
  elseif text == "fail" then room.go("b") error("broken")
  elseif text == "ask" then
    local n = ui.dialog("Where to?", {"b", "c"})
    room.go(n == 1 and "b" or "c")
  else room.go(text) end
end`

const roomB = `
function on_enter(player) say("B " .. player.room) end
function on_input(player, text) room.go(text) end`

func TestRoomGoMovesAfterTheHook(t *testing.T) {
	w := roomsWorld(t, roomA, roomB)
	s, first := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := texts(first); got != "A 1 a" {
		t.Fatalf("first = %q", got)
	}
	// The old room's output comes first; then the player is in b, with b's items.
	got := s.Handle(ws.Message{Type: "click", Item: "box"})
	if texts(got) != "leaving A | items:rope | B b" {
		t.Fatalf("click = %q", texts(got))
	}
	if got := s.Handle(ws.Message{Type: "click", Item: "box"}); !isError(got) {
		t.Errorf("a's item in b = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "click", Item: "rope"}); texts(got) != "Nothing happens." {
		t.Errorf("b's item = %q", texts(got))
	}
	// Back in a, its globals are as the player left them.
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "a"})); got != "items:box | A 2 a" {
		t.Errorf("back to a = %q", got)
	}
	// A room without a script is described, with its items.
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "c"})); got != "items:key | room:c" {
		t.Errorf("to c = %q", got)
	}
	if got := s.Handle(ws.Message{Type: "click", Item: "key"}); texts(got) != "Nothing happens." {
		t.Errorf("c's item = %q", texts(got))
	}
}

func TestRoomGoRules(t *testing.T) {
	w := roomsWorld(t, roomA, roomB)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	// The last room.go wins, and going to the current room runs on_enter again.
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "stay"})); got != "items:box | A 2 a" {
		t.Errorf("stay = %q", got)
	}
	// An unknown room, waiting after room.go and a failing hook move no one.
	for _, in := range []string{"attic", "wait", "fail"} {
		if got := s.Handle(ws.Message{Type: "input", Text: in}); kinds(got) != "error:" {
			t.Errorf("%s = %+v", in, got)
		}
		if got := texts(s.Handle(ws.Message{Type: "input", Text: "a"})); !strings.HasPrefix(got, "items:box | A ") {
			t.Errorf("after %s the player is not in a: %q", in, got)
		}
	}
	// A move after an answered dialog.
	got := s.Handle(ws.Message{Type: "input", Text: "ask"})
	if kinds(got) != "dialog:d1" {
		t.Fatalf("ask = %+v", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2})); got != "items:key | room:c" {
		t.Errorf("choose = %q", got)
	}
}

// Rooms whose on_enter send the player on cannot loop forever.
func TestRoomGoLoopIsBounded(t *testing.T) {
	w := roomsWorld(t, `function on_enter() say("A") room.go("b") end function on_input() say("A here") end`,
		`function on_enter() say("B") room.go("a") end`)
	s, first := w.NewSession("", ws.Who{})
	defer s.Close()
	if n := strings.Count(texts(first), "A"); kinds(first[len(first)-1:]) != "error:" || n != MaxMoves/2+1 {
		t.Fatalf("first = %q", texts(first))
	}
	// The count starts again with the next message.
	if got := s.Handle(ws.Message{Type: "input", Text: "x"}); len(got) == 0 {
		t.Errorf("input after the loop = %+v", got)
	}
}

// A player who leaves while a hook waits: the hook runs to its end without
// them, and its room.go is an error that moves no one.
func TestRoomGoAfterPlayerLeft(t *testing.T) {
	w := roomsWorld(t, `function on_click() pcall(ui.dialog, "?", {"x"}) room.go("b") end`, roomB)
	s, _ := w.NewSession("", ws.Who{})
	if got := s.Handle(ws.Message{Type: "click", Item: "box"}); kinds(got) != "dialog:d1" {
		t.Fatalf("click = %+v", got)
	}
	s.Close()
	if n := w.rt.Paused(); n != 0 {
		t.Errorf("%d hooks paused", n)
	}
}
