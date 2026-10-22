package game

import (
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// A player keeps the script instances of the last MaxRoomInstances rooms
// they entered (T-0022): coming back to one finds its globals, an older
// room starts over.

// chainWorld has scripted rooms r1 to rn, which count their on_enter calls
// and go to any room typed, and c, without a script. r1 is the start.
func chainWorld(t *testing.T, n int) *World {
	t.Helper()
	fsys := fstest.MapFS{
		"pack.yaml":    {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: r1\n")},
		"rooms/c.yaml": {Data: []byte("id: c\ntitle: C\ndescription: Room C.\nitems:\n  - {id: key, label: a key}\n")},
	}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("r%d", i)
		fsys["rooms/"+id+".yaml"] = &fstest.MapFile{Data: []byte("id: " + id + "\ntitle: R\ndescription: A room.\nscript: " + id + ".lua\n")}
		fsys["scripts/"+id+".lua"] = &fstest.MapFile{Data: []byte(`count = 0
function on_enter(p) count = count + 1 say(p.room .. " " .. count) end
function on_input(p, text) room.go(text) end`)}
	}
	p, err := pack.Load(fsys)
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

// goTo moves s to room and returns the text of the new room's on_enter.
func goTo(t *testing.T, s ws.Session, room string) string {
	t.Helper()
	got := s.Handle(ws.Message{Type: "input", Text: room})
	if len(got) < 2 || got[0].Type != "items" {
		t.Fatalf("go %s = %q", room, texts(got))
	}
	return texts(got[1:])
}

func TestRoomInstancesEvicted(t *testing.T) {
	w := chainWorld(t, 6)
	const client = "192.0.2.1"
	s, first := w.NewSession(client, ws.Who{})
	if texts(first) != "r1 1" {
		t.Fatalf("first = %q", texts(first))
	}
	ss := s.(*session)
	steps := []struct{ to, want string }{
		{"r2", "r2 1"}, {"r3", "r3 1"}, {"r4", "r4 1"},
		{"r1", "r1 2"},  // four instances: r1 is kept
		{"r5", "r5 1"},  // a fifth: r2, entered longest ago, goes
		{"r2", "r2 1"},  // r2 starts over; r3 goes
		{"r1", "r1 3"},  // still kept
		{"r1", "r1 4"},  // the current room at the limit runs again
		{"r3", "r3 1"},  // r4 goes
		{"r5", "r5 2"},  // kept
		{"r6", "r6 1"},  // r2 goes
		{"r4", "r4 1"},  // r1 goes
		{"r1", "r1 1"},  // and starts over
		{"r6", "r6 2"},  // kept
		{"c", "room:c"}, // a room without a script keeps no instance and evicts none
	}
	for _, st := range steps {
		if got := goTo(t, s, st.to); got != st.want {
			t.Errorf("go %s = %q, want %q", st.to, got, st.want)
		}
		if len(ss.envs) > MaxRoomInstances {
			t.Errorf("after %s: %d instances", st.to, len(ss.envs))
		}
		w.mu.Lock()
		if n := len(w.clientEnvs[client]); n != len(ss.envs) {
			t.Errorf("after %s: the client counts %d instances, the session has %d", st.to, n, len(ss.envs))
		}
		w.mu.Unlock()
	}
	if len(ss.envs) != MaxRoomInstances || ss.envs["r6"] == nil {
		t.Errorf("in c: %d instances, r6 kept = %v", len(ss.envs), ss.envs["r6"] != nil)
	}
	s.Close()
	if len(w.clientEnvs) != 0 {
		t.Errorf("clientEnvs after Close = %d clients", len(w.clientEnvs))
	}
}

// busyMove has a session in r4 with instances of r1 to r4 try to go to
// room while its address has used up its Lua time; the move is refused.
// Then the time refills.
func busyMove(t *testing.T, room string) ws.Session {
	t.Helper()
	w := chainWorld(t, 5)
	clk := &stepClock{t: time.Unix(1e9, 0)}
	w.now = clk.now
	s, _ := w.NewSession("192.0.2.1", ws.Who{})
	t.Cleanup(s.Close)
	for _, r := range []string{"r2", "r3", "r4"} {
		goTo(t, s, r)
	}
	clk.step = 3 * time.Second // r4's on_input "takes" more than the 2 s burst
	got := s.Handle(ws.Message{Type: "input", Text: room})
	clk.step, clk.t = 0, clk.t.Add(time.Hour)
	if kinds(got) != "error:" || got[0].Message != busyMessage || roomOf(s) != "r4" {
		t.Fatalf("busy move to %s = %q, room %q", room, texts(got), roomOf(s))
	}
	return s
}

// BUG: a move undone because the address is busy (enterLocked, after
// place) leaves the target at the end of s.visited, as if the player had
// entered it: here r1, entered first, outlives r2.
func TestRoomGoRefusedMoveKeepsVisitOrder(t *testing.T) {
	s := busyMove(t, "r1")
	goTo(t, s, "r5") // the fifth instance: r1, entered longest ago, should go
	if got := goTo(t, s, "r2"); got != "r2 2" {
		t.Errorf("r2 = %q, want r2 2: r2 was evicted instead of r1, which the player never re-entered", got)
	}
}

// BUG: session.env evicts an older room's instance before it knows the new
// one can start: when the move is then refused (address busy, or still
// full), the player stays, but has lost r1's globals for nothing.
func TestRoomGoRefusedMoveEvictsNothing(t *testing.T) {
	s := busyMove(t, "r5")
	if n := len(s.(*session).envs); n != 4 {
		t.Errorf("%d instances after the refused move, want r1 to r4", n)
	}
	if got := goTo(t, s, "r1"); got != "r1 2" {
		t.Errorf("r1 = %q, want r1 2: a refused move lost r1's globals", got)
	}
}
