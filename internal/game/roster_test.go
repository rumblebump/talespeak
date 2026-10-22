package game

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// T-0036: player.gm in every hook, and world.players() across rooms.

// rosterRoom reports player.gm and world.players(); rosterRoomB is the
// same room under another id.
const rosterRoom = `
local function roster()
  local parts = {}
  for _, q in ipairs(world.players()) do
    parts[#parts + 1] = q.id .. "@" .. q.room .. (q.gm and "*" or "")
  end
  return table.concat(parts, ",")
end
function on_enter(p) say("enter " .. tostring(p.gm)) end
function on_input(p, text)
  if text == "roster" then say(roster())
  elseif text == "here" then
    local parts = {}
    for _, q in ipairs(room.players()) do parts[#parts + 1] = q.id .. (q.gm and "*" or "") end
    say(table.concat(parts, ","))
  elseif text == "go" then room.go(p.room == "a" and "b" or "a")
  elseif text == "timer" then timer.start("t", 1)
  elseif text == "write" then state.world.set("k", text)
  elseif text == "left" then say(tostring(state.world.get("left")))
  else say("input " .. tostring(p.gm)) end
end
function on_timer(p) say("timer " .. tostring(p.gm)) end
function on_change(p) say("change " .. tostring(p.gm)) end
function on_leave(p) state.world.set("left", p.id .. " " .. tostring(p.gm) .. " " .. roster()) end`

func TestWorldPlayersAcrossRooms(t *testing.T) {
	w, c := clockWorld(t, rosterRoom, rosterRoom)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{GM: true})
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	if got := texts(input(s3, "roster")); got != "p1@a,p2@a*,p3@a" {
		t.Fatalf("roster = %q", got)
	}
	input(s1, "go")
	if got := texts(input(s3, "roster")); got != "p1@b,p2@a*,p3@a" {
		t.Errorf("after p1 went to b: %q, want the same order with p1's new room", got)
	}
	if got := texts(input(s3, "here")); got != "p2*,p3" {
		t.Errorf("room.players = %q", got)
	}
	s2.Close()
	c.Advance(DeliverGap)
	for _, s := range []ws.Session{s1, s3} {
		if got := texts(pending(t, s)); got != "change false" {
			t.Errorf("after p2 left: %q", got)
		}
	}
	if got := texts(input(s1, "roster")); got != "p1@b,p3@a" {
		t.Errorf("after p2 left: %q", got)
	}
	s4, _ := w.NewSession("", ws.Who{})
	defer s4.Close()
	if got := texts(input(s1, "roster")); got != "p1@b,p3@a,p4@a" {
		t.Errorf("a new player is last: %q", got)
	}
	if got := texts(input(s1, "left")); got != "p2 true p1@b,p3@a" {
		t.Errorf("on_leave saw %q, want the leaver with gm and without itself in the list", got)
	}
}

func TestPlayerGMInEveryHook(t *testing.T) {
	w, c := clockWorld(t, rosterRoom, rosterRoom)
	gm, first := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if got := texts(first); got != "enter true" {
		t.Errorf("on_enter = %q", got)
	}
	p, first := w.NewSession("", ws.Who{})
	defer p.Close()
	if got := texts(first); got != "enter false" {
		t.Errorf("on_enter of a player = %q", got)
	}
	if got := texts(input(gm, "x")); got != "input true" {
		t.Errorf("on_input = %q", got)
	}
	if got := texts(input(p, "x")); got != "input false" {
		t.Errorf("on_input of a player = %q", got)
	}
	input(gm, "timer")
	input(p, "write")
	c.Advance(DeliverGap)
	if got := texts(pending(t, gm)); got != "change true" {
		t.Errorf("on_change of the GM = %q", got)
	}
	c.Advance(time.Second)
	if got := texts(pending(t, gm)); got != "timer true" {
		t.Errorf("on_timer of the GM = %q", got)
	}
	if got := texts(input(gm, "go")); got != "items:rope | enter true" {
		t.Errorf("on_enter after room.go = %q", got)
	}
}

// world.players lists who is online now: after a restart, only the players
// who came back, whatever on_leave did or did not run.
func TestWorldPlayersAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, world, who := openStore(t, path)
	w := storeWorldWith(t, st, world, rosterRoom, rosterRoom)
	gmWho := who[0]
	gmWho.GM = true
	mira, _ := w.NewSession("", gmWho)
	zed, _ := w.NewSession("", who[1])
	got := texts(input(zed, "roster"))
	if len(strings.Split(got, ",")) != 2 || !strings.Contains(got, "@a*") {
		t.Fatalf("roster = %q", got)
	}
	w.Close() // a shutdown: on_leave does not run
	mira.Close()
	zed.Close()
	st.Close()

	st, world, who = openStore(t, path)
	w = storeWorldWith(t, st, world, rosterRoom, rosterRoom)
	zed, _ = w.NewSession("", who[1])
	defer zed.Close()
	got = texts(input(zed, "roster"))
	if strings.Contains(got, ",") || strings.Contains(got, "*") || !strings.HasSuffix(got, "@a") {
		t.Errorf("after the restart: %q, want Zed alone", got)
	}
	w.End("bye")
	if ms := input(zed, "roster"); !isError(ms) {
		t.Errorf("after the world ended: %v", ms)
	}
}
