package game

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of the demo's Lua libraries as they ship: lib/round.lua
// (T-0019) and lib/items.lua (T-0020), in rooms a and b of roomsFS.

// demoLibWorld is roomsFS(a, b) with the demo's lib/round.lua and
// lib/items.lua, on a fake clock.
func demoLibWorld(t *testing.T, a, b string) (*World, *fakeClock) {
	t.Helper()
	fsys := roomsFS(a, b)
	for _, name := range []string{"round", "items"} {
		src, err := fs.ReadFile(content.Demo(), "lib/"+name+".lua")
		if err != nil {
			t.Fatal(err)
		}
		fsys["lib/"+name+".lua"] = &fstest.MapFile{Data: src}
	}
	p, err := pack.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	c := newClock()
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithClock(c.Now, c.After))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w, c
}

// splitCmd is Lua that splits text into cmd and arg at the first space,
// and arg into ids at commas (Lua patterns are off in the sandbox).
const splitCmd = `
local function split(text)
  local sp = string.find(text, " ", 1, true)
  if not sp then return text, "" end
  return string.sub(text, 1, sp - 1), string.sub(text, sp + 1)
end
local function ids(s)
  local out, i = {}, 1
  while true do
    local j = string.find(s, ",", 1 + i - 1, true)
    if not j then out[#out + 1] = string.sub(s, i) return out end
    out[#out + 1] = string.sub(s, i, j - 1)
    i = j + 1
  end
end
`

// libRound drives lib/round.lua by typed commands. The world key "mode"
// says what on_round_end and on_turn_timeout do.
const libRound = splitCmd + `
local round = require("round")
local function say2(a, b) say(tostring(a) .. " " .. tostring(b)) end
function on_input(p, text)
  local cmd, arg = split(text)
  if cmd == "turns" then
    local order = nil
    if arg ~= "" then order = ids(arg) end
    say2(round.turns(p, order))
  elseif cmd == "timed" then say2(round.turns(p, nil, {seconds = 30}))
  elseif cmd == "done" then say2(round.done(p))
  elseif cmd == "next" then say2(round.next(p))
  elseif cmd == "free" then say2(round.free(p))
  elseif cmd == "cur" then say2(round.current(p))
  elseif cmd == "status" then say(tostring(round.status(p)))
  elseif cmd == "act" then say(tostring(round.may_act(p)))
  elseif cmd == "leave" then say(tostring(round.leave(p)))
  elseif cmd == "out" then round.leave(p) room.go("b")
  elseif cmd == "go" then room.go(arg)
  elseif cmd == "mode" then state.world.set("mode", arg)
  end
end
function on_turn(p) say("turn " .. p.id) end
function on_round_end(p, n)
  say("end " .. n)
  local mode = state.world.get("mode")
  if mode == "free" then round.free(p)
  elseif mode == "reverse" then
    local list, order = room.players(), {}
    for i = #list, 1, -1 do order[#order + 1] = list[i].id end
    round.turns(p, order)
  end
end
function on_turn_timeout(p, id, name)
  if state.world.get("mode") == "keep" then
    say("timeout " .. id .. " " .. name)
  else
    round.next(p)
  end
end
function on_change(p, keys) round.changed(p, keys) end
function on_timer(p, name, kind) round.timeout(p, name, kind) end
function on_leave(p) round.leave(p) end`

const libRoundB = `
function on_input(p, text) room.go("a") end
function on_change(p, keys) end`

func roundPlayers(t *testing.T, n int) (*World, *fakeClock, []ws.Session) {
	t.Helper()
	w, c := demoLibWorld(t, libRound, libRoundB)
	ss := make([]ws.Session, n)
	for i := range ss {
		ss[i], _ = w.NewSession("", ws.Who{})
		t.Cleanup(ss[i].Close)
	}
	return w, c, ss
}

