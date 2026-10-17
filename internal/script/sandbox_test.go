package script

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runHook loads src, runs on_enter and returns what it said and how it ended.
func runHook(t *testing.T, limits Limits, src string) ([]string, *Thread, error) {
	t.Helper()
	r := New(limits)
	t.Cleanup(r.Close)
	env := load(t, r, src)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	return h.out, th, err
}

// roomy has the default memory limits but a long time limit, for tests of
// what functions do rather than how long they take: under -race even
// unpack of 8000 values can take 100 ms.
var roomy = Limits{Time: 10 * time.Second, CallMemory: DefaultLimits.CallMemory, StateMemory: DefaultLimits.StateMemory}

// say1 runs a hook that says one expression and returns what it said.
func say1(t *testing.T, expr string) string {
	t.Helper()
	out, _, err := runHook(t, roomy, `function on_enter() say(tostring(`+expr+`)) end`)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	if len(out) != 1 {
		t.Fatalf("%s: outputs %q", expr, out)
	}
	return strings.TrimPrefix(out[0], "say:")
}

// The functions docs/lua-sandbox.md lists as available.
func TestSandboxOffersDocumentedLibrary(t *testing.T) {
	names := []string{
		"assert", "error", "getmetatable", "ipairs", "next", "pairs", "pcall", "rawequal", "rawget", "rawset",
		"select", "setmetatable", "tonumber", "tostring", "type", "unpack", "xpcall",
		"say", "room.describe", "ui.dialog",
		"string.byte", "string.char", "string.find", "string.format", "string.len", "string.lower",
		"string.rep", "string.reverse", "string.sub", "string.upper",
		"table.concat", "table.insert", "table.remove", "table.sort",
		"math.abs", "math.floor", "math.max", "math.min", "math.random", "math.sqrt",
	}
	for _, name := range names {
		if got := say1(t, "type("+name+")"); got != "function" {
			t.Errorf("%s is %s, want function", name, got)
		}
	}
	if got := say1(t, "_VERSION"); got != "Lua 5.1" {
		t.Errorf("_VERSION = %s", got)
	}
	if got := say1(t, "type(_G)"); got != "table" {
		t.Errorf("_G is %s", got)
	}
}

func TestSandboxBasics(t *testing.T) {
	tests := []struct{ expr, want string }{
		{`select("#", unpack({1, 2, 3}))`, "3"},
		{`select("#", unpack({}, 1, 8000))`, "8000"},
		{`string.byte("A")`, "65"},
		{`select("#", string.byte("abc", 1, -1))`, "3"},
		{`select("#", string.byte("abc", -2))`, "1"},
		{`#string.rep("ab", 3)`, "6"},
		{`#string.rep("x", 1048576)`, "1048576"},
		{`string.rep("x", 0)`, ""},
		{`string.rep("x", -5)`, ""},
		{`string.rep("", 1e12)`, ""},
		{`string.format("%99d", 1):len()`, "99"},
		{`string.format("%-5s|", "a")`, "a    |"},
		{`string.format("%.2f", 1/3)`, "0.33"},
		{`string.format("%d%%", 50)`, "50%"},
		{`table.concat({"a", "b", "c"}, ",", 2, 3)`, "b,c"},
		{`table.concat({}, ",")`, ""},
		{`#table.concat({string.rep("x", 524288), string.rep("y", 524288)})`, "1048576"},
		{`("a.b"):find(".", 1, true)`, "2"},
		{`string.find("abc", "z", 1, true)`, "nil"},
		{`math.floor(2.5)`, "2"},
		{`("Hi"):upper()`, "HI"},
		{`pcall(error, "x")`, "false"},
	}
	for _, tt := range tests {
		if got := say1(t, tt.expr); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.expr, got, tt.want)
		}
	}
}

