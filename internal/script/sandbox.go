package script

import (
	"math"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// The sandbox is built from allowlists: a function that gopher-lua adds in a
// later version stays out until it is reviewed and listed here.

// baseFuncs are the base library functions a script gets as they are.
var baseFuncs = []string{
	"assert", "error", "getmetatable", "ipairs", "next", "pairs", "pcall",
	"rawequal", "rawget", "select", "setmetatable", "tonumber", "tostring",
	"type", "xpcall",
}

// stringFuncs are the string functions a script gets as they are. find,
// sub, rep, format and byte are wrapped below.
var stringFuncs = []string{"char", "len", "lower", "reverse", "upper"}

// patternFuncs run gopher-lua's backtracking pattern matcher in Go, where
// the time limit cannot interrupt them, and a crafted pattern takes
// exponential time. They raise an error until a step-limited matcher exists.
var patternFuncs = []string{"gfind", "gmatch", "gsub", "match"}

// tableFuncs are the table functions a script gets as they are; concat is
// wrapped.
var tableFuncs = []string{"getn", "insert", "maxn", "remove"}

// MaxSortLength is the longest table table.sort accepts. Sorting without a
// comparator runs entirely in Go, where the time limit cannot stop it;
// 20000 entries sort in a few milliseconds.
const MaxSortLength = 20000

// lockedMeta is what getmetatable returns for a protected table or string.
const lockedMeta = lua.LString("locked")

// concatName is the function every .. in a script is compiled into; see
// rewriteConcat.
const concatName = "__concat"

// openSandbox builds the shared globals every Env reads from: allowlisted
// parts of base, string, table and math, plus the Talespeak capabilities.
// Library tables are read-only proxies.
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
	full := L.G.Global
	fn := func(t *lua.LTable, name string) *lua.LFunction { return t.RawGetString(name).(*lua.LFunction) }

	lib := L.NewTable()
	for _, name := range baseFuncs {
		lib.RawSetString(name, fn(full, name))
	}
	lib.RawSetString("_VERSION", full.RawGetString("_VERSION"))
	lib.RawSetString("unpack", L.NewFunction(limitResults(fn(full, "unpack"), "unpack", 1)))
	lib.RawSetString("rawset", L.NewFunction(r.rawset(fn(full, "rawset"))))
	lib.RawSetString(concatName, L.NewFunction(concat))

	fullStr := full.RawGetString("string").(*lua.LTable)
	str := L.NewTable()
	for _, name := range stringFuncs {
		str.RawSetString(name, fn(fullStr, name))
	}
	for _, name := range patternFuncs {
		name := name
		str.RawSetString(name, L.NewFunction(func(L *lua.LState) int {
			L.RaiseError("string.%s is not available: Lua patterns are off in the sandbox (docs/lua-sandbox.md)", name)
			return 0
		}))
	}
	str.RawSetString("find", L.NewFunction(strFind(fn(fullStr, "find"))))
	str.RawSetString("sub", L.NewFunction(strSub(fn(fullStr, "sub"))))
	str.RawSetString("byte", L.NewFunction(limitResults(fn(fullStr, "byte"), "string.byte", 1)))
	str.RawSetString("rep", L.NewFunction(strRep))
	str.RawSetString("format", L.NewFunction(strFormat))
	// Methods on strings ("x"):rep(3) look up the same table.
	if mt, ok := L.GetMetatable(lua.LString("")).(*lua.LTable); ok {
		mt.RawSetString("__index", str)
		mt.RawSetString("__metatable", lockedMeta)
	}
	lib.RawSetString("string", r.readOnly(str))

	fullTbl := full.RawGetString("table").(*lua.LTable)
	tbl := L.NewTable()
	for _, name := range tableFuncs {
		tbl.RawSetString(name, fn(fullTbl, name))
	}
	tbl.RawSetString("concat", L.NewFunction(tableConcat(fn(fullTbl, "concat"))))
	tbl.RawSetString("sort", L.NewFunction(tableSort(fn(fullTbl, "sort"))))
	lib.RawSetString("table", r.readOnly(tbl))

	lib.RawSetString("math", r.readOnly(full.RawGetString("math").(*lua.LTable)))

	lib.RawSetString("say", L.NewFunction(r.say))
	room := L.NewTable()
	room.RawSetString("describe", L.NewFunction(r.describe))
	lib.RawSetString("room", r.readOnly(room))
	ui := L.NewTable()
	ui.RawSetString("dialog", L.NewFunction(r.dialog))
	lib.RawSetString("ui", r.readOnly(ui))

	// Nothing outside lib is reachable from a script, so the full library
	// tables stay in L.G.Global only for gopher-lua's own use.
	return lib
}

// newEnv returns a script's globals: its own table, reading the shared
// globals through a protected metatable. _G is the script's own table.
func (r *Runtime) newEnv() *lua.LTable {
	env := r.L.NewTable()
	meta := r.L.NewTable()
	meta.RawSetString("__index", r.lib)
	meta.RawSetString("__metatable", lockedMeta)
	env.Metatable = meta
	env.RawSetString("_G", env)
	return env
}

