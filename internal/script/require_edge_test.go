package script

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Edge cases of require (T-0026): what modules return, nesting and deep
// chains, paused hooks inside a module, the host functions at a module's
// top level, the output and memory limits, and what is left loading or
// cached after every way a require can end. Cycle tracking is per hook
// (Thread.loading), so it ends with the hook; TestRequireLoadingPerHook
// checks it on hooks that wait.

// noLoading fails when a released Instance left its module cache.
func noLoading(t *testing.T, r *Runtime) {
	t.Helper()
	for env := range r.loaded {
		if _, ok := r.envs[env]; !ok {
			t.Errorf("a released Instance still has a module cache")
		}
	}
}

func TestRequireReturnValues(t *testing.T) {
	tests := []struct {
		name, src string
		want      string // what the hook says about require's two results
	}{
		{"table", `return {v = 7}`, "table table true 1"},
		{"function", `return function() return "called" end`, "function function true 1"},
		{"false", `return false`, "boolean false true 1"},
		{"nil", `return nil`, "boolean true true 1"},
		{"nothing", `x = 1`, "boolean true true 1"},
		{"number", `return 42`, "number 42 true 1"},
		{"string", `return "s"`, "string s true 1"},
		{"first of many", `return 1, 2, 3`, "number 1 true 1"},
		{"zero", `return 0`, "number 0 true 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newModules(t, map[string]string{"m": `runs = (runs or 0) + 1 ` + tt.src})
			env := load(t, r, `function on_enter()
  local a, extra = require("m")
  local b = require("m")
  local shown = a
  if type(a) == "table" then shown = "table" elseif type(a) == "function" then shown = "function" end
  say(type(a) .. " " .. tostring(shown) .. " " .. tostring(a == b and extra == nil) .. " " .. runs)
end`)
			for range 2 { // the second call takes it from the cache too
				h := &recorder{}
				if _, err := r.Run(h, env, "on_enter", "p"); err != nil || len(h.out) != 1 || h.out[0] != "say:"+tt.want {
					t.Errorf("out = %q, err = %v, want %q", h.out, err, tt.want)
				}
			}
			noLoading(t, r)
		})
	}
}

// A module's returned function runs later with the Instance's globals,
// and a table one hook changes is the same table the next require returns.
func TestRequireReturnedValuesAreShared(t *testing.T) {
	r := newModules(t, map[string]string{
		"f": `return function() count = (count or 0) + 1 return count end`,
		"t": `return {n = 0}`,
	})
	env := load(t, r, `local f = require("f")
function on_enter() local t = require("t") t.n = t.n + 1 say(f() .. " " .. t.n .. " " .. count) end`)
	for i := 1; i <= 3; i++ {
		h := &recorder{}
		want := fmt.Sprintf("say:%d %d %d", i, i, i)
		if _, err := r.Run(h, env, "on_enter", "p"); err != nil || h.out[0] != want {
			t.Errorf("run %d: out = %q, err = %v", i, h.out, err)
		}
	}
	// Another Instance has its own count and its own table.
	other := load(t, r, `function on_enter() local t = require("t") say(t.n .. " " .. tostring(count)) end`)
	h := &recorder{}
	if _, err := r.Run(h, other, "on_enter", "p"); err != nil || h.out[0] != "say:0 nil" {
		t.Errorf("other Instance: out = %q, err = %v", h.out, err)
	}
}

// Nested requires: a diamond runs the shared module once, and a module
// sees what an earlier module set in the Instance's globals.
func TestRequireNested(t *testing.T) {
	r := newModules(t, map[string]string{
		"top":   `local l, rr = require("left"), require("right") return l .. rr .. order`,
		"left":  `order = (order or "") .. "L" return require("base") .. "l"`,
		"right": `order = (order or "") .. "R" return require("base") .. "r"`,
		"base":  `order = (order or "") .. "B" base_runs = (base_runs or 0) + 1 return "b"`,
	})
	env := load(t, r, `function on_enter() say(require("top") .. " " .. base_runs) end`)
	h := &recorder{}
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil || h.out[0] != "say:blbrLBR 1" {
		t.Errorf("out = %q, err = %v", h.out, err)
	}
	noLoading(t, r)
}

