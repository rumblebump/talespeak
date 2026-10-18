package script

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// T-0031 at the script level: player.id, room.players and room timers
// against fake hosts, with and without Party.

// partyHost is the recorder with a room shared by players: room timers
// are recorded like player timers, with a "room" prefix.
type partyHost struct {
	recorder
	players    []Player
	roomTimers map[string]time.Duration
	refuse     bool // StartRoomTimer refuses every new timer
}

func (h *partyHost) Players() []Player { return h.players }

func (h *partyHost) StartRoomTimer(name string, d time.Duration, show string) error {
	if h.refuse {
		return errors.New("room is full of timers")
	}
	if h.roomTimers == nil {
		h.roomTimers = map[string]time.Duration{}
	}
	h.roomTimers[name] = d
	h.out = append(h.out, "roomtimer:"+name+"="+d.String()+"|"+show)
	return nil
}

func (h *partyHost) StopRoomTimer(name string) bool {
	_, ok := h.roomTimers[name]
	delete(h.roomTimers, name)
	h.out = append(h.out, "roomstop:"+name)
	return ok
}

func (h *partyHost) RoomTimerLeft(name string) (time.Duration, bool) {
	d, ok := h.roomTimers[name]
	return d, ok
}

// runParty runs on_enter of src on host h, with player id "p7".
func runParty(t *testing.T, h Host, src string) ([]string, error) {
	t.Helper()
	r := New(roomy)
	t.Cleanup(r.Close)
	env := load(t, r, src)
	_, err := r.RunHook(h, env, Hook{Name: "on_enter", Player: "Ada", PlayerID: "p7"})
	switch h := h.(type) {
	case *partyHost:
		return h.out, err
	case *recorder:
		return h.out, err
	}
	return nil, err
}

func TestPlayerID(t *testing.T) {
	src := `function on_enter(p) say(tostring(p.id) .. " " .. p.name) end`
	out, err := runParty(t, &recorder{}, src)
	if err != nil || strings.Join(out, ";") != "say:p7 Ada" {
		t.Errorf("RunHook with PlayerID: out = %q, err = %v", out, err)
	}
	// Without PlayerID (Run, and hooks of tools), player.id is nil.
	got, _, err := runHook(t, DefaultLimits, src)
	if err != nil || strings.Join(got, ";") != "say:nil p" {
		t.Errorf("Run: out = %q, err = %v", got, err)
	}
}

func TestRoomPlayersList(t *testing.T) {
	tests := []struct {
		name    string
		players []Player
		want    string
	}{
		{"nobody", nil, "say:0"},
		{"one", []Player{{"p1", "traveler"}}, "say:1 p1=traveler"},
		{"in order", []Player{{"p3", "c"}, {"p1", "a"}, {"p2", "b"}}, "say:3 p3=c p1=a p2=b"},
		{"odd names", []Player{{"p1", ""}, {"p2", "x y\tz"}}, "say:2 p1= p2=x y\tz"},
	}
	src := `function on_enter(p)
  local list = room.players()
  local parts = {tostring(#list)}
  for _, q in ipairs(list) do parts[#parts + 1] = q.id .. "=" .. q.name end
  say(table.concat(parts, " "))
end`
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runParty(t, &partyHost{players: tt.players}, src)
			if err != nil || strings.Join(out, ";") != tt.want {
				t.Errorf("out = %q, err = %v, want %q", out, err, tt.want)
			}
		})
	}
}

// Each call returns a new table: a script changing it changes nothing
// for the next call.
func TestRoomPlayersFreshTable(t *testing.T) {
	h := &partyHost{players: []Player{{"p1", "a"}}}
	out, err := runParty(t, h, `function on_enter(p)
  local a = room.players()
  a[1].name = "changed"
  a[2] = {id = "fake"}
  local b = room.players()
  say(#b .. " " .. b[1].name .. " " .. tostring(rawequal(a, b)))
end`)
	if err != nil || strings.Join(out, ";") != "say:1 a false" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	if h.players[0].Name != "a" {
		t.Errorf("the host's list changed: %+v", h.players)
	}
}

// A host without Party plays alone: room.players and room timers are
// script errors, player timers still work.
func TestPartyWithoutParty(t *testing.T) {
	for _, call := range []string{
		`room.players()`,
		`timer.start("g", 5, {room = true})`,
		`timer.start("g", 5, {room = true, show = "Turn"})`,
		`timer.cancel("g", {room = true})`,
		`timer.left("g", {room = true})`,
	} {
		t.Run(call, func(t *testing.T) {
			out, err := runParty(t, &recorder{}, `function on_enter() `+call+` end`)
			if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "no rooms shared by players") {
				t.Errorf("err = %v", err)
			}
			if len(out) != 0 {
				t.Errorf("reached the host: %q", out)
			}
		})
	}
	out, err := runParty(t, &recorder{}, `function on_enter()
  timer.start("g", 5, {room = false})
  say(tostring(timer.left("g", {room = false})) .. " " .. tostring(timer.cancel("g", {room = false})))
end`)
	if err != nil || strings.Join(out, ";") != "timer:g=5s|;say:5 true" {
		t.Errorf("room = false is the player's timer: out = %q, err = %v", out, err)
	}
}

// room.players and room timers need a player: the main chunk has none.
func TestPartyOutsideHook(t *testing.T) {
	for _, call := range []string{`room.players()`, `timer.start("g", 5, {room = true})`, `timer.cancel("g", {room = true})`, `timer.left("g", {room = true})`} {
		r := New(DefaultLimits)
		_, err := r.Load("main.lua", call)
		r.Close()
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "no player here") {
			t.Errorf("%s in the main chunk: err = %v", call, err)
		}
	}
}

