package script

import (
	"regexp"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// removedBase are base functions a pack never gets: they load code or
// files, reach the global environment of other functions, or inspect the VM.
var removedBase = []string{
	"collectgarbage", "dofile", "getfenv", "load", "loadfile", "loadstring",
	"module", "newproxy", "print", "require", "setfenv", "_printregs",
}

// patternFuncs run gopher-lua's backtracking pattern matcher in Go, where
// the time limit cannot interrupt them, and a crafted pattern takes
// exponential time. They stay out until a step-limited matcher exists.
var patternFuncs = []string{"gmatch", "gsub", "match"}

// openSandbox loads base, table, string and math (no io, os, debug,
// package, channel or coroutine) and the Talespeak capabilities, and
// returns the table every Env reads its globals from.
func (r *Runtime) openSandbox() *lua.LTable {
	L := r.L
	for _, lib := range []struct {
		name string
		open lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		L.Push(L.NewFunction(lib.open))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}
	g := L.G.Global
	for _, name := range removedBase {
		g.RawSetString(name, lua.LNil)
	}

	g.RawSetString("unpack", L.NewFunction(limitResults(g.RawGetString("unpack").(*lua.LFunction), "unpack", 1)))

	str := g.RawGetString("string").(*lua.LTable)
	str.RawSetString("byte", L.NewFunction(limitResults(str.RawGetString("byte").(*lua.LFunction), "string.byte", 2)))
	for _, name := range patternFuncs {
		name := name
		str.RawSetString(name, L.NewFunction(func(L *lua.LState) int {
			L.RaiseError("string.%s is not available: Lua patterns are off in the sandbox (docs/lua-sandbox.md)", name)
			return 0
		}))
	}
	str.RawSetString("find", L.NewFunction(strFind(str.RawGetString("find").(*lua.LFunction))))
	str.RawSetString("rep", L.NewFunction(strRep))
	str.RawSetString("format", L.NewFunction(strFormat(str.RawGetString("format").(*lua.LFunction))))
	tbl := g.RawGetString("table").(*lua.LTable)
	tbl.RawSetString("concat", L.NewFunction(tableConcat(tbl.RawGetString("concat").(*lua.LFunction))))

	g.RawSetString("say", L.NewFunction(r.say))
	room := L.NewTable()
	room.RawSetString("describe", L.NewFunction(r.describe))
	g.RawSetString("room", room)
	ui := L.NewTable()
	ui.RawSetString("dialog", L.NewFunction(r.dialog))
	g.RawSetString("ui", ui)
	return g
}

// MaxResults is how many values unpack and string.byte may return. Past
// the Lua stack limit gopher-lua fails only after seconds of work.
const MaxResults = 8000

// limitResults wraps f(x, i, j), which returns one value per index from i
// to j, so that it refuses ranges longer than MaxResults. first is the
// default i.
func limitResults(f *lua.LFunction, name string, first int) lua.LGFunction {
	return func(L *lua.LState) int {
		i := L.OptInt(2, first)
		var j int
		switch v := L.Get(1).(type) {
		case *lua.LTable:
			j = L.OptInt(3, v.Len())
		case lua.LString:
			j = L.OptInt(3, i)
			if j < 0 {
				j += len(v) + 1
			}
			if i < 0 {
				i += len(v) + 1
			}
		}
		if j-i >= MaxResults {
			L.RaiseError("too many results from %s (more than %d)", name, MaxResults)
		}
		return f.GFunction(L)
	}
}

// strFind allows plain search only: string.find(s, text, init, true).
func strFind(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if !lua.LVAsBool(L.Get(4)) {
			L.RaiseError("string.find needs plain = true: Lua patterns are off in the sandbox (docs/lua-sandbox.md)")
		}
		return orig.GFunction(L)
	}
}

func strRep(L *lua.LState) int {
	s := L.CheckString(1)
	n := L.CheckInt(2)
	if n <= 0 || s == "" {
		L.Push(lua.LString(""))
		return 1
	}
	if n > MaxStringBytes/len(s) {
		L.RaiseError("string.rep result larger than %d bytes", MaxStringBytes)
	}
	L.Push(lua.LString(strings.Repeat(s, n)))
	return 1
}

// longFormat finds a width or precision of three or more digits. Lua itself
// rejects them; Go's fmt would pad to a million characters.
var longFormat = regexp.MustCompile(`%[-+ #0]*(\d{3,}|\d*\.\d{3,})`)

func strFormat(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if longFormat.MatchString(L.CheckString(1)) {
			L.RaiseError("invalid format (width or precision too long)")
		}
		n := orig.GFunction(L)
		if s, ok := L.Get(-1).(lua.LString); ok && len(s) > MaxStringBytes {
			L.RaiseError("string.format result larger than %d bytes", MaxStringBytes)
		}
		return n
	}
}

// tableConcat checks the result size before building it: a table can hold
// the same large string many times.
func tableConcat(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		t := L.CheckTable(1)
		sep := len(L.OptString(2, ""))
		i, j := L.OptInt(3, 1), L.OptInt(4, t.Len())
		total := 0
		for k := max(i, 1); k <= min(j, t.Len()); k++ {
			if s, ok := t.RawGetInt(k).(lua.LString); ok {
				total += len(s)
			} else {
				total += 24 // a number
			}
			total += sep
			if total > MaxStringBytes {
				L.RaiseError("table.concat result larger than %d bytes", MaxStringBytes)
			}
		}
		return orig.GFunction(L)
	}
}

// output counts say and room.describe calls and returns the host, or raises
// an error when called outside a hook or too often.
func (r *Runtime) output(L *lua.LState) Host {
	if r.cur == nil || r.cur.host == nil {
		L.RaiseError("no player here: call this from a hook such as on_enter")
	}
	r.cur.outputs++
	if r.cur.outputs > MaxOutputs {
		L.RaiseError("more than %d outputs in one call", MaxOutputs)
	}
	return r.cur.host
}

// say(text) sends a line of text to the player.
func (r *Runtime) say(L *lua.LState) int {
	text := L.CheckString(1)
	if len(text) > MaxTextBytes {
		L.ArgError(1, "text longer than 4000 bytes")
	}
	r.output(L).Say(text)
	return 0
}

// room.describe() sends the current room's title and description.
func (r *Runtime) describe(L *lua.LState) int {
	r.output(L).DescribeRoom()
	return 0
}

// ui.dialog(title, options) shows a choice and pauses the hook until the
// player picks one. It returns the option's number (1-based) and its label.
func (r *Runtime) dialog(L *lua.LState) int {
	title := L.CheckString(1)
	opts := L.CheckTable(2)
	if len(title) > MaxTitleBytes {
		L.ArgError(1, "title longer than 200 bytes")
	}
	n := opts.Len()
	if n < 1 || n > MaxDialogOptions {
		L.ArgError(2, "a dialog needs 1 to 9 options")
	}
	d := Dialog{Title: title, Options: make([]string, n)}
	for i := range n {
		s, ok := opts.RawGetInt(i + 1).(lua.LString)
		if !ok || s == "" || len(s) > MaxOptionBytes {
			L.ArgError(2, "options must be strings of 1 to 200 bytes")
		}
		d.Options[i] = string(s)
	}
	r.output(L)
	if L.Parent == nil {
		L.RaiseError("ui.dialog can only wait inside a hook")
	}
	r.cur.dialog = &d
	return L.Yield()
}
