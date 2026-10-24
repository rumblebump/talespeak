package script

import (
	"strings"
	"testing"
)

// permScript defines permissions at its top level, as packs do, and says
// what the player may do.
const permScript = `
perm.define("cast.forbidden_spell")
perm.define("open.door", { gm = true, player = true })
perm.define("gm.never", { gm = false, player = true })
perm.define("open.door", { player = true, gm = true }) -- the same again is fine
function on_input(p, what)
  if what == "fake" then p.gm = not p.gm end
  local out = {}
  for _, name in ipairs({"cast.forbidden_spell", "open.door", "gm.never", "world.pause", "gm.tools"}) do
    out[#out + 1] = name .. "=" .. tostring(p:can(name))
  end
  say(table.concat(out, " "))
end`

func TestPermissions(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env, err := r.Load("perm.lua", permScript)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		gm   bool
		arg  string
		want string
	}{
		{true, "", "say:cast.forbidden_spell=true open.door=true gm.never=false world.pause=true gm.tools=true"},
		{false, "", "say:cast.forbidden_spell=false open.door=true gm.never=true world.pause=false gm.tools=false"},
		// Changing p.gm changes nothing: the role belongs to can.
		{false, "fake", "say:cast.forbidden_spell=false open.door=true gm.never=true world.pause=false gm.tools=false"},
		{true, "fake", "say:cast.forbidden_spell=true open.door=true gm.never=false world.pause=true gm.tools=true"},
	} {
		h := &recorder{}
		if _, err := r.RunHook(h, env, Hook{Name: "on_input", Player: "p", GM: tt.gm, Args: []string{tt.arg}}); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(h.out, "|"); got != tt.want {
			t.Errorf("gm=%v %q:\n got %s\nwant %s", tt.gm, tt.arg, got, tt.want)
		}
	}
}

// The other players of room.players and world.players can be asked too.
func TestPermissionsOfOthers(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env, err := r.Load("perm.lua", `
perm.define("kick")
function on_input(p)
  local out = {}
  for _, o in ipairs(room.players()) do out[#out + 1] = o.name .. "=" .. tostring(o:can("kick")) end
  say(table.concat(out, " "))
end`)
	if err != nil {
		t.Fatal(err)
	}
	h := &partyHost{players: []Player{{ID: "p1", Name: "ann", GM: true}, {ID: "p2", Name: "bob"}}}
	if _, err := r.RunHook(h, env, Hook{Name: "on_input", Player: "ann"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:ann=true bob=false" {
		t.Errorf("got %s", got)
	}
}

func TestPermissionErrors(t *testing.T) {
	for _, tt := range []struct{ src, want string }{
		{`perm.define("world.pause", {player = true})`, "engine's permission"},
		{`perm.define("chat.view_all")`, "engine's permission"},
		{`perm.define("Bad Name")`, "permission names are"},
		{`perm.define("")`, "permission names are"},
		{`perm.define(string.rep("a", 41))`, "permission names are"},
		{`perm.define("x", "gm")`, "roles must be a table"},
		{`perm.define("x", {admin = true})`, "roles takes only gm and player"},
		{`perm.define("x", {gm = "yes"})`, "roles takes only gm and player"},
		{`perm.define("x") perm.define("x", {player = true})`, "defined twice"},
		{`for i = 1, 51 do perm.define("p" .. i) end`, "at most 50"},
		{`function on_input(p) return p:can("never.defined") end`, "perm.define it first"},
		{`function on_input(p) return p:can("Bad Name") end`, "permission names are"},
		{`function on_input(p) return p.can({}) end`, "string expected"},
	} {
		r := New(DefaultLimits)
		env, err := r.Load("bad.lua", tt.src)
		if err == nil {
			_, err = r.RunHook(&recorder{}, env, Hook{Name: "on_input", Player: "p"})
		}
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.src, err, tt.want)
		}
		r.Close()
	}
}

// Permissions belong to one instance: another player's instance, or
// another room's script, defines its own.
func TestPermissionsPerInstance(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	a, err := r.Load("a.lua", `perm.define("x", {player = true})`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Load("b.lua", `perm.define("x", {player = false}) function on_input(p) say(tostring(p:can("x"))) end`)
	if err != nil {
		t.Fatal(err)
	}
	_ = a
	h := &recorder{}
	if _, err := r.RunHook(h, b, Hook{Name: "on_input", Player: "p"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:false" {
		t.Errorf("got %s", got)
	}
}