func TestRoomTimerArguments(t *testing.T) {
	tests := []struct {
		name, call string
		want       string // what the host saw, or "" for a script error
	}{
		{"room", `timer.start("turn", 30, {room = true})`, "roomtimer:turn=30s|"},
		{"room shown", `timer.start("turn", 30, {room = true, show = "Mira's turn"})`, "roomtimer:turn=30s|Mira's turn"},
		{"room fraction", `timer.start("turn", 1.5, {room = true})`, "roomtimer:turn=1.5s|"},
		{"room false", `timer.start("turn", 30, {room = false, show = "Mine"})`, "timer:turn=30s|Mine"},
		{"room nil", `timer.start("turn", 30, {room = nil})`, "timer:turn=30s|"},
		{"room string", `timer.start("turn", 30, {room = "yes"})`, ""},
		{"room number", `timer.start("turn", 30, {room = 1})`, ""},
		{"room table", `timer.start("turn", 30, {room = {}})`, ""},
		{"room show empty", `timer.start("turn", 30, {room = true, show = ""})`, ""},
		{"room show two lines", `timer.start("turn", 30, {room = true, show = "a\nb"})`, ""},
		{"room show too long", `timer.start("turn", 30, {room = true, show = string.rep("x", 101)})`, ""},
		{"room show not a string", `timer.start("turn", 30, {room = true, show = 5})`, ""},
		{"room too short", `timer.start("turn", 0.5, {room = true})`, ""},
		{"room too long", `timer.start("turn", 86401, {room = true})`, ""},
		{"room bad name", `timer.start("Turn", 30, {room = true})`, ""},
		{"room empty name", `timer.start("", 30, {room = true})`, ""},
		{"cancel room", `timer.cancel("turn", {room = true})`, "roomstop:turn"},
		{"cancel room false", `timer.cancel("turn", {room = false})`, "-"},
		{"cancel nil options", `timer.cancel("turn", nil)`, "-"},
		{"cancel room string", `timer.cancel("turn", {room = "true"})`, ""},
		{"cancel options not a table", `timer.cancel("turn", true)`, ""},
		{"cancel bad name", `timer.cancel("a b", {room = true})`, ""},
		{"cancel ignores show", `timer.cancel("turn", {room = true, show = 5})`, "roomstop:turn"},
		{"left room string", `timer.left("turn", {room = 0})`, ""},
		{"left options not a table", `timer.left("turn", "room")`, ""},
		{"left ignores show", `timer.left("turn", {room = true, show = {}})`, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runParty(t, &partyHost{}, `function on_enter() `+tt.call+` end`)
			if tt.want == "" {
				if !errors.Is(err, ErrScript) {
					t.Errorf("err = %v, out = %q, want a script error", err, out)
				}
				if len(out) != 0 {
					t.Errorf("a refused call reached the host: %q", out)
				}
				return
			}
			want := []string{tt.want}
			if tt.want == "-" {
				want = nil
			}
			if err != nil || strings.Join(out, ";") != strings.Join(want, ";") {
				t.Errorf("out = %q, err = %v, want %q", out, err, want)
			}
		})
	}
}

// Room timers and player timers of one name are apart.
func TestRoomTimerCancelAndLeft(t *testing.T) {
	h := &partyHost{}
	out, err := runParty(t, h, `function on_enter()
  say(tostring(timer.left("g", {room = true})))
  timer.start("g", 90, {room = true})
  timer.start("g", 10)
  say(timer.left("g", {room = true}) .. " " .. timer.left("g"))
  say(tostring(timer.cancel("g", {room = true})) .. " " .. tostring(timer.cancel("g", {room = true})))
  say(tostring(timer.left("g", {room = true})) .. " " .. tostring(timer.left("g")))
end`)
	want := "say:nil;roomtimer:g=1m30s|;timer:g=10s|;say:90 10;roomstop:g;roomstop:g;say:true false;say:nil 10"
	if err != nil || strings.Join(out, ";") != want {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// A room timer whose deadline passed but has not fired has 0 seconds left.
func TestRoomTimerLeftNeverNegative(t *testing.T) {
	h := &partyHost{roomTimers: map[string]time.Duration{"late": -3 * time.Second}}
	out, err := runParty(t, h, `function on_enter() say(tostring(timer.left("late", {room = true}))) end`)
	if err != nil || strings.Join(out, ";") != "say:0" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// The host refusing a room timer is not an error: nil and the reason.
func TestRoomTimerStartRefused(t *testing.T) {
	out, err := runParty(t, &partyHost{refuse: true}, `function on_enter()
  local ok, why = timer.start("g", 5, {room = true})
  say(tostring(ok) .. " " .. tostring(why))
end`)
	if err != nil || strings.Join(out, ";") != "say:nil timer.start: room is full of timers" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// A shown room timer is output, like a shown player timer, so it counts
// toward MaxOutputs; a secret one is not.
func TestRoomTimerShownCountsAsOutput(t *testing.T) {
	for _, tt := range []struct {
		opts string
		ok   bool
	}{{`{room = true, show = "x"}`, false}, {`{room = true}`, true}} {
		_, err := runParty(t, &partyHost{}, `function on_enter()
  for i = 1, `+strconv.Itoa(MaxOutputs)+` do say("x") end
  timer.start("g", 5, `+tt.opts+`)
end`)
		if tt.ok != (err == nil) {
			t.Errorf("%s after %d outputs: err = %v", tt.opts, MaxOutputs, err)
		}
	}
}
