package game

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
	"github.com/rumblebump/talespeak/internal/ws"
)

// lastingRoom starts each kind of timer that lasts with a store.
const lastingRoom = `
function on_enter(p) say("in a") end
function on_input(p, text)
  if text == "start" then timer.start("guards", 60, {show = "Guards"})
  elseif text == "secret" then timer.start("hush", 60)
  elseif text == "room" then timer.start("turn", 60, {room = true, show = "Turn"})
  elseif text == "cancel" then timer.cancel("guards") timer.cancel("turn", {room = true})
  elseif text == "ask" then
    local n = ui.dialog("Fight or flee?", {"Fight", "Flee"}, {timeout = 30})
    say("answer " .. tostring(n))
  elseif text == "other" then
    local n = ui.dialog("Left or right?", {"Left", "Right"}, {timeout = 30})
    say("other " .. tostring(n))
  elseif text == "left" then
    say(tostring(timer.left("guards")) .. " " .. tostring(timer.left("turn", {room = true})))
  elseif text == "go" then room.go("b")
  end
end
function on_timer(p, name, kind) say("up " .. name .. " " .. tostring(kind)) end`

const lastingRoomB = `
function on_enter(p) say("in b") end
function on_input(p, text) if text == "back" then room.go("a") end end
function on_timer(p, name, kind) say("b up " .. name .. " " .. tostring(kind)) end`

// clockStoreWorld is a world of rooms a and b with st on c's clock.
func clockStoreWorld(t *testing.T, st store.Worlds, world store.World, c *fakeClock) *World {
	t.Helper()
	p, err := pack.Load(roomsFS(lastingRoom, lastingRoomB))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithStore(st, world.ID), WithClock(c.Now, c.After))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// timerMsg finds the timer message with id in ms.
func timerMsg(ms []ws.Message, id string) (ws.Message, bool) {
	for _, m := range ms {
		if m.Type == "timer" && m.ID == id {
			return m, true
		}
	}
	return ws.Message{}, false
}

// A player's timers survive a restart with the time they have left, and
// fire on time; a secret one is not shown when the player comes back.
func TestLastingPlayerTimerRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, world, who := openStore(t, path)
	c := newClock()
	w := clockStoreWorld(t, st, world, c)
	mira, _ := w.NewSession("", who[0])
	input(mira, "start")
	input(mira, "secret")
	c.Advance(10 * time.Second)
	w.Close() // the server stops: no on_leave, no save
	st.Close()

	st, world, who = openStore(t, path)
	c.Advance(5 * time.Second)
	w = clockStoreWorld(t, st, world, c)
	mira, first := w.NewSession("", who[0])
	defer mira.Close()
	m, ok := timerMsg(first, "guards")
	if !ok || m.Title != "Guards" || m.Ms != 45000 {
		t.Fatalf("after the restart: %+v, want the guards' countdown with 45 s left", first)
	}
	if _, ok := timerMsg(first, "hush"); ok {
		t.Errorf("a secret timer was shown: %+v", first)
	}
	if got := texts(input(mira, "left")); got != "45 nil" {
		t.Errorf("left = %q", got)
	}
	c.Advance(45 * time.Second)
	if got := texts(pending(t, mira)); got != "timer | up guards nil | up hush nil" {
		t.Errorf("at the deadline: %q", got)
	}
	if n := c.running(); n != 0 {
		t.Errorf("%d clock timers running", n)
	}
	// Run out means gone, also from the store.
	w.Close()
	w = clockStoreWorld(t, st, world, c)
	mira2, first := w.NewSession("", who[0])
	defer mira2.Close()
	if _, ok := timerMsg(first, "guards"); ok || c.running() != 0 {
		t.Errorf("a timer that ran out came back: %+v", first)
	}
}

// A timer that ran out while the player was away fires as they come
// back, after on_enter; one cancelled before does not come back.
func TestLastingPlayerTimerReload(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := newClock()
	w := clockStoreWorld(t, st, world, c)
	mira, _ := w.NewSession("", who[0])
	input(mira, "start")
	mira.Close()
	c.Advance(2 * time.Minute)
	mira, first := w.NewSession("", who[0])
	if _, ok := timerMsg(first, "guards"); !ok || texts(first) != "timer | in a" {
		t.Fatalf("coming back: %q", texts(first))
	}
	c.Advance(0)
	if got := texts(pending(t, mira)); got != "timer | up guards nil" {
		t.Errorf("the timer that ran out meanwhile: %q", got)
	}

	input(mira, "start")
	input(mira, "cancel")
	mira.Close()
	mira, first = w.NewSession("", who[0])
	defer mira.Close()
	if texts(first) != "in a" || c.running() != 0 {
		t.Errorf("a cancelled timer came back: %q", texts(first))
	}
}

