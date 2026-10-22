package script

import (
	"math"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Limits of timers and question timeouts. Timers last for the session for
// now; T-0012 keeps them in the database.
const (
	MaxTimers       = 10 // running timers per player
	MinTimerSeconds = 1
	MaxTimerSeconds = 24 * 60 * 60
)

// seconds reads v as a duration of MinTimerSeconds to MaxTimerSeconds
// seconds, fractions allowed; ok is false for anything else.
func seconds(v lua.LValue) (time.Duration, bool) {
	n, isNum := v.(lua.LNumber)
	f := float64(n)
	if !isNum || math.IsNaN(f) || f < MinTimerSeconds || f > MaxTimerSeconds {
		return 0, false
	}
	return time.Duration(f * float64(time.Second)), true
}

// timeoutArg reads the optional timeout field of a question's options at
// argument n: 0 when it is nil.
func timeoutArg(L *lua.LState, t *lua.LTable, n int) time.Duration {
	v := t.RawGetString("timeout")
	if v == lua.LNil {
		return 0
	}
	d, ok := seconds(v)
	if !ok {
		L.ArgError(n, "timeout must be 1 to 86400 seconds")
	}
	return d
}

// timerName reads a timer's name at argument 1: like an item id.
func timerName(L *lua.LState) string {
	name := L.CheckString(1)
	if !ValidItemID(name) {
		L.ArgError(1, "a timer's name is 1 to 64 of a-z, 0-9, _ and -")
	}
	return name
}

// timerHost is the host of the running hook, or an error outside one.
func (r *Runtime) timerHost(L *lua.LState) Host {
	if r.cur == nil || r.cur.host == nil || r.cur.thread == nil {
		L.RaiseError("no player here: call timer functions from a hook such as on_enter")
	}
	return r.cur.host
}

// timerOpts reads the optional options table of a timer call at argument
// n: room, and for timer.start show.
func timerOpts(L *lua.LState, n int, start bool) (room bool, show string) {
	if L.GetTop() < n || L.Get(n) == lua.LNil {
		return false, ""
	}
	opts := L.CheckTable(n)
	switch v := opts.RawGetString("room").(type) {
	case *lua.LNilType:
	case lua.LBool:
		room = bool(v)
	default:
		L.ArgError(n, "room must be a boolean")
	}
	if !start {
		return room, ""
	}
	switch v := opts.RawGetString("show").(type) {
	case *lua.LNilType:
	case lua.LString:
		if !validLabel(string(v)) {
			L.ArgError(n, "show must be one line of 1 to 100 bytes")
		}
		show = string(v)
	default:
		L.ArgError(n, "show must be a string")
	}
	return room, show
}

// party is the running hook's Party, or an error when its host has none.
func (r *Runtime) party(L *lua.LState) Party {
	p, ok := r.timerHost(L).(Party)
	if !ok {
		L.RaiseError("this world has no rooms shared by players")
	}
	return p
}

// timer.start(name, seconds [, {show = label, room = true}]) starts the
// player's timer name, replacing a running one of that name. Once it runs
// out, the engine calls on_timer(player, name) in the player's current
// room. show is a label the player sees with a countdown; without it the
// timer is secret. With room = true it is the room's timer instead, shared
// by the players in it (see Party). It returns true, or nil and the reason
// when MaxTimers run already.
func (r *Runtime) timerStart(L *lua.LState) int {
	name := timerName(L)
	d, ok := seconds(L.Get(2))
	if !ok {
		L.ArgError(2, "a timer runs 1 to 86400 seconds")
	}
	room, show := timerOpts(L, 3, true)
	host := r.timerHost(L)
	if show != "" {
		host = r.output(L)
	}
	start := host.StartTimer
	if room {
		start = r.party(L).StartRoomTimer
	}
	if err := start(name, d, show); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("timer.start: " + err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

// timer.cancel(name [, {room = true}]) stops the player's (or the room's)
// timer name; it returns whether one was running.
func (r *Runtime) timerCancel(L *lua.LState) int {
	name := timerName(L)
	room, _ := timerOpts(L, 2, false)
	stop := r.timerHost(L).StopTimer
	if room {
		stop = r.party(L).StopRoomTimer
	}
	L.Push(lua.LBool(stop(name)))
	return 1
}

// timer.left(name [, {room = true}]) returns the seconds until the
// player's (or the room's) timer name runs out, or nil when it is not
// running.
func (r *Runtime) timerLeft(L *lua.LState) int {
	name := timerName(L)
	room, _ := timerOpts(L, 2, false)
	left := r.timerHost(L).TimerLeft
	if room {
		left = r.party(L).RoomTimerLeft
	}
	d, ok := left(name)
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	L.Push(lua.LNumber(max(d, 0).Seconds()))
	return 1
}

// room.players() lists the players in the caller's room, the caller
// included, in the order they came in: {{id = "p1", name = "traveler",
// gm = false}, ...}.
func (r *Runtime) roomPlayers(L *lua.LState) int {
	L.Push(playerList(L, r.party(L).Players(), false))
	return 1
}

// world.players() lists the players online in the world, in every room,
// the caller included, in the order they joined: {{id = "p1", name =
// "traveler", room = "cell", gm = false}, ...}.
func (r *Runtime) worldPlayers(L *lua.LState) int {
	L.Push(playerList(L, r.party(L).WorldPlayers(), true))
	return 1
}

// playerList is list as a Lua list of player tables, with each player's
// room when withRoom is set.
func playerList(L *lua.LState, list []Player, withRoom bool) *lua.LTable {
	t := L.CreateTable(len(list), 0)
	for i, p := range list {
		e := L.CreateTable(0, 4)
		e.RawSetString("id", lua.LString(p.ID))
		e.RawSetString("name", lua.LString(p.Name))
		if withRoom {
			e.RawSetString("room", lua.LString(p.Room))
		}
		e.RawSetString("gm", lua.LBool(p.GM))
		t.RawSetInt(i+1, e)
	}
	return t
}