// round.turns keeps the ids of players in the room, once each, in the
// order given.
func TestRoundTurnsOrderFiltered(t *testing.T) {
	tests := []struct {
		name, order string
		want        string // what turns says to p1
		status      string // round.status for p1
	}{
		{"absent and duplicates", "p3,p9,p1,p3,p2,p1", "true nil", "Round 1. Order: traveler 3 (acting), traveler (you), traveler 2."},
		{"one", "p2", "true nil", "Round 1. Order: traveler 2 (acting)."},
		{"only absent", "p9,p10", "nil nobody here to play", "nil"},
		{"empty id", "", "turn p1 | true nil", "Round 1. Order: traveler (you) (acting), traveler 2, traveler 3."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ss := roundPlayers(t, 3)
			cmd := "turns " + tt.order
			if tt.order == "" {
				cmd = "turns"
			}
			if got := texts(input(ss[0], cmd)); got != tt.want {
				t.Errorf("turns = %q, want %q", got, tt.want)
			}
			if got := texts(input(ss[0], "status")); got != tt.status {
				t.Errorf("status = %q, want %q", got, tt.status)
			}
		})
	}
}

// The first in the order gets on_turn through on_change, once.
func TestRoundTurnsFirstHearsOnce(t *testing.T) {
	_, c, ss := roundPlayers(t, 3)
	input(ss[0], "turns p3,p1")
	if got, _ := heard(t, c, ss[2]); got != "turn p3" {
		t.Fatalf("p3 = %q", got)
	}
	input(ss[0], "mode x") // another world key: no second on_turn
	if got, _ := heard(t, c, ss[2]); got != "" {
		t.Errorf("p3 after another change = %q", got)
	}
	if got, _ := heard(t, c, ss[1]); got != "" {
		t.Errorf("p2, not in the order = %q", got)
	}
	if got := texts(input(ss[1], "act")); got != "Wait for your turn: traveler 3 is acting. | false" {
		t.Errorf("p2 act = %q", got)
	}
}

// What the functions say when the room runs free.
func TestRoundFree(t *testing.T) {
	_, _, ss := roundPlayers(t, 1)
	for cmd, want := range map[string]string{
		"done": "nil no turns here", "next": "nil no turns here", "cur": "nil nil",
		"status": "nil", "act": "true", "leave": "true", "free": "true nil",
	} {
		if got := texts(input(ss[0], cmd)); got != want {
			t.Errorf("%s = %q, want %q", cmd, got, want)
		}
	}
}

func TestRoundDoneNotYourTurn(t *testing.T) {
	_, _, ss := roundPlayers(t, 2)
	input(ss[0], "turns")
	if got := texts(input(ss[1], "done")); got != "nil not your turn" {
		t.Errorf("p2 done = %q", got)
	}
	if got := texts(input(ss[1], "cur")); got != "p1 traveler" {
		t.Errorf("cur = %q", got)
	}
}

// round.leave of a player before the current one keeps the current; of
// one after it, the round goes on without them.
func TestRoundLeaveNotCurrent(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		_, c, ss := roundPlayers(t, 3)
		input(ss[0], "turns")
		input(ss[0], "done") // p2 acts
		heard(t, c, ss[1])
		if got := texts(input(ss[0], "leave")); got != "true" {
			t.Fatalf("leave = %q", got)
		}
		if got := texts(input(ss[2], "status")); got != "Round 1. Order: traveler 2 (acting), traveler 3 (you)." {
			t.Errorf("status = %q", got)
		}
		if got, _ := heard(t, c, ss[1]); got != "" {
			t.Errorf("p2 got on_turn again: %q", got)
		}
		input(ss[1], "done")
		if got, _ := heard(t, c, ss[2]); got != "turn p3" {
			t.Errorf("p3 = %q", got)
		}
		// p1 is still in the room: they join the next round, at its end.
		if got := texts(input(ss[2], "done")); got != "end 1 | true nil" {
			t.Fatalf("round end = %q", got)
		}
		if got := texts(input(ss[0], "status")); got != "Round 2. Order: traveler 2 (acting), traveler 3, traveler (you)." {
			t.Errorf("round 2 = %q", got)
		}
	})
	t.Run("after", func(t *testing.T) {
		_, c, ss := roundPlayers(t, 3)
		input(ss[0], "turns")
		input(ss[2], "out") // p3 leaves the room
		if got := texts(input(ss[0], "status")); got != "Round 1. Order: traveler (you) (acting), traveler 2." {
			t.Errorf("status = %q", got)
		}
		input(ss[0], "done")
		heard(t, c, ss[1])
		if got := texts(input(ss[1], "done")); got != "end 1 | true nil" {
			t.Fatalf("round end = %q", got)
		}
		if got, _ := heard(t, c, ss[0]); got != "turn p1" {
			t.Errorf("p1 in round 2 = %q", got)
		}
		if got := texts(input(ss[1], "status")); got != "Round 2. Order: traveler (acting), traveler 2 (you)." {
			t.Errorf("round 2 = %q", got)
		}
	})
}