// readOnly returns an empty proxy that reads from t and refuses writes.
// The proxy has no keys of its own, so pairs and rawget see nothing.
func (r *Runtime) readOnly(t *lua.LTable) *lua.LTable {
	L := r.L
	proxy := L.NewTable()
	meta := L.NewTable()
	meta.RawSetString("__index", t)
	meta.RawSetString("__newindex", L.NewFunction(func(L *lua.LState) int {
		L.RaiseError("this library is read-only")
		return 0
	}))
	meta.RawSetString("__metatable", lockedMeta)
	proxy.Metatable = meta
	r.locked[proxy] = true
	return proxy
}

// rawset refuses the read-only proxies, which would otherwise take a
// shadowing key for every script in the world.
func (r *Runtime) rawset(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if r.locked[L.CheckTable(1)] {
			L.RaiseError("this library is read-only")
		}
		return orig.GFunction(L)
	}
}

// intArg reads argument n as an integer within ±2^31, or def when absent.
// Larger values, NaN and infinities are errors, so later arithmetic on the
// result cannot overflow.
func intArg(L *lua.LState, n, def int) int {
	v := L.Get(n)
	if v == lua.LNil {
		return def
	}
	f := float64(L.CheckNumber(n))
	if math.IsNaN(f) || f < math.MinInt32 || f > math.MaxInt32 {
		L.ArgError(n, "number out of range")
	}
	return int(f)
}

// MaxResults is how many values unpack and string.byte may return. Past
// the Lua stack limit gopher-lua fails only after seconds of work.
const MaxResults = 8000

// limitResults wraps f(x, i, j), which returns one value per index from i
// to j, so that it refuses ranges longer than MaxResults. first is the
// default i; j defaults to the length of a table, or to i for a string.
func limitResults(f *lua.LFunction, name string, first int) lua.LGFunction {
	return func(L *lua.LState) int {
		i := intArg(L, 2, first)
		var j int
		switch v := L.Get(1).(type) {
		case *lua.LTable:
			j = intArg(L, 3, v.Len())
		case lua.LString:
			j = intArg(L, 3, i)
			if i < 0 {
				i += len(v) + 1
			}
			if j < 0 {
				j += len(v) + 1
			}
		}
		if j >= i && j-i >= MaxResults {
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
		intArg(L, 3, 1)
		return orig.GFunction(L)
	}
}

// strSub copies the substring: a Go substring would keep its whole parent
// string alive while the state measures only the slice.
func strSub(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		n := orig.GFunction(L)
		if s, ok := L.Get(-1).(lua.LString); ok {
			L.Pop(1)
			L.Push(lua.LString(strings.Clone(string(s))))
		}
		return n
	}
}

func strRep(L *lua.LState) int {
	s := L.CheckString(1)
	n := float64(L.CheckNumber(2))
	if !(n >= 1) || s == "" { // also NaN
		L.Push(lua.LString(""))
		return 1
	}
	if n > float64(MaxStringBytes/len(s)) {
		L.RaiseError("string.rep result larger than %d bytes", MaxStringBytes)
	}
	L.Push(lua.LString(strings.Repeat(s, int(n))))
	return 1
}

// concat implements .. for scripts: strings and numbers only, and never a
// result over MaxStringBytes. The check comes before the copy, so one
// a .. b .. c cannot allocate gigabytes.
func concat(L *lua.LState) int {
	n := L.GetTop()
	total := 0
	for i := 1; i <= n; i++ {
		switch v := L.Get(i).(type) {
		case lua.LString:
			total += len(v)
		case lua.LNumber:
			total += len(v.String())
		default:
			L.RaiseError("attempt to concatenate a %s value", v.Type().String())
		}
		if total > MaxStringBytes {
			L.RaiseError("string longer than %d bytes", MaxStringBytes)
		}
	}
	var b strings.Builder
	b.Grow(total)
	for i := 1; i <= n; i++ {
		b.WriteString(L.Get(i).String())
	}
	L.Push(lua.LString(b.String()))
	return 1
}

// tableConcat checks the result size before building it: a table can hold
// the same large string many times.
func tableConcat(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		t := L.CheckTable(1)
		sep := len(L.OptString(2, ""))
		i, j := intArg(L, 3, 1), intArg(L, 4, t.Len())
		total := 0
		for k := max(i, 1); k <= min(j, t.Len()); k++ {
			switch v := t.RawGetInt(k).(type) {
			case lua.LString:
				total += len(v)
			case lua.LNumber:
				total += len(v.String())
			}
			total += sep
			if total > MaxStringBytes {
				L.RaiseError("table.concat result larger than %d bytes", MaxStringBytes)
			}
		}
		return orig.GFunction(L)
	}
}

func tableSort(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if L.CheckTable(1).Len() > MaxSortLength {
			L.RaiseError("table.sort: more than %d entries", MaxSortLength)
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

// ui.dialog(title, options) shows a choice and waits until the player picks
// one. It returns the option's number (1-based) and its label.
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
	if r.cur == nil || r.cur.host == nil {
		L.RaiseError("no player here: call ui.dialog from a hook such as on_enter")
	}
	choice, label := r.pause(L, &d)
	L.Push(lua.LNumber(choice))
	L.Push(lua.LString(label))
	return 2
}
