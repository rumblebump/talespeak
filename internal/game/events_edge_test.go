package game

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Room events (events.go) where they meet the rest of the world: rooms
// apart, on_leave, room timers, on_change, a store, restarts and /reset.

// edgeEvents tells events from the hooks that run without the player
// typing: on_leave, on_timer and on_change.
const edgeEvents = `
function on_input(p, text)
  if text == "ring" then room.event(p.name .. " rings the bell.")
  elseif text == "alarm" then say(tostring(timer.start("alarm", 10, {room = true})))
  elseif text == "go" then room.go("b")
  elseif text == "flag" then state.world.set("flag", true)
  end
end
function on_leave(p) room.event(p.name .. " leaves.") end
function on_timer(p, name, kind) room.event("The alarm rings for " .. p.name .. ".", {all = true}) end
function on_change(p, keys)
  local ok, err = room.event(p.name .. " sees the flag.")
  say("change " .. tostring(ok) .. " " .. tostring(err))
end
`

// ringOf is the texts of room's kept events.
func ringOf(w *World, room string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var parts []string
	for _, ev := range w.events[room] {
		parts = append(parts, ev.text)
	}
	return strings.Join(parts, " | ")
}

// Each room keeps its own events; a player who connects reads only those
// of the room they connect to, and moving does not replay another's.
func TestRoomEventsPerRoom(t *testing.T) {
	w, _ := clockWorld(t, edgeEvents, edgeEvents)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	input(b, "go")
	input(a, "ring")
	input(b, "ring")
	if got := ringOf(w, "a"); got != "traveler rings the bell." {
		t.Errorf("ring of a = %q", got)
	}
	if got := ringOf(w, "b"); got != "traveler 2 rings the bell." {
		t.Errorf("ring of b = %q", got)
	}
	c, first := w.NewSession("", ws.Who{})
	defer c.Close()
	if got := eventTypes(first); got != "replay:traveler rings the bell." {
		t.Errorf("c in a: %q", got)
	}
	if got := eventTypes(input(c, "go")); got != "" {
		t.Errorf("c moved to b: %q", got)
	}
}

// What on_leave tells reaches those who stay and is kept; on_timer of a
// room timer tells everyone in the room with all.
func TestRoomEventsFromLeaveAndTimer(t *testing.T) {
	w, c := clockWorld(t, edgeEvents, edgeEvents)
	a, _ := w.NewSession("", ws.Who{})
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	pending(t, b)
	a.Close()
	if got := eventTypes(pending(t, b)); got != "traveler leaves." {
		t.Errorf("b after a left: %q", got)
	}
	d, _ := w.NewSession("", ws.Who{})
	defer d.Close()
	pending(t, b)
	if got := texts(input(b, "alarm")); got != "true" {
		t.Fatalf("alarm = %q", got)
	}
	c.Advance(10 * time.Second)
	want := "The alarm rings for traveler 2."
	if got := eventTypes(pending(t, b)); got != want {
		t.Errorf("b, whose timer it is: %q", got)
	}
	if got := eventTypes(pending(t, d)); got != want {
		t.Errorf("d: %q", got)
	}
	if got := ringOf(w, "a"); got != "traveler leaves. | "+want {
		t.Errorf("ring = %q", got)
	}
}

// on_change runs for every player, so it cannot tell room events (they
// would multiply): room.event returns nil and the reason, and nothing is
// sent or kept.
func TestRoomEventFromChange(t *testing.T) {
	w, c := clockWorld(t, edgeEvents, edgeEvents)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	input(a, "flag")
	c.Advance(0)
	if got := texts(pending(t, b)); got != "change nil room.event: on_change cannot tell room events; tell them where the change is made" {
		t.Errorf("b: %q", got)
	}
	if got := eventTypes(pending(t, a)); got != "" {
		t.Errorf("a: %q", got)
	}
	c.Advance(time.Minute)
	if got := pending(t, a); len(got) != 0 {
		t.Errorf("a later: %+v", got)
	}
	if got := ringOf(w, "a"); got != "" {
		t.Errorf("ring = %q", got)
	}
}

// /reset room forgets only that room's events.
func TestResetForgetsOnlyThatRoom(t *testing.T) {
	w, _ := clockWorld(t, edgeEvents, edgeEvents)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	input(a, "ring")
	input(a, "go")
	input(a, "ring")
	input(gm, "/reset room b")
	if got := ringOf(w, "b"); got != "" {
		t.Errorf("b after its reset: %q", got)
	}
	if got := ringOf(w, "a"); got != "traveler 2 rings the bell." {
		t.Errorf("a after b's reset: %q", got)
	}
}

// eventIDRE is what the client takes as an event id (clean.js eventID).
var eventIDRE = regexp.MustCompile(`^[0-9a-z]{1,16}\.[0-9a-z]{1,16}$`)