// The current player walking out with room.go but without round.leave is
// skipped by the next player's may_act.
func TestRoundCurrentWalkedOut(t *testing.T) {
	_, c, ss := roundPlayers(t, 2)
	input(ss[0], "turns")
	input(ss[0], "go b")
	if got := texts(input(ss[1], "act")); got != "turn p2 | true" {
		t.Errorf("p2 act = %q", got)
	}
	if got, _ := heard(t, c, ss[1]); got != "" {
		t.Errorf("p2 on_turn twice: %q", got)
	}
	if got := texts(input(ss[1], "cur")); got != "p2 traveler 2" {
		t.Errorf("cur = %q", got)
	}
}

// Everyone leaving the world ends the round: the room runs free and its
// turn timer is gone.
func TestRoundEveryoneLeaves(t *testing.T) {
	w, c, ss := roundPlayers(t, 2)
	input(ss[0], "timed")
	ss[0].Close()
	if got, _ := heard(t, c, ss[1]); !strings.HasPrefix(got, "turn p2") {
		t.Fatalf("p2 after p1 left = %q", got)
	}
	ss[1].Close()
	w.mu.Lock()
	timers := len(w.roomTimers)
	w.mu.Unlock()
	if timers != 0 {
		t.Errorf("room timers left: %d", timers)
	}
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	if got := texts(input(s3, "cur")); got != "nil nil" {
		t.Errorf("after everyone left, cur = %q", got)
	}
	c.Advance(time.Minute)
	// p3 may get the removal of the turn timer still due to be delivered
	// (it came in before the delivery), but no turn and no countdown.
	for _, m := range pending(t, s3) {
		if m.Type != "timer" || m.Ms != 0 {
			t.Errorf("p3 = %+v", m)
		}
	}
}

// on_round_end may stop the round, or start another order: no second
// start of the next round.
func TestRoundEndHooks(t *testing.T) {
	t.Run("free", func(t *testing.T) {
		_, c, ss := roundPlayers(t, 2)
		input(ss[0], "mode free")
		input(ss[0], "turns")
		input(ss[0], "done")
		heard(t, c, ss[1])
		if got := texts(input(ss[1], "done")); got != "end 1 | true nil" {
			t.Fatalf("done = %q", got)
		}
		if got := texts(input(ss[0], "cur")); got != "nil nil" {
			t.Errorf("cur = %q", got)
		}
		if got, _ := heard(t, c, ss[0]); got != "" {
			t.Errorf("p1 got a turn: %q", got)
		}
	})
	t.Run("reverse", func(t *testing.T) {
		_, c, ss := roundPlayers(t, 2)
		input(ss[0], "mode reverse")
		input(ss[0], "turns")
		input(ss[0], "done")
		heard(t, c, ss[1])
		if got := texts(input(ss[1], "done")); got != "end 1 | turn p2 | true nil" {
			t.Fatalf("done = %q", got)
		}
		if got := texts(input(ss[0], "status")); got != "Round 1. Order: traveler 2 (acting), traveler (you)." {
			t.Errorf("status = %q", got)
		}
		if got, _ := heard(t, c, ss[0]); got != "" {
			t.Errorf("p1 got a turn: %q", got)
		}
	})
}

// With on_turn_timeout, a turn whose time ran out is the script's: here
// it keeps the turn; without (mode not "keep") the turn is skipped.
func TestRoundTurnTimeout(t *testing.T) {
	_, c, ss := roundPlayers(t, 2)
	input(ss[0], "mode keep")
	input(ss[0], "timed")
	c.Advance(30 * time.Second)
	if got := texts(pending(t, ss[0])); got != "timer | timeout p1 traveler" {
		t.Fatalf("p1 at the deadline = %q", got)
	}
	pending(t, ss[1])
	if got := texts(input(ss[1], "cur")); got != "p1 traveler" {
		t.Errorf("cur = %q", got)
	}
	input(ss[0], "mode skip")
	input(ss[0], "timed")
	c.Advance(30 * time.Second)
	if got, _ := heard(t, c, ss[1]); !strings.Contains(got, "turn p2") {
		t.Errorf("p2 after the skip = %q", got)
	}
	if got := texts(input(ss[1], "status")); got != "traveler took too long. Round 1. Order: traveler, traveler 2 (you) (acting)." {
		t.Errorf("status = %q", got)
	}
}