func TestSandboxRefuses(t *testing.T) {
	big := `string.rep("x", 1048576)`
	tests := []struct{ name, src string }{
		{"string.match", `string.match("a", "a")`},
		{"string.gmatch", `string.gmatch("a", "a")`},
		{"method match", `("a"):match("a")`},
		{"method gsub", `("a"):gsub("a", "b")`},
		{"method find pattern", `("a"):find("a")`},
		{"find plain false", `string.find("a", "a", 1, false)`},
		{"rep one byte over", `string.rep("x", 1048577)`},
		{"rep two bytes", `string.rep("xy", 524289)`},
		{"format width 100", `string.format("%100d", 1)`},
		{"format flags then width", `string.format("%-0100d", 1)`},
		{"format precision 100", `string.format("%.100f", 1)`},
		{"format result too long", `string.format("%s%s", ` + big + `, "x")`},
		{"concat one byte over", `table.concat({` + big + `, "x"})`},
		{"concat with separator", `table.concat({string.rep("x", 524288), string.rep("y", 524288)}, ",")`},
		{"concat range", `local s = string.rep("x", 600000) table.concat({s, s, s}, "", 2, 3)`},
		{"unpack 8001", `unpack({}, 1, 8001)`},
		{"unpack long table", `local t = {} for i = 1, 8001 do t[i] = i end unpack(t)`},
		{"byte 8001", `string.byte(string.rep("x", 8001), 1, -1)`},
		{"say too long", `function on_enter() say(string.rep("x", 4001)) end on_enter()`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(roomy)
			defer r.Close()
			if _, err := r.Load("bad.lua", tt.src); !errors.Is(err, ErrScript) {
				t.Errorf("Load = %v, want ErrScript", err)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	if _, err := r.Load("big.lua", strings.Repeat("-", MaxScriptBytes)); err != nil {
		t.Errorf("script of MaxScriptBytes: %v", err)
	}
	tests := []struct {
		name, src string
		want      []string
	}{
		{"too large", strings.Repeat("-", MaxScriptBytes+1), []string{"rooms/x.lua", "larger than"}},
		{"syntax error", "function (", []string{"rooms/x.lua"}},
		{"runtime error at top level", "\n\nerror('boom')", []string{"rooms/x.lua:3", "boom"}},
		{"nil call", "nothing()", []string{"rooms/x.lua:1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Load("rooms/x.lua", tt.src)
			if !errors.Is(err, ErrScript) {
				t.Fatalf("Load = %v, want ErrScript", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
	// A failed load leaves the runtime usable.
	env := load(t, r, `function on_enter() say("ok") end`)
	h := &recorder{}
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil || len(h.out) != 1 {
		t.Errorf("Run after failed loads = %v, %q", err, h.out)
	}
}

func TestHasHookAndRun(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `on_enter = 5 function on_leave(p) say("bye " .. p.name) end`)
	if env.HasHook("on_enter") || !env.HasHook("on_leave") || env.HasHook("say") {
		t.Errorf("HasHook: on_enter %v, on_leave %v, say %v", env.HasHook("on_enter"), env.HasHook("on_leave"), env.HasHook("say"))
	}
	h := &recorder{}
	if th, err := r.Run(h, env, "on_enter", "p"); th != nil || err != nil || len(h.out) != 0 {
		t.Errorf("Run of a non-function = %v, %v, %q", th, err, h.out)
	}
	if _, err := r.Run(h, env, "on_leave", "Zoë <b>"); err != nil || h.out[0] != "say:bye Zoë <b>" {
		t.Errorf("Run = %v, %q", err, h.out)
	}
}

func TestOutputLimit(t *testing.T) {
	tests := []struct {
		name string
		src  string
		ok   bool
	}{
		{"100 says", `for i = 1, 100 do say(i) end`, true},
		{"101 says", `for i = 1, 101 do say(i) end`, false},
		{"100 describes", `for i = 1, 100 do room.describe() end`, true},
		{"mixed 101", `for i = 1, 50 do say(i) room.describe() end say("x")`, false},
		{"4000 bytes", `say(string.rep("x", 4000))`, true},
		{"4001 bytes", `say(string.rep("x", 4001))`, false},
		{"number", `say(42)`, true},
		{"nil", `say(nil)`, false},
		{"table", `say({})`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runHook(t, DefaultLimits, `function on_enter() `+tt.src+` end`)
			if tt.ok && err != nil {
				t.Errorf("err = %v, want none", err)
			}
			if !tt.ok && !errors.Is(err, ErrScript) {
				t.Errorf("err = %v, want ErrScript", err)
			}
		})
	}
}

// The output count starts again with every call: answering a dialog starts
// a new call.
func TestOutputLimitIsPerCall(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter()
  for i = 1, 50 do say(i) end
  ui.dialog("?", {"a"})
  for i = 1, 100 do say(i) end
end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatalf("Run = %v, %v", th, err)
	}
	if th, err = r.Resume(h, th, 1); err != nil || th != nil {
		t.Fatalf("Resume = %v, %v", th, err)
	}
	if len(h.out) != 150 {
		t.Errorf("%d outputs, want 150", len(h.out))
	}
}

// BUG: docs/lua-sandbox.md counts only say and room.describe toward the 100
// outputs per call, but ui.dialog counts too (sandbox.go:215), so a hook that
// says 100 lines and then asks fails.
func TestDialogIsNotCountedAsOutput(t *testing.T) {
	_, th, err := runHook(t, DefaultLimits, `function on_enter() for i = 1, 100 do say(i) end ui.dialog("?", {"a"}) end`)
	if err != nil || th == nil {
		t.Errorf("Run = %v, %v; want the dialog after 100 says", th, err)
	}
}

func TestDialogArguments(t *testing.T) {
	opt := func(n int) string { return `"` + strings.Repeat("o", n) + `"` }
	nine := `{"1","2","3","4","5","6","7","8","9"}`
	tests := []struct {
		name, args string
		options    int // 0: must fail
	}{
		{"one option", `"?", {"a"}`, 1},
		{"nine options", `"?", ` + nine, 9},
		{"empty title", `"", {"a"}`, 1},
		{"title 200 bytes", `string.rep("t", 200), {"a"}`, 1},
		{"option 200 bytes", `"?", {` + opt(200) + `}`, 1},
		{"numeric title", `5, {"a"}`, 1},
		{"no options", `"?", {}`, 0},
		{"ten options", `"?", {"1","2","3","4","5","6","7","8","9","10"}`, 0},
		{"title 201 bytes", `string.rep("t", 201), {"a"}`, 0},
		{"option 201 bytes", `"?", {` + opt(201) + `}`, 0},
		{"empty option", `"?", {"a", ""}`, 0},
		{"number option", `"?", {"a", 2}`, 0},
		{"table option", `"?", {{}}`, 0},
		{"options not a table", `"?", "a"`, 0},
		{"no title", `nil, {"a"}`, 0},
		{"no arguments", ``, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, th, err := runHook(t, DefaultLimits, `function on_enter() ui.dialog(`+tt.args+`) end`)
			if tt.options == 0 {
				if th != nil || !errors.Is(err, ErrScript) {
					t.Errorf("Run = %v, %v; want ErrScript", th, err)
				}
				return
			}
			if err != nil || th == nil || len(th.Dialog.Options) != tt.options {
				t.Errorf("Run = %+v, %v; want a dialog with %d options", th, err, tt.options)
			}
		})
	}
}

func TestDialogReturnsNumberAndLabel(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter()
  local n, label = ui.dialog("?", {"Nord", "Süd", "Ost"})
  say(type(n) .. " " .. n .. " " .. label)
end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resume(h, th, 2); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:number 2 Süd" {
		t.Errorf("outputs = %q", got)
	}
}

// Every Resume is a new call with a fresh time budget.
func TestTimeLimitAfterResume(t *testing.T) {
	r := New(Limits{Time: 30 * time.Millisecond, CallMemory: 32 << 20, StateMemory: 64 << 20})
	defer r.Close()
	env := load(t, r, `function on_enter() ui.dialog("?", {"a"}) while true do end end`)
	th, err := r.Run(&recorder{}, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatalf("Run = %v, %v", th, err)
	}
	if _, err := r.Resume(&recorder{}, th, 1); !errors.Is(err, ErrTimeout) {
		t.Errorf("Resume = %v, want ErrTimeout", err)
	}
	// The runtime keeps working after a timeout.
	th, err = r.Run(&recorder{}, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Errorf("Run after a timeout = %v, %v", th, err)
	}
}

// The time a player takes to answer does not count: a hook that keeps
// asking outlives its time limit many times over.
func TestDialogWaitDoesNotCountTowardTime(t *testing.T) {
	if testing.Short() {
		t.Skip("runs past the time limit on the wall clock")
	}
	const limit = 20 * time.Millisecond
	r := New(Limits{Time: limit, CallMemory: 32 << 20, StateMemory: 64 << 20})
	defer r.Close()
	env := load(t, r, `function on_enter() while true do ui.dialog("?", {"again"}) end end`)
	start := time.Now()
	th, err := r.Run(&recorder{}, env, "on_enter", "p")
	for n := 0; err == nil && time.Since(start) < 5*limit; n++ {
		if n > 1e6 {
			t.Fatal("wall clock did not advance")
		}
		th, err = r.Resume(&recorder{}, th, 1)
	}
	if err != nil || th == nil {
		t.Errorf("after %v: %v, %v", time.Since(start), th, err)
	}
}

// BUG: docs/lua-sandbox.md promises "Every script gets its own globals", but
// _G is the shared sandbox table (sandbox.go:40, script.go:131): a global
// set through _G is visible to every other script, and one room can replace
// say or ui for all rooms.
func TestGlobalsTableIsPerScript(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	load(t, r, `_G.leak = "a" rawset(_G, "say", function() end)`)
	b := load(t, r, `function on_enter() say(tostring(leak)) end`)
	h := &recorder{}
	if _, err := r.Run(h, b, "on_enter", "p"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:nil" {
		t.Errorf("second script said %q, want %q: the first script's _G writes leaked", got, "say:nil")
	}
}

// BUG: limitResults (sandbox.go:95) computes j-i in int, which overflows for
// far-apart bounds, so the check passes and gopher-lua pushes values for
// seconds and about a GiB, far past the time limit (and stops only because
// the stack grows past the memory budget).
func TestUnpackHugeRangeIsRefusedQuickly(t *testing.T) {
	if testing.Short() {
		t.Skip("fails slowly while the bug is present")
	}
	for _, src := range []string{
		`unpack({}, -2^62, 2^62)`,
		`string.byte("abc", -2^62, 2^62)`,
	} {
		t.Run(src, func(t *testing.T) {
			r := New(Limits{Time: 50 * time.Millisecond, CallMemory: 16 << 20, StateMemory: 64 << 20})
			defer r.Close()
			start := time.Now()
			_, err := r.Load("bad.lua", src)
			if !errors.Is(err, ErrScript) {
				t.Errorf("Load = %v, want ErrScript (too many results)", err)
			}
			if d := time.Since(start); d > 500*time.Millisecond {
				t.Errorf("took %v with a 50 ms limit", d)
			}
		})
	}
}

// BUG: string.format checks the result size only after gopher-lua's
// fmt.Sprintf built it (sandbox.go:135-136), and gopher-lua hands the format
// to Go's fmt, so "%[1]s" repeats one argument as often as the format says.
// One call builds hundreds of MiB in Go (an 8000-fold format is GiBs), which
// the watcher cannot interrupt. The doc promises a size check before running.
// "%.[1]999999f" also slips past the width/precision pattern (sandbox.go:128).
func TestFormatRefusesBeforeBuilding(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates heavily while the bug is present")
	}
	for name, src := range map[string]string{
		"repeated argument":    `string.format(string.rep("%[1]s", 32), string.rep("x", 1e6))`,
		"indexed precision":    `string.format(string.rep("%.[1]999999f", 32), 1)`,
		"many large arguments": `local s = string.rep("x", 1e6) string.format(string.rep("%s", 32), s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s)`,
	} {
		t.Run(name, func(t *testing.T) {
			r := New(Limits{Time: 10 * time.Second, CallMemory: 1 << 30, StateMemory: 1 << 30})
			defer r.Close()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err := r.Load("bad.lua", src)
			runtime.ReadMemStats(&after)
			if !errors.Is(err, ErrScript) {
				t.Errorf("Load = %v, want ErrScript", err)
			}
			if n := after.TotalAlloc - before.TotalAlloc; n > 16<<20 {
				t.Errorf("allocated %d MiB before refusing; a 1 MiB result cap should refuse first", n>>20)
			}
		})
	}
}

// yieldAcross are hooks that call ui.dialog where gopher-lua cannot pause:
// inside pcall, inside a Lua function called from Go, inside a metamethod.
var yieldAcross = []struct{ name, src string }{
	{"pcall", `function on_enter() local ok, n = pcall(ui.dialog, "?", {"a", "b"}) say(tostring(ok) .. tostring(n)) end`},
	{"xpcall", `function on_enter() xpcall(function() ui.dialog("?", {"a"}) end, function(e) return e end) say("after") end`},
	{"__index", `function on_enter() local t = setmetatable({}, {__index = function() return ui.dialog("?", {"a"}) end}) say(tostring(t.x)) end`},
	{"table.sort comparator", `function on_enter() table.sort({3, 1, 2}, function(a, b) ui.dialog("?", {"a"}) return a < b end) end`},
	{"__tostring", `function on_enter() say(tostring(setmetatable({}, {__tostring = function() ui.dialog("?", {"a"}) return "s" end}))) end`},
}

// BUG: script.go:189 lets panics from gopher-lua escape Run. ui.dialog in a
// table.sort comparator panics with the *lua.ApiError raised at
// sandbox.go:217, and in __tostring with a nil pointer dereference inside
// gopher-lua. In the game the panic leaves World.mu locked (game.go:95-97).
func TestDialogAcrossGoCallDoesNotPanic(t *testing.T) {
	for _, tt := range yieldAcross {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("Run panicked: %v", p)
				}
			}()
			runHook(t, DefaultLimits, tt.src)
		})
	}
}

