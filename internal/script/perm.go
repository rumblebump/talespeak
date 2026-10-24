package script

import (
	"regexp"
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// Permissions (T-0011): a pack names what a role may do with
// perm.define, and asks with player:can. The engine's own permissions
// belong to game masters in GM mode; a pack can neither define them again
// nor give them to players, so no pack lowers what the engine keeps for
// game masters (the edu floor, docs/accounts.md).
//
//	perm.define("cast.forbidden_spell", { gm = true, player = false })
//	if player:can("cast.forbidden_spell") then ... end

// Limits of a script's permissions.
const (
	// MaxPerms is how many permissions one script instance (its room
	// script and the modules it requires) defines.
	MaxPerms = 50
	// MaxPermBytes is the longest permission name.
	MaxPermBytes = 40
)

// EnginePerms are the engine's permissions: true for a game master in GM
// mode, false for everyone else, whatever a pack says.
var EnginePerms = []string{"chat.view_all", "gm.tools", "item.spawn", "player.mute", "timer.edit", "world.pause"}

var permName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

// perm is who has one permission a pack defined.
type perm struct{ gm, player bool }

func isEnginePerm(name string) bool {
	for _, p := range EnginePerms {
		if p == name {
			return true
		}
	}
	return false
}

// checkPermName raises a Lua error for a name that cannot name a
// permission, without formatting more of it than a valid one could be.
func checkPermName(L *lua.LState, n int, name string) {
	if len(name) > MaxPermBytes || !permName.MatchString(name) {
		L.ArgError(n, "permission names are 1 to "+strconv.Itoa(MaxPermBytes)+" of a-z, 0-9, _, . and -, got "+strconv.Quote(truncate(name, MaxPermBytes)))
	}
}

// permDefine is perm.define(name, roles): roles is {gm = bool, player =
// bool}, both optional; without it only game masters have it. Defining a
// name again with other roles is an error, the same roles are not (every
// player runs the top level of their own instance).
func (r *Runtime) permDefine(L *lua.LState) int {
	name := L.CheckString(1)
	checkPermName(L, 1, name)
	if isEnginePerm(name) {
		L.ArgError(1, strconv.Quote(name)+" is the engine's permission: a pack cannot define it")
	}
	p := perm{gm: true}
	if v := L.Get(2); v != lua.LNil {
		t, ok := v.(*lua.LTable)
		if !ok {
			L.ArgError(2, "roles must be a table such as {gm = true, player = false}")
		}
		var bad string
		t.ForEach(func(k, v lua.LValue) {
			key, ok := k.(lua.LString)
			b, isBool := v.(lua.LBool)
			switch {
			case !ok || (key != "gm" && key != "player") || !isBool:
				bad = "roles takes only gm and player, each true or false"
			case key == "gm":
				p.gm = bool(b)
			default:
				p.player = bool(b)
			}
		})
		if bad != "" {
			L.ArgError(2, bad)
		}
	}
	st := r.curState(L, "perm.define")
	if old, ok := st.perms[name]; ok {
		if old != p {
			L.RaiseError("permission %q is defined twice with other roles", name)
		}
		return 0
	}
	if len(st.perms) >= MaxPerms {
		L.RaiseError("a script defines at most %d permissions", MaxPerms)
	}
	if st.perms == nil {
		st.perms = map[string]perm{}
	}
	st.perms[name] = p
	return 0
}

// curState is the envState of the instance whose hook runs now.
func (r *Runtime) curState(L *lua.LState, what string) *envState {
	if r.cur == nil || r.cur.thread == nil {
		L.RaiseError("%s works only while a script runs", what)
	}
	st := r.envs[r.cur.thread.env]
	if st == nil {
		L.RaiseError("%s: the script instance was released", what)
	}
	return st
}

// canFunc returns player:can for a player in GM mode (gm) or not. The
// role is the function's, not the table's gm field, so a script that
// changes player.gm changes nothing.
func (r *Runtime) canFunc(gm bool) *lua.LFunction {
	return r.L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(2) // player:can(name): the table is argument 1
		checkPermName(L, 2, name)
		if isEnginePerm(name) {
			L.Push(lua.LBool(gm))
			return 1
		}
		p, ok := r.curState(L, "can").perms[name]
		if !ok {
			L.ArgError(2, "no permission "+strconv.Quote(name)+": perm.define it first")
		}
		L.Push(lua.LBool((gm && p.gm) || (!gm && p.player)))
		return 1
	})
}

// can is player:can for gm.
func (r *Runtime) can(gm bool) *lua.LFunction {
	if gm {
		return r.canGM
	}
	return r.canPlayer
}