// libItems drives lib/items.lua by typed commands.
const libItems = splitCmd + `
local items = require("items")
items.define({
  { id = "coins", rule = "split", amount = 2, gives = "gold" },
  { id = "gems", rule = "split", amount = 7, gives = "gem" },
  { id = "potion", rule = "count", count = 2 },
  { id = "crown", rule = "once" },
  { id = "torch", rule = "each" },
})
ALL = {
  {id = "coins", label = "c"}, {id = "gems", label = "g"}, {id = "potion", label = "p"},
  {id = "crown", label = "k"}, {id = "torch", label = "t"}, {id = "rock", label = "r"},
}
function on_input(p, text)
  local cmd, arg = split(text)
  if cmd == "take" then
    local got, why, who = items.take(p, arg)
    if got then say("got " .. got.n .. " " .. got.name)
    else say("no " .. tostring(why) .. " " .. tostring(who)) end
  elseif cmd == "release" then say(tostring(items.release(p, arg)))
  elseif cmd == "vis" then
    local list = {}
    for _, it in ipairs(items.visible(p, ALL)) do list[#list + 1] = it.id end
    say(table.concat(list, ","))
  elseif cmd == "bag" then
    local list = {}
    for _, e in ipairs(items.inventory(p)) do list[#list + 1] = e.n .. " " .. e.name end
    say("bag: " .. table.concat(list, ", "))
  elseif cmd == "bless" then
    local opts = {}
    if arg == "moves" or arg == "both" then opts.moves = 2 end
    if arg == "seconds" or arg == "both" then opts.seconds = 10 opts.show = "Glow" end
    say(tostring(items.bless(p, "glow", opts)))
  elseif cmd == "blessed" then say(tostring(items.blessed(p, "glow")))
  elseif cmd == "move" then items.move(p) say("moved")
  elseif cmd == "go" then room.go(arg)
  end
end
function on_timer(p, name)
  if not items.on_timer(p, name) then say("other timer " .. name) end
end
function on_effect_end(p, id) say("effect ends " .. id) end`

// libItemsB has no item library: its on_timer only says the name.
const libItemsB = `
function on_input(p, text) room.go("a") end
function on_timer(p, name) say("b timer " .. name) end`

func itemPlayers(t *testing.T, n int) (*World, *fakeClock, []ws.Session) {
	t.Helper()
	w, c := demoLibWorld(t, libItems, libItemsB)
	ss := make([]ws.Session, n)
	for i := range ss {
		ss[i], _ = w.NewSession("", ws.Who{})
		t.Cleanup(ss[i].Close)
	}
	return w, c, ss
}

// steps runs typed commands on players and checks what each says.
type step struct {
	who       int
	cmd, want string
}

func runSteps(t *testing.T, ss []ws.Session, steps []step) {
	t.Helper()
	for i, st := range steps {
		if got := texts(input(ss[st.who], st.cmd)); got != st.want {
			t.Errorf("step %d: p%d %q = %q, want %q", i, st.who+1, st.cmd, got, st.want)
		}
	}
}

func TestItemsSplit(t *testing.T) {
	t.Run("more players than coins", func(t *testing.T) {
		_, _, ss := itemPlayers(t, 3)
		runSteps(t, ss, []step{
			// The first take shares among all three: with fewer coins than
			// players, the first to come in get one, even when the last
			// one opened the purse.
			{2, "take coins", "no none nil"}, // there, but not enough for everyone
			{2, "vis", "gems,potion,crown,torch,rock"},
			{0, "take coins", "got 1 gold"},
			{1, "vis", "coins,gems,potion,crown,torch,rock"},
			{1, "take coins", "got 1 gold"},
			{1, "take coins", "no had nil"},
			{1, "bag", "bag: 1 gold"},
		})
	})
	t.Run("remainder stays", func(t *testing.T) {
		w, _, ss := itemPlayers(t, 3)
		runSteps(t, ss, []step{
			{0, "take gems", "got 2 gem"},
			{1, "take gems", "got 2 gem"},
			{2, "take gems", "got 2 gem"},
		})
		s4, _ := w.NewSession("", ws.Who{})
		defer s4.Close()
		runSteps(t, []ws.Session{s4}, []step{{0, "take gems", "no late nil"}})
	})
}

