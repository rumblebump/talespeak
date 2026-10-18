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

// timer.start(name, seconds [, {show = label}]) starts the player's timer
// name, replacing a running one of that name. Once it runs out, the engine
// calls on_timer(player, name) in the player's current room. show is a
// label the player sees with a countdown; without it the timer is secret.
// It returns true, or nil and the reason when the player has MaxTimers
// running.
func (r *Runtime) timerStart(L *lua.LState) int {
	name := timerName(L)
	d, ok := seconds(L.Get(2))
	if !ok {
		L.ArgError(2, "a timer runs 1 to 86400 seconds")
	}
	show := ""
	if L.GetTop() >= 3 && L.Get(3) != lua.LNil {
		switch v := L.CheckTable(3).RawGetString("show").(type) {
		case *lua.LNilType:
		case lua.LString:
			if !validLabel(string(v)) {
				L.ArgError(3, "show must be one line of 1 to 100 bytes")
			}
			show = string(v)
		default:
			L.ArgError(3, "show must be a string")
		}
	}
	host := r.timerHost(L)
	if show != "" {
		host = r.output(L)
	}
	if err := host.StartTimer(name, d, show); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("timer.start: " + err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

// timer.cancel(name) stops the player's timer name; it returns whether one
// was running.
func (r *Runtime) timerCancel(L *lua.LState) int {
	name := timerName(L)
	L.Push(lua.LBool(r.timerHost(L).StopTimer(name)))
	return 1
}

// timer.left(name) returns the seconds until the player's timer name runs
// out, or nil when it is not running.
func (r *Runtime) timerLeft(L *lua.LState) int {
	name := timerName(L)
	d, ok := r.timerHost(L).TimerLeft(name)
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	L.Push(lua.LNumber(max(d, 0).Seconds()))
	return 1
}
