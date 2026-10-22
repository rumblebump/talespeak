package script

import (
	"strings"
	"testing"
)

// T-0036 edge cases: world.players with nobody, odd names and rooms, a
// fresh table each call, a read-only library, and player.gm without a
// Party.

func TestWorldPlayersList(t *testing.T) {
	tests := []struct {
		name  string
		world []Player
		want  string
	}{
		{"nobody", nil, "say:0"},
		{"in order, rooms and gm", []Player{{ID: "p3", Name: "c", Room: "b"}, {ID: "p1", Name: "a", Room: "a", GM: true}}, "say:2 p3=c@b:false p1=a@a:true"},
		{"odd names and an empty room", []Player{{ID: "p1", Name: "x y\tz", Room: ""}}, "say:1 p1=x y\tz@:false"},
	}
	src := `function on_enter(p)
  local list = world.players()
  local parts = {tostring(#list)}
  for _, q in ipairs(list) do parts[#parts + 1] = q.id .. "=" .. q.name .. "@" .. q.room .. ":" .. tostring(q.gm) end
  say(table.concat(parts, " "))
end`
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runParty(t, &partyHost{world: tt.world}, src)
			if err != nil || strings.Join(out, ";") != tt.want {
				t.Errorf("out = %q, err = %v, want %q", out, err, tt.want)
			}
		})
	}
}

// Each call returns a new table, and a script changing it changes
// nothing for the host or the next call.
func TestWorldPlayersFreshTable(t *testing.T) {
	h := &partyHost{world: []Player{{ID: "p1", Name: "a", Room: "a"}}}
	out, err := runParty(t, h, `function on_enter(p)
  local a = world.players()
  a[1].gm = true
  a[1].room = "x"
  a[2] = {id = "fake"}
  local b = world.players()
  say(#b .. " " .. b[1].room .. " " .. tostring(b[1].gm) .. " " .. tostring(rawequal(a, b)))
end`)
	if err != nil || strings.Join(out, ";") != "say:1 a false false" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	if h.world[0].GM || h.world[0].Room != "a" {
		t.Errorf("the host's list changed: %+v", h.world)
	}
}

// The world library is read-only: a script cannot replace world.players
// for the other hooks of its room.
func TestWorldLibraryReadOnly(t *testing.T) {
	for _, src := range []string{
		`world.players = function() return {} end`,
		`world.extra = 1`,
		`world = nil; say(type(world))`,
	} {
		h := &partyHost{world: []Player{{ID: "p1", Name: "a", Room: "a"}}}
		out, err := runParty(t, h, "function on_enter(p)\n"+src+"\nend")
		if strings.HasPrefix(src, "world = nil") {
			// Clearing the global leaves the library behind it.
			if err != nil || strings.Join(out, ";") != "say:table" {
				t.Errorf("%s: out = %q, err = %v", src, out, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s: err = %v, want read-only", src, err)
		}
	}
}

// player.gm is false unless the hook says so, also without a Party, and
// a script cannot make it stick across hooks.
func TestPlayerGMDefaultFalse(t *testing.T) {
	src := `function on_enter(p) say(tostring(p.gm)) p.gm = true end`
	for range 2 {
		out, err := runParty(t, &recorder{}, src)
		if err != nil || strings.Join(out, ";") != "say:false" {
			t.Errorf("out = %q, err = %v", out, err)
		}
	}
}
