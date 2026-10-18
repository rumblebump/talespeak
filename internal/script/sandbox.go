package script

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

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
	room.RawSetString("go", L.NewFunction(r.goRoom))
	lib.RawSetString("room", r.readOnly(room))
	player := L.NewTable()
	player.RawSetString("get", L.NewFunction(r.stateGet))
	player.RawSetString("set", L.NewFunction(r.stateSet))
	state := L.NewTable()
	state.RawSetString("player", r.readOnly(player))
	lib.RawSetString("state", r.readOnly(state))
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
	how := voiceArg(L, 2)
	r.output(L).Say(text, how)
	return 0
}

// voiceArg reads the optional {speak = bool, show = bool} table at n:
// speak reads the text aloud even when read aloud is off, show = false
// shows it only when it cannot be spoken (a listening exercise).
func voiceArg(L *lua.LState, n int) Voice {
	if L.GetTop() < n || L.Get(n) == lua.LNil {
		return Voice{}
	}
	t := L.CheckTable(n)
	flag := func(name string, def bool) bool {
		switch v := t.RawGetString(name).(type) {
		case *lua.LNilType:
			return def
		case lua.LBool:
			return bool(v)
		default:
			L.ArgError(n, name+" must be true or false")
			return false
		}
	}
	v := Voice{Speak: flag("speak", false), Hide: !flag("show", true)}
	if v.Hide && !v.Speak {
		L.ArgError(n, "show = false needs speak = true: the player must see or hear it")
	}
	return v
}

// room.describe() sends the current room's title and description.
func (r *Runtime) describe(L *lua.LState) int {
	r.output(L).DescribeRoom()
	return 0
}

// room.go(id) moves the player to room id of the pack once the hook ends.
// The new room's on_enter runs then; the hook cannot wait in a dialog after
// room.go, so a move is never half done. The last room.go of a hook wins.
func (r *Runtime) goRoom(L *lua.LState) int {
	id := L.CheckString(1)
	host := r.output(L)
	if len(id) > 64 {
		L.ArgError(1, "room ids are at most 64 bytes")
	}
	if !host.Go(id) {
		L.ArgError(1, "no room "+strconv.Quote(id)+" in this pack")
	}
	r.cur.thread.moved = true
	return 0
}

// ui.dialog(title, options) shows a choice and waits until the player picks
// one. It returns the option's number (1-based) and its label.
func (r *Runtime) dialog(L *lua.LState) int {
	d := r.dialogArgs(L, "ui.dialog")
	choices := r.pause(L, &d).choices
	L.Push(lua.LNumber(choices[0]))
	L.Push(lua.LString(d.Options[choices[0]-1]))
	return 2
}

// ui.select(title, options [, {min = 1, max = #options}]) lets the player
// pick several options and waits for them. It returns two lists: the
// numbers picked, in ascending order, and their labels.
func (r *Runtime) choose(L *lua.LState) int {
	d := r.dialogArgs(L, "ui.select")
	n := len(d.Options)
	d.Multiple, d.Min, d.Max = true, 1, n
	if L.GetTop() >= 3 && L.Get(3) != lua.LNil {
		limits := L.CheckTable(3)
		d.Min = countField(L, limits, "min", d.Min)
		d.Max = countField(L, limits, "max", d.Max)
	}
	if d.Min < 0 || d.Max < 1 || d.Min > d.Max || d.Max > n {
		L.ArgError(3, "need 0 <= min <= max, 1 <= max <= number of options")
	}
	choices := r.pause(L, &d).choices
	nums, labels := L.CreateTable(len(choices), 0), L.CreateTable(len(choices), 0)
	for i, c := range choices {
		nums.RawSetInt(i+1, lua.LNumber(c))
		labels.RawSetInt(i+1, lua.LString(d.Options[c-1]))
	}
	L.Push(nums)
	L.Push(labels)
	return 2
}

// countField reads limits[name] as a whole number from -9 to 9, or def
// when it is nil.
func countField(L *lua.LState, limits *lua.LTable, name string, def int) int {
	switch v := limits.RawGetString(name).(type) {
	case *lua.LNilType:
		return def
	case lua.LNumber:
		f := float64(v)
		if f != math.Trunc(f) || f < -MaxDialogOptions || f > MaxDialogOptions {
			L.ArgError(3, name+" must be a whole number of options")
		}
		return int(f)
	default:
		L.ArgError(3, name+" must be a number")
		return 0
	}
}