func TestItemsCountOnceEach(t *testing.T) {
	_, _, ss := itemPlayers(t, 3)
	runSteps(t, ss, []step{
		{0, "take potion", "got 1 potion"},
		{0, "take potion", "got 1 potion"}, // a count item is one per take, not one per player
		{1, "take potion", "no gone nil"},
		{2, "vis", "coins,gems,crown,torch,rock"},
		{0, "bag", "bag: 2 potion"},

		{0, "take crown", "got 1 crown"},
		{1, "take crown", "no gone traveler"},
		{1, "release crown", "false"}, // not the owner
		{1, "release potion", "false"},
		{1, "release rock", "false"},
		{1, "release torch", "false"},
		{1, "vis", "coins,gems,torch,rock"},
		{0, "release crown", "true"},
		{0, "release crown", "false"},
		{1, "take crown", "got 1 crown"},

		{0, "take torch", "got 1 torch"},
		{0, "take torch", "no had nil"},
		{1, "take torch", "got 1 torch"},
		{1, "take rock", "no unknown nil"},
		{0, "vis", "coins,gems,rock"},
	})
}

func TestItemsBless(t *testing.T) {
	t.Run("moves only", func(t *testing.T) {
		_, c, ss := itemPlayers(t, 1)
		got := input(ss[0], "bless moves")
		if texts(got) != "true" || find(got, "timer") != nil {
			t.Fatalf("bless = %+v", got)
		}
		runSteps(t, ss, []step{
			{0, "blessed", "true"},
			{0, "move", "moved"},
			{0, "blessed", "true"},
			{0, "move", "effect ends glow | moved"},
			{0, "blessed", "false"},
			{0, "move", "moved"},
		})
		if n := c.running(); n != 0 {
			t.Errorf("%d timers running", n)
		}
	})
	t.Run("seconds only", func(t *testing.T) {
		_, c, ss := itemPlayers(t, 1)
		got := input(ss[0], "bless seconds")
		if m := find(got, "timer"); m == nil || m.ID != "fx_glow" || m.Title != "Glow" || m.Ms != 10000 {
			t.Fatalf("bless = %+v", got)
		}
		for range 10 {
			input(ss[0], "move")
		}
		runSteps(t, ss, []step{{0, "blessed", "true"}})
		c.Advance(10 * time.Second)
		if got := texts(pending(t, ss[0])); got != "timer | effect ends glow" {
			t.Errorf("at the end = %q", got)
		}
		runSteps(t, ss, []step{{0, "blessed", "false"}})
	})
	t.Run("again starts over", func(t *testing.T) {
		_, c, ss := itemPlayers(t, 1)
		input(ss[0], "bless both")
		c.Advance(5 * time.Second)
		input(ss[0], "move")
		input(ss[0], "bless both") // 10 s and 2 moves again
		c.Advance(6 * time.Second)
		runSteps(t, ss, []step{{0, "blessed", "true"}, {0, "move", "moved"}})
		c.Advance(4 * time.Second)
		if got := texts(pending(t, ss[0])); got != "timer | effect ends glow" {
			t.Errorf("at the end = %q", got)
		}
	})
	t.Run("moves after seconds", func(t *testing.T) {
		_, c, ss := itemPlayers(t, 1)
		input(ss[0], "bless seconds")
		input(ss[0], "bless moves") // no time limit any more
		if n := c.running(); n != 0 {
			t.Errorf("%d timers running", n)
		}
	})
	t.Run("neither", func(t *testing.T) {
		_, _, ss := itemPlayers(t, 1)
		if got := input(ss[0], "bless none"); !isError(got) {
			t.Errorf("bless without seconds or moves = %+v", got)
		}
	})
}

// A player's other timers are not effects.
func TestItemsOtherTimer(t *testing.T) {
	_, c, ss := clockLib(t)
	input(ss[0], "start")
	c.Advance(5 * time.Second)
	if got := texts(pending(t, ss[0])); got != "other timer plain" {
		t.Errorf("= %q", got)
	}
}

func clockLib(t *testing.T) (*World, *fakeClock, []ws.Session) {
	t.Helper()
	w, c := demoLibWorld(t, libItems+`
local base = on_input
function on_input(p, text)
  if text == "start" then timer.start("plain", 5) else base(p, text) end
end`, libItemsB)
	s, _ := w.NewSession("", ws.Who{})
	t.Cleanup(s.Close)
	return w, c, []ws.Session{s}
}