// Event ids are unique in a run and across runs of a world, and in the
// form the client takes.
func TestRoomEventIDsAcrossRuns(t *testing.T) {
	ids := map[string]bool{}
	for run := 0; run < 2; run++ {
		p, err := pack.Load(roomsFS(edgeEvents, edgeEvents))
		if err != nil {
			t.Fatal(err)
		}
		c := newClock()
		c.now = c.now.Add(time.Duration(run) * time.Second) // the next run starts later
		w, err := NewWorld(p, script.DefaultLimits, quiet, WithClock(c.Now, c.After))
		if err != nil {
			t.Fatal(err)
		}
		a, _ := w.NewSession("", ws.Who{})
		b, _ := w.NewSession("", ws.Who{})
		for i := 0; i < 40; i++ { // past one base 36 digit
			input(a, "ring")
		}
		for _, m := range pending(t, b) {
			if m.Type != "event" {
				continue
			}
			if !eventIDRE.MatchString(m.ID) || ids[m.ID] {
				t.Errorf("run %d: id %q (seen: %v)", run, m.ID, ids[m.ID])
			}
			ids[m.ID] = true
		}
		a.Close()
		b.Close()
		w.Close()
	}
	if len(ids) != 2*40 {
		t.Errorf("%d ids", len(ids))
	}
}

// With a store, a character who connects again comes back in their room
// and reads what happened there, not elsewhere; what they typed is still
// theirs. A restart forgets the events: they live in memory only.
func TestRoomEventsReplayWithStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, world, who := openStore(t, path)
	w := storeWorldWith(t, st, world, eventRoom, eventRoom)
	mira, _ := w.NewSession("", who[0])
	zed, _ := w.NewSession("", who[1])
	input(mira, "all 1")
	input(mira, "go")
	input(mira, "all 2")
	input(mira, "keep Quellbach") // in state.player: still hers when she comes back
	mira.Close()
	zed.Close()

	mira, first := w.NewSession("", who[0])
	if got := eventTypes(first); got != "replay:The bell rings 2 times." {
		t.Errorf("Mira back in b: %q", got)
	}
	w.mu.Lock()
	err := w.shareable("at Quellbach")
	w.mu.Unlock()
	if err != script.ErrTyped {
		t.Errorf("Mira's word after she came back: %v", err)
	}
	zed, first = w.NewSession("", who[1])
	if got := eventTypes(first); got != "replay:The bell rings 1 times." {
		t.Errorf("Zed in a: %q", got)
	}
	mira.Close()
	zed.Close()
	w.Close()
	st.Close()

	st, world, who = openStore(t, path)
	w = storeWorldWith(t, st, world, eventRoom, eventRoom)
	mira, first = w.NewSession("", who[0])
	defer mira.Close()
	if got := eventTypes(first); got != "" {
		t.Errorf("after a restart: %q", got)
	}
}

// A store character's own name may be shared, though they typed it.
func TestStoreNameIsShareable(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	w := storeWorldWith(t, st, world, eventRoom, eventRoom)
	mira, _ := w.NewSession("", who[0])
	defer mira.Close()
	zed, _ := w.NewSession("", who[1])
	defer zed.Close()
	if got := texts(input(mira, "reveal Mira, ZED")); got != "true nil" {
		t.Errorf("names: %q", got)
	}
}

// /sayroom takes a room id in any letter case, needs a text, is audited
// and holds back a game master who sends too many.
func TestSayRoomEdges(t *testing.T) {
	w, _ := clockWorld(t, eventRoom, eventRoom)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	input(b, "go")
	pending(t, gm)

	if got := input(gm, "/sayroom B   Look up.  "); len(got) != 1 || got[0].Text != "to B (b): Look up." {
		t.Errorf("gm: %+v", got)
	}
	if got := pending(t, b); len(got) != 1 || got[0].Text != "Look up." {
		t.Errorf("b: %+v", got)
	}
	for _, args := range []string{"", "b", "b   ", "here"} {
		if got := input(gm, "/sayroom "+args); len(got) != 1 || !strings.HasPrefix(got[0].Text, "Type /sayroom") {
			t.Errorf("/sayroom %q: %+v", args, got)
		}
	}
	w.mu.Lock()
	last := w.auditLog[len(w.auditLog)-1]
	w.mu.Unlock()
	if last.Action != "sayroom" || last.Detail != "b Look up." {
		t.Errorf("audit = %+v", last)
	}
	slowed := false
	for i := 0; i < broadcastBurst+1; i++ {
		if got := input(gm, "/sayroom b again"); len(got) == 1 && got[0].Text == slowDownMessage {
			slowed = true
		}
	}
	if !slowed {
		t.Error("no slow down after a burst")
	}
	other, _ := w.NewSession("", ws.Who{GM: true})
	defer other.Close()
	input(other, "/gm off")
	if got := input(other, "/sayroom b hi"); len(got) != 1 || !strings.Contains(got[0].Text, "no command /sayroom") {
		t.Errorf("GM off: %+v", got)
	}
	if got := ringOf(w, "b"); got != "" {
		t.Errorf("/sayroom was kept: %q", got)
	}
}
