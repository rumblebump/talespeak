package script

import (
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

// tableFuncs are the table functions a script gets as they are; concat,
// sort and insert are wrapped.
var tableFuncs = []string{"getn", "maxn", "remove"}

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
	tbl.RawSetString("insert", L.NewFunction(tableInsert(fn(fullTbl, "insert"))))
	lib.RawSetString("table", r.readOnly(tbl))

	lib.RawSetString("math", r.readOnly(openMath(L, full.RawGetString("math").(*lua.LTable))))

	lib.RawSetString("require", L.NewFunction(r.require))
	lib.RawSetString("say", L.NewFunction(r.say))
	room := L.NewTable()
	room.RawSetString("describe", L.NewFunction(r.describe))
	room.RawSetString("go", L.NewFunction(r.goRoom))
	room.RawSetString("players", L.NewFunction(r.roomPlayers))
	room.RawSetString("event", L.NewFunction(r.roomEvent))
	lib.RawSetString("room", r.readOnly(room))
	world := L.NewTable()
	world.RawSetString("players", L.NewFunction(r.worldPlayers))
	world.RawSetString("paused", L.NewFunction(r.worldPaused))
	world.RawSetString("time", L.NewFunction(r.worldTime))
	lib.RawSetString("world", r.readOnly(world))
	perm := L.NewTable()
	perm.RawSetString("define", L.NewFunction(r.permDefine))
	lib.RawSetString("perm", r.readOnly(perm))
	player := L.NewTable()
	player.RawSetString("get", L.NewFunction(r.stateGet(r.playerState, "state.player")))
	player.RawSetString("set", L.NewFunction(r.stateSet(r.playerState, "state.player")))
	state := L.NewTable()
	state.RawSetString("player", r.readOnly(player))
	shared := L.NewTable()
	shared.RawSetString("get", L.NewFunction(r.stateGet(r.worldState, "state.world")))
	shared.RawSetString("set", L.NewFunction(r.stateSet(r.worldState, "state.world")))
	shared.RawSetString("update", L.NewFunction(r.stateUpdate))
	state.RawSetString("world", r.readOnly(shared))
	r.openProfile(lib, state)
	r.openGame(state, world) // state.game, world.finish
	lib.RawSetString("state", r.readOnly(state))
	timer := L.NewTable()
	timer.RawSetString("start", L.NewFunction(r.timerStart))
	timer.RawSetString("cancel", L.NewFunction(r.timerCancel))
	timer.RawSetString("left", L.NewFunction(r.timerLeft))
	lib.RawSetString("timer", r.readOnly(timer))
	ui := L.NewTable()
	ui.RawSetString("dialog", L.NewFunction(r.dialog))
	ui.RawSetString("select", L.NewFunction(r.choose))
	ui.RawSetString("items", L.NewFunction(r.items))
	ui.RawSetString("image", L.NewFunction(r.image))
	ui.RawSetString("ask", L.NewFunction(r.ask))
	words := L.NewTable()
	words.RawSetString("normalize", L.NewFunction(wordsNormalize))
	words.RawSetString("same", L.NewFunction(wordsSame))
	lib.RawSetString("words", r.readOnly(words))
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