// BUG: where ui.dialog cannot pause (inside pcall, xpcall or a metamethod)
// it returns nothing and the hook goes on as if the player had never been
// asked: Run returns neither a dialog nor an error. It should do one of the
// two; the doc says ui.dialog waits for the player.
func TestDialogNeverSilentlySkipped(t *testing.T) {
	for _, tt := range yieldAcross {
		t.Run(tt.name, func(t *testing.T) {
			defer func() { _ = recover() }() // the panics are TestDialogAcrossGoCallDoesNotPanic's
			out, th, err := runHook(t, DefaultLimits, tt.src)
			if th == nil && err == nil {
				t.Errorf("Run finished without the dialog and without an error; said %q", out)
			}
		})
	}
}

// A dropped Thread holds nothing the runtime needs: new calls work and the
// old dialog can be answered later.
func TestManyPausedThreads(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter(p) local n, l = ui.dialog(p.name, {"a", "b"}) say(p.name .. l) end`)
	var ths []*Thread
	for i := range 50 {
		th, err := r.Run(&recorder{}, env, "on_enter", strconv.Itoa(i))
		if err != nil || th == nil {
			t.Fatalf("Run %d = %v, %v", i, th, err)
		}
		ths = append(ths, th)
	}
	for i := len(ths) - 1; i >= 0; i -= 7 {
		h := &recorder{}
		if th, err := r.Resume(h, ths[i], 2); err != nil || th != nil || h.out[0] != "say:"+strconv.Itoa(i)+"b" {
			t.Errorf("Resume %d = %v, %v, %q", i, th, err, h.out)
		}
	}
}