// dialogArgs checks the title and options of ui.dialog and ui.select, and
// that there is a player to ask.
func (r *Runtime) dialogArgs(L *lua.LState, name string) Dialog {
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
		L.RaiseError("no player here: call %s from a hook such as on_enter", name)
	}
	return d
}

// ui.ask(question [, {strict = true, speak = true, show = false}]) waits
// for the player to type an answer and returns it: one line, trimmed, with
// control characters turned into spaces. strict asks the client for no
// typing help (no autocorrect, autocomplete or suggestions), for spelling
// practice and riddles. speak and show work as in say. The script decides
// whether the answer is right, for example with words.same.
func (r *Runtime) ask(L *lua.LState) int {
	title := L.CheckString(1)
	if title == "" || len(title) > MaxTitleBytes {
		L.ArgError(1, "question must be 1 to 200 bytes")
	}
	d := Dialog{Title: title, Ask: true}
	if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
		opts := L.CheckTable(2)
		switch v := opts.RawGetString("strict").(type) {
		case *lua.LNilType:
		case lua.LBool:
			d.Strict = bool(v)
		default:
			L.ArgError(2, "strict must be true or false")
		}
		d.Voice = voiceArg(L, 2)
	}
	if r.cur == nil || r.cur.host == nil {
		L.RaiseError("no player here: call ui.ask from a hook such as on_enter")
	}
	L.Push(lua.LString(r.pause(L, &d).text))
	return 1
}

// normalize lowercases s, trims it and turns every run of spaces into
// one, so "  The  Castle " and "the castle" compare equal.
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// words.normalize(s) returns s lowercased, trimmed, with single spaces.
func wordsNormalize(L *lua.LState) int {
	L.Push(lua.LString(normalize(L.CheckString(1))))
	return 1
}

// words.same(a, b) reports whether a and b are the same words, ignoring
// case and extra spaces: the exact check for spelling practice.
func wordsSame(L *lua.LState) int {
	L.Push(lua.LBool(normalize(L.CheckString(1)) == normalize(L.CheckString(2))))
	return 1
}

// ui.items({{id = "straw", label = "heap of straw"}, ...}) replaces the
// things in the room the player can click; on_click(player, id) runs on a
// click. An empty list clears them.
func (r *Runtime) items(L *lua.LState) int {
	list := L.CheckTable(1)
	n := list.Len()
	if n > MaxItems {
		L.ArgError(1, "more than 20 items")
	}
	items := make([]Item, n)
	seen := map[string]bool{}
	for i := range n {
		t, ok := list.RawGetInt(i + 1).(*lua.LTable)
		if !ok {
			L.ArgError(1, "items must be tables with an id and a label")
		}
		id, ok1 := t.RawGetString("id").(lua.LString)
		label, ok2 := t.RawGetString("label").(lua.LString)
		if !ok1 || !ok2 || !ValidItemID(string(id)) || !validLabel(string(label)) || seen[string(id)] {
			L.ArgError(1, "each item needs a unique id (a-z, 0-9, _ and -, at most 64) and a label: one line of 1 to 100 bytes")
		}
		seen[string(id)] = true
		items[i] = Item{ID: string(id), Label: string(label)}
	}
	r.output(L).Items(items)
	return 0
}

// validLabel is the rule for item labels in room YAML too: one line of
// UTF-8 text, 1 to MaxLabelBytes long, not only spaces.
func validLabel(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= MaxLabelBytes && utf8.ValidString(s) && !strings.ContainsAny(s, "\r\n")
}

// ValidItemID reports whether id can name an item: 1 to 64 of a-z, 0-9, _
// and -, starting with a letter or digit, like room ids.
func ValidItemID(id string) bool {
	if id == "" || len(id) > MaxItemIDBytes {
		return false
	}
	for i, c := range []byte(id) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '_' || c == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}

// ui.image(name [, caption]) shows one of the pack's images, from its
// assets/ folder, as a popup with an optional caption.
func (r *Runtime) image(L *lua.LState) int {
	name := L.CheckString(1)
	caption := L.OptString(2, "")
	if len(caption) > MaxCaptionBytes {
		L.ArgError(2, "caption longer than 200 bytes")
	}
	if !r.output(L).Image(name, caption) {
		L.ArgError(1, "the pack has no image named "+strconv.Quote(truncate(name, 64)))
	}
	return 0
}

// truncate cuts s to at most n bytes, for error messages.
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