// modulesWith is newModules with limits.
func modulesWith(t *testing.T, limits Limits, mods map[string]string) *Runtime {
	t.Helper()
	r := New(limits)
	t.Cleanup(r.Close)
	for name, src := range mods {
		s, err := r.Compile("lib/"+name+".lua", src)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.AddModule(name, s); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// chainModules returns n modules c0 to c(n-1), each returning one more
// than the next; the last returns 0.
func chainModules(n int) map[string]string {
	mods := map[string]string{}
	for i := range n {
		if i == n-1 {
			mods["c"+strconv.Itoa(i)] = `return 0`
		} else {
			mods["c"+strconv.Itoa(i)] = `return require("c` + strconv.Itoa(i+1) + `") + 1`
		}
	}
	return mods
}

// A chain of every module a pack may have nests require MaxModules deep.
// Past the call depth of 200 it is a script error, never a crash, and
// nothing of the chain is left loading or cached; a shorter chain from the
// same modules works afterwards.
func TestRequireDeepChain(t *testing.T) {
	// A long time budget: under -race the chain takes more than 100 ms.
	slow := Limits{Time: 10 * time.Second, CallMemory: 256 << 20, StateMemory: 256 << 20}
	r := modulesWith(t, slow, chainModules(MaxModules))
	env := load(t, r, `function on_enter(p, from) say(tostring(require(from))) end`)
	h := &recorder{}
	_, err := r.Run(h, env, "on_enter", "p", "c0")
	switch {
	case err == nil:
		if h.out[0] != "say:"+strconv.Itoa(MaxModules-1) {
			t.Errorf("out = %q", h.out)
		}
	case !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "overflow"):
		t.Errorf("err = %v, want a stack overflow script error", err)
	}
	noLoading(t, r)
	if mods := r.loaded[env.table]; err != nil && mods != nil && mods.RawGetString("c0").String() != "nil" {
		t.Errorf("c0 cached after the chain failed")
	}
	h.out = nil
	if _, err := r.Run(h, env, "on_enter", "p", "c90"); err != nil || h.out[0] != "say:9" {
		t.Errorf("short chain after the deep one: out = %q, err = %v", h.out, err)
	}
	// The overflow can be caught: a module that pcalls the rest of the
	// chain still loads.
	mods := chainModules(MaxModules - 1)
	mods["catch"] = `local ok, err = pcall(require, "c0") return tostring(ok)`
	r2 := modulesWith(t, slow, mods)
	env2 := load(t, r2, `function on_enter() say(require("catch")) end`)
	h.out = nil
	if _, err := r2.Run(h, env2, "on_enter", "p"); err != nil || (h.out[0] != "say:false" && h.out[0] != "say:true") {
		t.Errorf("caught chain: out = %q, err = %v", h.out, err)
	}
	noLoading(t, r2)
}

// A Lua function recursing through require to the call depth limit fails
// cleanly: the cached module is returned at every level.
func TestRequireInDeepRecursion(t *testing.T) {
	r := newModules(t, map[string]string{"m": `return 1`})
	env := load(t, r, `function f(n) return require("m") + f(n + 1) end
function on_enter() local ok, err = pcall(f, 1) say(tostring(ok)) end`)
	h := &recorder{}
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil || len(h.out) != 1 || h.out[0] != "say:false" {
		t.Errorf("out = %q, err = %v", h.out, err)
	}
	noLoading(t, r)
}

// askModule waits in ui.dialog at its top level and counts its runs.
var askModule = map[string]string{"ask": `asks = (asks or 0) + 1 local n = ui.dialog("Pick", {"a", "b"}) return n`}

// A hook paused inside a module and then canceled loads nothing: the
// next require runs the module again, and an answer then caches it.
func TestRequirePausedThenCancel(t *testing.T) {
	r := newModules(t, askModule)
	env := load(t, r, `function on_enter() say("got " .. require("ask") .. " after " .. asks) end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatalf("th = %v, err = %v", th, err)
	}
	r.Cancel(th)
	if len(h.out) != 0 || r.Paused() != 0 {
		t.Errorf("after Cancel: out = %q, paused = %d", h.out, r.Paused())
	}
	noLoading(t, r)
	if mods := r.loaded[env.table]; mods != nil && mods.RawGetString("ask").String() != "nil" {
		t.Error("a canceled require was cached")
	}
	th, err = r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatalf("second Run: th = %v, err = %v", th, err)
	}
	if _, err := r.Resume(h, th, 1); err != nil || len(h.out) != 1 || h.out[0] != "say:got 1 after 2" {
		t.Errorf("out = %q, err = %v", h.out, err)
	}
	h.out = nil
	if th, err := r.Run(h, env, "on_enter", "p"); th != nil || err != nil || h.out[0] != "say:got 1 after 2" {
		t.Errorf("cached: th = %v, out = %q, err = %v", th, h.out, err)
	}
	noLoading(t, r)
}

// A module that catches the player leaving cannot wait again, and its
// hook ends.
func TestRequirePausedModuleCatchesLeave(t *testing.T) {
	r := newModules(t, map[string]string{"ask": `local ok = pcall(ui.dialog, "?", {"a"}) ui.dialog("again", {"b"}) return 1`})
	env := load(t, r, `function on_enter() local ok, err = pcall(require, "ask") say(tostring(err)) ui.dialog("x", {"y"}) end`)
	th, err := r.Run(&recorder{}, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatalf("th = %v, err = %v", th, err)
	}
	r.Cancel(th)
	if r.Paused() != 0 {
		t.Errorf("paused = %d", r.Paused())
	}
	noLoading(t, r)
}

func TestRequirePausedThenClose(t *testing.T) {
	r := New(DefaultLimits)
	s, err := r.Compile("lib/ask.lua", askModule["ask"])
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddModule("ask", s); err != nil {
		t.Fatal(err)
	}
	var env *Env
	for range 3 { // one Instance each: see TestRequireTwoHooksInOneModule
		env = load(t, r, `function on_enter() require("ask") end`)
		if th, err := r.Run(&recorder{}, env, "on_enter", "p"); err != nil || th == nil {
			t.Fatalf("th = %v, err = %v", th, err)
		}
	}
	r.Close()
	if r.Paused() != 0 {
		t.Errorf("paused = %d after Close", r.Paused())
	}
	if _, err := r.Run(&recorder{}, env, "on_enter", "p"); !errors.Is(err, ErrClosed) {
		t.Errorf("Run after Close = %v", err)
	}
}

// The room's Instance is released while a hook waits inside a module, as
// when a player's old room gives way. Cancel or a late Resume afterwards
// leaves no loading entry and no cache for the released Instance.
func TestRequirePausedThenRelease(t *testing.T) {
	for _, end := range []string{"cancel", "resume"} {
		t.Run(end, func(t *testing.T) {
			r := newModules(t, map[string]string{
				"ask":   askModule["ask"] + ` .. require("after")`,
				"after": `return ""`,
			})
			env := load(t, r, `function on_enter() say("got " .. require("ask")) end`)
			h := &recorder{}
			th, err := r.Run(h, env, "on_enter", "p")
			if err != nil || th == nil {
				t.Fatalf("th = %v, err = %v", th, err)
			}
			r.Release(env)
			if end == "cancel" {
				r.Cancel(th)
			} else if _, err := r.Resume(h, th, 2); err != nil {
				t.Errorf("Resume after Release = %v", err)
			}
			if r.Paused() != 0 || r.Kept(env) != 0 {
				t.Errorf("paused = %d, kept = %d", r.Paused(), r.Kept(env))
			}
			if len(r.loaded) != 0 {
				t.Errorf("released Instance left %d caches", len(r.loaded))
			}
			if _, err := r.Run(h, env, "on_enter", "p"); !errors.Is(err, ErrScript) {
				t.Errorf("Run on the released Instance = %v", err)
			}
		})
	}
}

// Two hooks of one Instance can wait at once (the Runtime allows it; see
// TestPausedHooksCountTowardStateMemory). The second one requiring the
// module the first waits in is not a cycle: it runs the module too. Found
// by this test when cycles were tracked per Instance; now per hook.
//
// Both runs finish, so the module ran twice for one Instance, and the
// cache keeps what the later one returned: the first hook holds a value
// the cache no longer has. The game keeps one open question per player, so
// players cannot reach this today.
func TestRequireTwoHooksInOneModule(t *testing.T) {
	r := newModules(t, map[string]string{"ask": `asks = (asks or 0) + 1 local n = ui.dialog("Pick", {"a", "b"}) return {n = n}`})
	env := load(t, r, `function on_enter() local m = require("ask") say("got " .. m.n .. " " .. asks .. " " .. tostring(m == require("ask"))) end`)
	h := &recorder{}
	th1, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th1 == nil {
		t.Fatalf("first: th = %v, err = %v", th1, err)
	}
	th2, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th2 == nil {
		t.Fatalf("second hook in the same module: th = %v, err = %v, want it waiting too", th2, err)
	}
	if !th1.loading["ask"] || !th2.loading["ask"] {
		t.Errorf("loading = %v and %v, want ask in both", th1.loading, th2.loading)
	}
	if _, err := r.Resume(h, th1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resume(h, th2, 2); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:got 1 2 true|say:got 2 2 true" {
		t.Errorf("out = %q", got)
	}
	h.out = nil
	if th, err := r.Run(h, env, "on_enter", "p"); th != nil || err != nil || h.out[0] != "say:got 2 2 true" {
		t.Errorf("cached: th = %v, out = %q, err = %v", th, h.out, err)
	}
	noLoading(t, r)
}

// A hook's loading set holds the modules it runs now: the one it waits
// in, and none after a require ended, whether it returned, failed or
// found a cycle.
func TestRequireLoadingPerHook(t *testing.T) {
	r := newModules(t, map[string]string{
		"ok":     `return 1`,
		"broken": `error("boom")`,
		"self":   `return require("self")`,
		"outer":  `return require("ask")`,
		"ask":    `local n = ui.dialog("Pick", {"a", "b"}) return n`,
	})
	const src = `function on_enter(p, which)
  require("ok") pcall(require, "broken") pcall(require, "self")
  if which ~= "" then require(which) end
  ui.dialog("done", {"x"})
end`
	for _, tt := range []struct {
		which string
		want  []string
	}{
		{"", nil},
		{"ask", []string{"ask"}},
		{"outer", []string{"ask", "outer"}},
	} {
		env := load(t, r, src) // a new Instance, so ask is not cached yet
		th, err := r.Run(&recorder{}, env, "on_enter", "p", tt.which)
		if err != nil || th == nil {
			t.Fatalf("%q: th = %v, err = %v", tt.which, th, err)
		}
		var got []string
		for name, on := range th.loading {
			if on {
				got = append(got, name)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, tt.want) {
			t.Errorf("%q: loading = %v, want %v", tt.which, got, tt.want)
		}
		if tt.which != "" { // the module's dialog, then the hook's
			if th, err = r.Resume(&recorder{}, th, 1); err != nil || th == nil {
				t.Fatalf("%q resumed: th = %v, err = %v", tt.which, th, err)
			}
			if len(th.loading) != 0 {
				t.Errorf("%q: loading = %v after the module returned", tt.which, th.loading)
			}
		}
		r.Cancel(th)
	}
}

// Two Instances may wait in the same module at once: each has its own
// loading entry.
func TestRequireTwoInstancesInOneModule(t *testing.T) {
	r := newModules(t, askModule)
	src := `function on_enter() say("got " .. require("ask") .. " " .. asks) end`
	a, b := load(t, r, src), load(t, r, src)
	h := &recorder{}
	tha, err := r.Run(h, a, "on_enter", "p")
	if err != nil || tha == nil {
		t.Fatalf("a: th = %v, err = %v", tha, err)
	}
	thb, err := r.Run(h, b, "on_enter", "p")
	if err != nil || thb == nil {
		t.Fatalf("b: th = %v, err = %v", thb, err)
	}
	if _, err := r.Resume(h, thb, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resume(h, tha, 1); err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.out, "|") != "say:got 2 1|say:got 1 1" {
		t.Errorf("out = %q", h.out)
	}
	noLoading(t, r)
}

// say, room.go, ui.items and state.player work at a module's top level
// when a hook requires it; at an Instance's load there is no player, and
// the load fails as it would in the room script.
func TestRequireHostCallsAtModuleTopLevel(t *testing.T) {
	tests := []struct{ name, mod, want string }{
		{"say", `say("from module")`, "say:from module"},
		{"room.describe", `room.describe()`, "describe"},
		{"room.go", `room.go("hall")`, "go:hall"},
		{"ui.items", `ui.items({{id = "k", label = "Key"}})`, "items:k=Key;"},
		{"state.player", `state.player.set("seen", (state.player.get("seen") or 0) + 1) say("seen " .. state.player.get("seen"))`, "say:seen 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newModules(t, map[string]string{"m": tt.mod})
			env := load(t, r, `function on_enter() require("m") end`)
			h := &recorder{}
			if _, err := r.Run(h, env, "on_enter", "p"); err != nil || len(h.out) != 1 || h.out[0] != tt.want {
				t.Errorf("in a hook: out = %q, err = %v, want %q", h.out, err, tt.want)
			}
			if _, err := r.Load("top.lua", `require("m")`); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "no player here") || !strings.Contains(err.Error(), "lib/m.lua") {
				t.Errorf("at load: err = %v", err)
			}
			noLoading(t, r)
		})
	}
}

// room.go in a module marks the hook as moving: the hook cannot wait in a
// dialog afterwards, as when the room script calls it.
func TestRequireRoomGoThenDialog(t *testing.T) {
	r := newModules(t, map[string]string{"m": `room.go("hall")`})
	env := load(t, r, `function on_enter() require("m") ui.dialog("?", {"a"}) end`)
	if th, err := r.Run(&recorder{}, env, "on_enter", "p"); th != nil || !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "room.go was called") {
		t.Errorf("th = %v, err = %v", th, err)
	}
	noLoading(t, r)
}

// A module's output counts toward the call's MaxOutputs, together with the
// hook's. A require that fails on the limit inside the module is not
// cached; one whose hook fails on it afterwards is.
func TestRequireOutputLimit(t *testing.T) {
	r := newModules(t, map[string]string{
		"talk":  `for i = 1, 60 do say("m") end return true`,
		"exact": `for i = 1, 100 do say("m") end return true`,
	})
	env := load(t, r, `function on_enter(p, which, before, after)
  for i = 1, tonumber(before) do say("h") end
  require(which)
  for i = 1, tonumber(after) do say("h") end
end`)
	for _, tt := range []struct {
		which, before, after string
		ok                   bool
	}{
		{"talk", "41", "0", false},  // 41 + 60, in the module
		{"talk", "41", "0", false},  // not cached: the module talks again
		{"talk", "40", "0", true},   // 40 + 60, cached now
		{"talk", "41", "59", true},  // 41 + 59
		{"talk", "0", "101", false}, // 101 in the hook
		{"talk", "0", "100", true},  // still cached
		{"exact", "0", "1", false},  // 100 + 1, in the hook: cached all the same
		{"exact", "1", "0", true},   // 1
	} {
		h := &recorder{}
		_, err := r.Run(h, env, "on_enter", "p", tt.which, tt.before, tt.after)
		if tt.ok && err != nil || !tt.ok && (!errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "outputs")) {
			t.Errorf("%s with %s before and %s after: err = %v", tt.which, tt.before, tt.after, err)
		}
	}
	noLoading(t, r)
}

// pcall around require catches a module's error, its error value comes
// through unchanged, and the module runs again on the next require. What
// the module set in the globals before failing stays: a module's top
// level is code of the Instance, as Lua's require does it.
func TestRequireErrorCaught(t *testing.T) {
	r := newModules(t, map[string]string{
		"str":   `half = (half or 0) + 1 error("boom")`,
		"table": `error({code = 7})`,
		"nil":   `error(nil)`,
		"level": `error("no position", 0)`,
	})
	env := load(t, r, `function on_enter()
  local ok1, e1 = pcall(require, "str")
  local ok2, e2 = pcall(require, "table")
  local ok3, e3 = pcall(require, "nil")
  local ok4, e4 = pcall(require, "level")
  local ok5 = pcall(require, "str")
  say(tostring(ok1) .. "|" .. e1 .. "|" .. e2.code .. "|" .. tostring(e3) .. "|" .. e4 .. "|" .. tostring(ok5) .. "|" .. half)
end`)
	h := &recorder{}
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil || len(h.out) != 1 || h.out[0] != "say:false|lib/str.lua:1: boom|7|nil|no position|false|2" {
		t.Errorf("out = %q, err = %v", h.out, err)
	}
	noLoading(t, r)
}

// A module that catches its own cycle loads, and is cached.
func TestRequireCycleCaught(t *testing.T) {
	r := newModules(t, map[string]string{
		"a": `local ok, err = pcall(require, "b") a_runs = (a_runs or 0) + 1 return err`,
		"b": `return require("a")`,
	})
	env := load(t, r, `function on_enter() local x = require("a") local y = require("b") say(x .. "|" .. y .. "|" .. a_runs) end`)
	h := &recorder{}
	const cycle = `lib/b.lua:1: require cycle: module "a" requires itself`
	want := "say:" + cycle + "|" + cycle + "|1"
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil || h.out[0] != want {
		t.Errorf("out = %q, err = %v", h.out, err)
	}
	noLoading(t, r)
}

// A module running too long or allocating too much ends the call even
// when the hook catches the error, and leaves nothing loading, also in a
// nested require.
func TestRequireBudgetsCaught(t *testing.T) {
	r := New(Limits{Time: 50 * time.Millisecond, CallMemory: 8 << 20, StateMemory: 256 << 20})
	t.Cleanup(r.Close)
	for name, src := range map[string]string{
		"loop":  `while true do end`,
		"outer": `return require("loop")`,
		"alloc": `local t = {} for i = 1, 1e7 do t[i] = {i} end`,
	} {
		sc, err := r.Compile("lib/"+name+".lua", src)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.AddModule(name, sc); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		which string
		want  error
	}{
		{"loop", ErrTimeout},
		{"outer", ErrTimeout},
		{"alloc", ErrMemory},
	} {
		env := load(t, r, `function on_enter(p, which) local ok = pcall(require, which) say("after " .. tostring(ok)) end`)
		h := &recorder{}
		_, err := r.Run(h, env, "on_enter", "p", tt.which)
		if !errors.Is(err, tt.want) && !(tt.want == ErrMemory && errors.Is(err, ErrTimeout)) {
			t.Errorf("%s: err = %v, want %v", tt.which, err, tt.want)
		}
		if len(h.out) != 0 {
			t.Errorf("%s: the hook ran on after its budget: %q", tt.which, h.out)
		}
		noLoading(t, r)
		r.Release(env)
	}
}

// What the cache holds counts toward PlayerMemory through the usual check
// after a call, though no global holds it: an Instance whose module keeps
// too much breaks, and releasing it frees its share of the world.
func TestRequireCacheMemory(t *testing.T) {
	r := New(Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 1 << 20, StateMemory: 8 << 20})
	t.Cleanup(r.Close)
	sc, err := r.Compile("lib/big.lua", `local t = {} for i = 1, 20000 do t[i] = "value " .. i end return t`)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddModule("big", sc); err != nil {
		t.Fatal(err)
	}
	before := r.total
	env := load(t, r, `function on_enter() local _ = require("big") end`)
	if _, err := r.Run(&recorder{}, env, "on_enter", "p"); !errors.Is(err, ErrStateMemory) {
		t.Errorf("module keeping about 2 MiB with PlayerMemory 1 MiB: err = %v", err)
	}
	if _, err := r.Run(&recorder{}, env, "on_enter", "p"); !errors.Is(err, ErrStateMemory) {
		t.Errorf("later call on the broken Instance: err = %v", err)
	}
	r.Release(env)
	if r.total != before || len(r.loaded) != 0 {
		t.Errorf("after Release: total = %d (was %d), %d caches", r.total, before, len(r.loaded))
	}
	// Under the limit, the same module in many Instances fills the world.
	sc, err = r.Compile("lib/half.lua", `local t = {} for i = 1, 5000 do t[i] = "value " .. i end return t`)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddModule("half", sc); err != nil {
		t.Fatal(err)
	}
	var envs []*Env
	full := false
	for range 40 {
		e, err := r.Load("x.lua", `function on_enter() local _ = require("half") end`)
		if err == nil {
			_, err = r.Run(&recorder{}, e, "on_enter", "p")
			envs = append(envs, e)
		}
		if errors.Is(err, ErrFull) {
			full = true
			break
		}
		if err != nil {
			t.Fatalf("Instance %d: %v", len(envs), err)
		}
	}
	if !full {
		t.Errorf("40 Instances keeping about 500 KiB each in their caches never filled an 8 MiB world")
	}
	for _, e := range envs {
		r.Release(e)
	}
	if r.total != before || len(r.loaded) != 0 {
		t.Errorf("after releasing all: total = %d (was %d), %d caches", r.total, before, len(r.loaded))
	}
}

// A module returning a shared library or the Instance's own globals adds
// little to what the Instance keeps.
func TestRequireCacheOfSharedValues(t *testing.T) {
	r := newModules(t, map[string]string{"lib": `return string`, "g": `return _G`})
	plain := load(t, r, `function on_enter() end`)
	env := load(t, r, `function on_enter() require("lib") require("g") end`)
	for _, e := range []*Env{plain, env} {
		if _, err := r.Run(&recorder{}, e, "on_enter", "p"); err != nil {
			t.Fatal(err)
		}
		r.measure(e.table, r.envs[e.table], nil)
	}
	if d := r.Kept(env) - r.Kept(plain); d > 16<<10 {
		t.Errorf("caching string and _G keeps %d bytes more", d)
	}
}

// require's argument: numbers are names too, anything else is an error,
// and a name that cannot be a module is quoted short.
func TestRequireNameArgument(t *testing.T) {
	r := newModules(t, map[string]string{"7": `return "seven"`})
	env := load(t, r, `function on_enter(p, name)
  if name == "num" then say(require(7)) return end
  if name == "none" then require() return end
  if name == "long" then require(string.rep("a", 100000)) return end
  require(name)
end`)
	for _, tt := range []struct{ arg, want string }{
		{"num", ""},
		{"none", "string expected"},
		{"../x", `module names are 1 to 64 of a-z, 0-9, _ and -, got "../x"`},
		{"", `module names are 1 to 64 of a-z, 0-9, _ and -, got ""`},
		{"Upper", `got "Upper"`},
		{"long", `got "` + strings.Repeat("a", 64) + `..."`},
		{"absent", `no module "absent" in this pack (lib/absent.lua)`},
	} {
		h := &recorder{}
		_, err := r.Run(h, env, "on_enter", "p", tt.arg)
		if tt.want == "" {
			if err != nil || h.out[0] != "say:seven" {
				t.Errorf("%s: out = %q, err = %v", tt.arg, h.out, err)
			}
			continue
		}
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.arg, err, tt.want)
		}
		if err != nil && len(err.Error()) > 1000 {
			t.Errorf("%s: error of %d bytes", tt.arg, len(err.Error()))
		}
	}
	noLoading(t, r)
}

// AddModule replaces a module of the same name without counting it twice.
func TestAddModuleReplaces(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	for i := range MaxModules {
		s, err := r.Compile("lib/x.lua", `return `+strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		name := "m" + strconv.Itoa(i)
		if i == MaxModules-1 {
			name = "m0"
		}
		if err := r.AddModule(name, s); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := r.Compile("lib/x.lua", `return 0`)
	if err := r.AddModule("last", s); err != nil {
		t.Errorf("the hundredth module: %v", err)
	}
	if err := r.AddModule("m0", s); err != nil {
		t.Errorf("replacing at the limit: %v", err)
	}
	if err := r.AddModule("extra", s); err == nil {
		t.Error("a hundred and first module")
	}
}

// FuzzRequire runs fuzzed module source and a fuzzed module name through
// require in a hook and at load: the call ends with a known error or a
// result, never a panic, the error stays short, and no released Instance
// keeps a cache.
func FuzzRequire(f *testing.F) {
	for _, s := range []struct{ mod, name string }{
		{`return {}`, "m"},
		{`return require("m")`, "m"},
		{`return require("o")`, "m"},
		{`local n = ui.dialog("?", {"a"}) return n`, "m"},
		{`error({})`, "m"},
		{`while true do end`, "m"},
		{`say("x") room.go("hall") return false`, "m"},
		{`state.player.set("k", 1) return nil`, "o"},
		{`return pcall(require, "m")`, "x"},
		{`return function() return require("o") end`, "M"},
	} {
		f.Add(s.mod, s.name)
	}
	f.Fuzz(func(t *testing.T, mod, name string) {
		r := New(Limits{Time: 20 * time.Millisecond, CallMemory: 16 << 20, StateMemory: 32 << 20})
		defer r.Close()
		known := func(err error) bool {
			return err == nil || errors.Is(err, ErrScript) || errors.Is(err, ErrTimeout) || errors.Is(err, ErrMemory) || errors.Is(err, ErrStateMemory)
		}
		for _, n := range []string{"m", "o"} {
			sc, err := r.Compile("lib/"+n+".lua", mod)
			if err != nil {
				return
			}
			if err := r.AddModule(n, sc); err != nil {
				t.Fatal(err)
			}
		}
		start := time.Now()
		h := &recorder{}
		env, err := r.Load("fuzz.lua", `function on_enter(p, name) local v = require(name) say(type(v)) end`)
		if err != nil {
			t.Fatal(err)
		}
		th, err := r.Run(h, env, "on_enter", "p", name)
		for i := 0; th != nil && i < 3; i++ {
			th, err = answerAny(t, r, h, th)
		}
		if th != nil {
			r.Cancel(th)
		}
		if !known(err) {
			t.Fatalf("Run = %v", err)
		}
		top, err := r.Load("top.lua", `require("m")`)
		if !known(err) {
			t.Fatalf("Load = %v", err)
		}
		if top != nil {
			r.Release(top)
		}
		r.Release(env)
		if len(r.loaded) != 0 {
			t.Fatalf("%d caches after releasing every Instance", len(r.loaded))
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("took %v", d)
		}
	})
}