// A room timer survives a restart: players coming in see it with the
// time left, and it fires for its starter. One that ran out while the
// world was down fires once a player comes into its room.
func TestLastingRoomTimerRestart(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := newClock()
	w := clockStoreWorld(t, st, world, c)
	mira, _ := w.NewSession("", who[0])
	input(mira, "room")
	w.Close()
	c.Advance(20 * time.Second)

	w = clockStoreWorld(t, st, world, c)
	zed, first := w.NewSession("", who[1])
	defer zed.Close()
	if m, ok := timerMsg(first, "room:turn"); !ok || m.Ms != 40000 {
		t.Fatalf("a player coming in: %+v, want the room's countdown with 40 s left", first)
	}
	mira, _ = w.NewSession("", who[0])
	c.Advance(40 * time.Second)
	if got := texts(pending(t, mira)); got != "timer | up turn room" {
		t.Errorf("the starter: %q", got)
	}
	if got := texts(pending(t, zed)); got != "timer" {
		t.Errorf("the other player: %q", got)
	}

	// Ran out while the world was down, with nobody in the room.
	input(mira, "room")
	mira.Close()
	zed.Close()
	w.Close()
	c.Advance(2 * time.Minute)
	w = clockStoreWorld(t, st, world, c)
	c.Advance(0) // runs out before anybody is back: it waits
	zed, first = w.NewSession("", who[1])
	defer zed.Close()
	if _, ok := timerMsg(first, "room:turn"); ok {
		t.Errorf("a timer that ran out is shown: %+v", first)
	}
	c.Advance(0)
	if got := texts(pending(t, zed)); got != "up turn room" {
		t.Errorf("the first player in: %q", got)
	}
	w.Close()
	w = clockStoreWorld(t, st, world, c)
	if c.running() != 0 || runningRoomTimers(w) != 0 {
		t.Error("a room timer that fired came back")
	}
}

// A question's deadline lasts: asked again after a reload, the same
// question gets the time left, and once that passed it times out at
// once. An answer forgets it; another question has its own.
func TestLastingQuestionDeadline(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := newClock()
	w := clockStoreWorld(t, st, world, c)
	mira, _ := w.NewSession("", who[0])
	if got := input(mira, "ask"); len(got) != 1 || got[0].Ms != 30000 {
		t.Fatalf("ask: %+v", got)
	}
	c.Advance(10 * time.Second)
	mira.Close() // a reload cuts the hook off
	mira, _ = w.NewSession("", who[0])
	if got := input(mira, "other"); len(got) != 1 || got[0].Ms != 30000 {
		t.Fatalf("another question: %+v, want its own 30 s", got)
	}
	mira.Close()

	w.Close() // and a restart
	c.Advance(5 * time.Second)
	w = clockStoreWorld(t, st, world, c)
	mira, _ = w.NewSession("", who[0])
	got := input(mira, "ask")
	if len(got) != 1 || got[0].Type != "dialog" || got[0].Ms != 15000 {
		t.Fatalf("the same question again: %+v, want 15 s left", got)
	}
	if got := texts(mira.Handle(ws.Message{Type: "choose", ID: got[0].ID, Option: 2})); got != "answer 2" {
		t.Fatalf("answer: %q", got)
	}
	if got := input(mira, "ask"); len(got) != 1 || got[0].Ms != 30000 {
		t.Errorf("asked again after an answer: %+v, want a new 30 s", got)
	}
	mira.Close()

	c.Advance(time.Minute) // its time passes while the player is away
	mira, _ = w.NewSession("", who[0])
	defer mira.Close()
	got = input(mira, "ask")
	if len(got) != 1 || got[0].Ms != 1 {
		t.Fatalf("asked after its time passed: %+v", got)
	}
	c.Advance(0)
	if got := texts(pending(t, mira)); got != "close | answer nil" {
		t.Errorf("it times out at once: %q", got)
	}
}

// Finishing a world deletes its timers.
func TestLastingFinishDeletesTimers(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := newClock()
	w := clockStoreWorld(t, st, world, c)
	mira, _ := w.NewSession("", who[0])
	input(mira, "start")
	input(mira, "room")
	input(mira, "ask")
	w.Close()
	ctx := context.Background()
	if err := st.FinishWorld(ctx, world.ID); err != nil {
		t.Fatal(err)
	}
	rooms, err := st.RoomTimers(ctx, world.ID)
	if err != nil || len(rooms) != 0 {
		t.Errorf("room timers after finishing: %+v, %v", rooms, err)
	}
	ch, err := st.Character(ctx, world.ID, who[0].Account, who[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	if list, err := st.PlayerTimers(ctx, ch.ID); err != nil || len(list) != 0 {
		t.Errorf("player timers after finishing: %+v, %v", list, err)
	}
}

var _ store.Worlds = (*sqlite.Store)(nil)
