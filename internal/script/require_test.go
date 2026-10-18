package script

import (
	"errors"
	"strings"
	"testing"
)

// newModules returns a Runtime with the given lib modules added.
func newModules(t *testing.T, mods map[string]string) *Runtime {
	t.Helper()
	r := New(DefaultLimits)
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

func TestRequireRunsOncePerInstance(t *testing.T) {
	r := newModules(t, map[string]string{
		"counter": `runs = (runs or 0) + 1 local M = {} function M.hello(who) return "hello " .. who end return M`,
		"plain":   `x = 1`,
	})
	src := `local c = require("counter")
function on_enter(p)
  local again = require("counter")
  say(c.hello("p") .. " " .. tostring(c == again) .. " " .. runs .. " " .. tostring(require("plain")))
end`
	h := &recorder{}
	for _, want := range []string{"say:hello p true 1 true", "say:hello p true 1 true"} {
		env := load(t, r, src) // a new Instance runs the module again
		for range 2 {
			h.out = nil
			if _, err := r.Run(h, env, "on_enter", "p"); err != nil || h.out[0] != want {
				t.Fatalf("out = %q, err = %v", h.out, err)
			}
		}
		r.Release(env)
	}
	if len(r.loaded) != 0 || len(r.loading) != 0 {
		t.Errorf("released Instances left %d caches, %d loading", len(r.loaded), len(r.loading))
	}
}

func TestRequireErrors(t *testing.T) {
	r := newModules(t, map[string]string{
		"a":      `return require("b")`,
		"b":      `return require("a")`,
		"self":   `return require("self")`,
		"broken": `error("boom")`,
	})
	for _, tt := range []struct{ body, want string }{
		{`require("missing")`, `no module "missing" in this pack (lib/missing.lua)`},
		{`require("a")`, `require cycle: module "a" requires itself`},
		{`require("self")`, `require cycle: module "self" requires itself`},
		{`require("broken")`, `lib/broken.lua:1: boom`},
		{`require({})`, `string expected`},
	} {
		env := load(t, r, "function on_enter() "+tt.body+" end")
		if _, err := r.Run(&recorder{}, env, "on_enter", "p"); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.body, err, tt.want)
		}
		// A failed require leaves nothing half loaded: the next try runs it again.
		if _, err := r.Run(&recorder{}, env, "on_enter", "p"); !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s again: err = %v", tt.body, err)
		}
	}
	if len(r.loading) != 0 {
		t.Errorf("loading = %v after failed requires", r.loading)
	}
}

func TestRequireAtTopLevelFailsTheLoad(t *testing.T) {
	r := newModules(t, map[string]string{"broken": `error("boom")`})
	if _, err := r.Load("rooms.lua", `require("broken")`); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}

// A module's top level runs with the Instance's globals and its budgets: an
// endless loop in a module is stopped like one in the room script.
func TestRequireUsesTheCallsBudgets(t *testing.T) {
	r := newModules(t, map[string]string{"loop": `while true do end`})
	env := load(t, r, `function on_enter() require("loop") end`)
	if _, err := r.Run(&recorder{}, env, "on_enter", "p"); !errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
	}
	if len(r.loading) != 0 {
		t.Errorf("loading = %v after a timeout", r.loading)
	}
}

// A module can wait for the player in a hook, and the waiting hook
// finishes the require after the answer.
func TestRequireWaitsInDialog(t *testing.T) {
	r := newModules(t, map[string]string{"ask": `local n = ui.dialog("Pick", {"a", "b"}) return n`})
	env := load(t, r, `function on_enter() say("got " .. require("ask")) end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatalf("th = %v, err = %v", th, err)
	}
	if _, err := r.Resume(h, th, 2); err != nil || h.out[len(h.out)-1] != "say:got 2" {
		t.Errorf("out = %q, err = %v", h.out, err)
	}
}

// What the module returns counts toward the Instance's memory even when no
// global holds it.
func TestRequiredValuesCount(t *testing.T) {
	r := newModules(t, map[string]string{"big": `local t = {} for i = 1, 3000 do t[i] = "v" .. i end return t`})
	small := load(t, r, `function on_enter() end`)
	big := load(t, r, `function on_enter() local _ = require("big") end`)
	for _, env := range []*Env{small, big} {
		if _, err := r.Run(&recorder{}, env, "on_enter", "p"); err != nil {
			t.Fatal(err)
		}
		st := r.envs[env.table]
		r.measure(env.table, st, nil)
	}
	if r.Kept(big) < r.Kept(small)+3000*sizeEntry {
		t.Errorf("kept %d with the module's table, %d without", r.Kept(big), r.Kept(small))
	}
}

func TestAddModuleNames(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	s, err := r.Compile("lib/x.lua", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "A", "a/b", "../x", "x.lua", strings.Repeat("a", 65)} {
		if err := r.AddModule(bad, s); err == nil {
			t.Errorf("AddModule(%q) succeeded", bad)
		}
	}
	for i := range MaxModules {
		if err := r.AddModule("m"+string(rune('a'+i%26))+strings.Repeat("x", i/26), s); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AddModule("onemore", s); err == nil {
		t.Error("more than MaxModules modules")
	}
}
