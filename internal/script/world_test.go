package script

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// T-0030: state.world.get, set and update, read-only hooks and the
// Changed callback, against the recorder host.

// worldHook runs body as on_enter with h, in a runtime that lives for the
// test, and returns what it said and the error.
func worldHook(t *testing.T, r *Runtime, h *recorder, hk Hook, body string) (string, *Thread, error) {
	t.Helper()
	env := load(t, r, "function "+hk.Name+"(player)\n"+body+"\nend")
	h.out = nil
	hk.Player = "p"
	th, err := r.RunHook(h, env, hk)
	return strings.Join(h.out, "|"), th, err
}

func TestWorldUpdateEdges(t *testing.T) {
	tests := []struct {
		name, body string
		want       string
		err        string // part of the hook's error, "" for none
	}{
		{"fn gets nil for a missing key",
			`state.world.update("k", function(v) say(tostring(v)) return 1 end) say(state.world.get("k"))`,
			"say:nil|say:1", ""},
		{"fn gets a copy",
			`state.world.set("k", {n = 1})
			 state.world.update("k", function(v) v.n = 2 say(state.world.get("k").n) return v end)
			 say(state.world.get("k").n)`,
			"say:1|say:2", ""},
		{"fn returning nil removes the key",
			`state.world.set("k", "v")
			 say(tostring(state.world.update("k", function(v) return nil end)))
			 say(tostring(state.world.get("k")))`,
			"say:true|say:nil", ""},
		{"fn returning nothing removes the key",
			`state.world.set("k", "v") state.world.update("k", function() end) say(tostring(state.world.get("k")))`,
			"say:nil", ""},
		{"only the first return value counts",
			`state.world.update("k", function() return 1, 2 end) say(state.world.get("k"))`,
			"say:1", ""},
		{"fn may set other keys",
			`state.world.update("a", function() state.world.set("b", 2) return 1 end) say(state.world.get("a") + state.world.get("b"))`,
			"say:3", ""},
		{"set of the same key inside fn is overwritten",
			`state.world.update("k", function() state.world.set("k", "inner") return "outer" end) say(state.world.get("k"))`,
			"say:outer", ""},
		{"an error in fn keeps the old value",
			`state.world.set("k", "old")
			 local ok, err = pcall(state.world.update, "k", function() error("boom") end)
			 say(tostring(ok) .. " " .. tostring(state.world.get("k")) .. " " .. tostring(string.find(err, "boom", 1, true) ~= nil))`,
			"say:false old true", ""},
		{"an error object passes through",
			`local ok, err = pcall(state.world.update, "k", function() error({code = 7}) end) say(tostring(ok) .. " " .. type(err) .. " " .. tostring(err.code))`,
			"say:false table 7", ""},
		{"a question works again after a failed update",
			`pcall(state.world.update, "k", function() ui.dialog("?", {"a"}) end) say("then")`,
			"say:then", ""},
		{"an uncaught error in fn is the hook's",
			`state.world.update("k", function() error("boom") end)`,
			"", "boom"},
		{"fn returning a function",
			`state.world.update("k", function() return function() end end)`,
			"", ""},
		{"fn not a function", `state.world.update("k", 1)`, "", "function expected"},
		{"no fn", `state.world.update("k")`, "", "function expected"},
		{"empty key", `state.world.update("", function() return 1 end)`, "", "keys are 1 to 64 bytes"},
		{"long key", `state.world.update(string.rep("k", 65), function() return 1 end)`, "", "keys are 1 to 64 bytes"},
		{"nested update", `state.world.update("a", function() state.world.update("b", function() return 1 end) return 1 end)`, "", "inside another update"},
		{"nested update in pcall",
			`state.world.update("a", function() say(tostring(pcall(state.world.update, "b", function() return 1 end))) return 1 end) say(state.world.get("a"))`,
			"say:false|say:1", ""},
		{"question in fn", `state.world.update("k", function() ui.ask("?") return 1 end)`, "", "inside state.world.update"},
		{"select in fn", `state.world.update("k", function() ui.select("?", {"a"}) return 1 end)`, "", "inside state.world.update"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(DefaultLimits)
			defer r.Close()
			h := &recorder{}
			out, th, err := worldHook(t, r, h, Hook{Name: "on_enter"}, tt.body)
			if th != nil {
				t.Fatalf("the hook waits: %+v", th.Dialog)
			}
			if tt.want != "" {
				if err != nil || out != tt.want {
					t.Errorf("out = %q, err = %v, want %q", out, err, tt.want)
				}
				return
			}
			if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("err = %v, want a script error with %q", err, tt.err)
			}
			if r.Paused() != 0 {
				t.Errorf("%d paused", r.Paused())
			}
		})
	}
}

// A function that runs out of time in update ends the hook with
// ErrTimeout, pcall or not, and changes nothing.
func TestWorldUpdateTimeout(t *testing.T) {
	for _, body := range []string{
		`state.world.update("k", function() while true do end end)`,
		`pcall(state.world.update, "k", function() while true do end end) state.world.set("k", "after")`,
	} {
		r := New(Limits{Time: 50 * time.Millisecond, CallMemory: DefaultLimits.CallMemory, StateMemory: DefaultLimits.StateMemory})
		h := &recorder{}
		h.world.vals = map[string][]byte{}
		_, _, err := worldHook(t, r, h, Hook{Name: "on_enter"}, `state.world.set("k", "old") `+body)
		if !errors.Is(err, ErrTimeout) {
			t.Errorf("%s: err = %v, want ErrTimeout", body, err)
		}
		if out, _, err := worldHook(t, r, h, Hook{Name: "on_enter"}, `say(state.world.get("k"))`); err != nil || out != "say:old" {
			t.Errorf("%s: after the timeout k = %q, err = %v", body, out, err)
		}
		r.Close()
	}
}

// Writes inside update and update itself count toward MaxStateWrites,
// shared with state.player.
func TestWorldWritesAcrossUpdate(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"49 sets then an update",
			`for i = 1, 49 do state.world.set("k" .. i, i) end say(tostring(state.world.update("u", function() return 1 end)))`,
			"say:true"},
		{"50 sets then an update",
			`for i = 1, 50 do state.world.set("k" .. i, i) end local ok, err = state.world.update("u", function() return 1 end) say(tostring(ok) .. " " .. err)`,
			"say:nil state.world.update: more than 50 writes in one hook"},
		{"writes in fn count",
			`local ok, err = state.world.update("u", function() for i = 1, 50 do state.world.set("k" .. i, i) end return 1 end) say(tostring(ok) .. " " .. err)`,
			"say:nil state.world.update: more than 50 writes in one hook"},
		{"player and world share the limit",
			`for i = 1, 25 do state.player.set("p" .. i, i) state.world.set("w" .. i, i) end local ok, err = state.world.update("u", function() return 1 end) say(tostring(ok) .. " " .. err)`,
			"say:nil state.world.update: more than 50 writes in one hook"},
		{"a refused update keeps the old value",
			`state.world.set("u", "old") for i = 1, 49 do state.world.set("k" .. i, i) end state.world.update("u", function() return "new" end) say(state.world.get("u"))`,
			"say:old"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(DefaultLimits)
			defer r.Close()
			out, _, err := worldHook(t, r, &recorder{}, Hook{Name: "on_enter"}, tt.body)
			if err != nil || out != tt.want {
				t.Errorf("out = %q, err = %v, want %q", out, err, tt.want)
			}
		})
	}
}

// A hook's write count goes on across its questions.
func TestWorldWritesAcrossQuestions(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	h := &recorder{}
	_, th, err := worldHook(t, r, h, Hook{Name: "on_enter"}, `
for i = 1, 30 do state.world.set("a" .. i, i) end
ui.dialog("?", {"a"})
for i = 1, 30 do
  local ok, err = state.world.set("b" .. i, i)
  if not ok then say(i .. " " .. err) return end
end`)
	if err != nil || th == nil {
		t.Fatal(err)
	}
	if _, err := r.Resume(h, th, 1); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:21 state.world.set: more than 50 writes in one hook" {
		t.Errorf("out = %q", got)
	}
}

func TestWorldStateKeyLimit(t *testing.T) {
	r := New(Limits{Time: 10 * time.Second, CallMemory: DefaultLimits.CallMemory, StateMemory: DefaultLimits.StateMemory})
	defer r.Close()
	h := &recorder{}
	env := load(t, r, `function on_input(p, n)
  for i = 1, 50 do
    local ok, err = state.world.set("k" .. (n * 50 + i), i)
    if not ok then say(err) return end
  end
end`)
	for i := range MaxWorldStateKeys/50 + 1 {
		h.out = nil
		if _, err := r.Run(h, env, "on_input", "p", strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
		if i < MaxWorldStateKeys/50 && len(h.out) != 0 {
			t.Fatalf("hook %d: %q", i, h.out)
		}
	}
	if got := strings.Join(h.out, "|"); got != "say:state.world.set: more than 5000 keys" {
		t.Errorf("key 5001: %q", got)
	}
	if len(h.world.vals) != MaxWorldStateKeys {
		t.Errorf("%d keys kept", len(h.world.vals))
	}
	// Changing a kept key at the limit still works; so does removing one.
	h.out = nil
	if _, err := r.Run(h, load(t, r, `function on_enter() say(tostring(state.world.set("k1", "x"))) say(tostring(state.world.set("k2", nil))) say(tostring(state.world.set("new", 1))) end`), "on_enter", "p"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:true|say:true|say:true" {
		t.Errorf("at the limit: %q", got)
	}
}

// Changed hears every write that changed the state, and nothing else.
func TestWorldChangedCallback(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	var changed []string
	h := &recorder{}
	h.world = State{World: true, Changed: func(k string) { changed = append(changed, k) }}
	_, _, err := worldHook(t, r, h, Hook{Name: "on_enter"}, `
state.world.set("a", 1)
state.world.set("gone", nil) -- removing a missing key changes nothing
state.world.set("b", 1)
state.world.set("b", nil)
state.world.update("c", function() return 1 end)
state.world.update("missing", function() return nil end)
pcall(state.world.update, "d", function() error("no") end)
pcall(state.world.set, "e", function() end)
state.world.set("big", string.rep("x", 20000))
state.player.set("player", 1)`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "b", "c"}; !slices.Equal(changed, want) {
		t.Errorf("changed = %q, want %q", changed, want)
	}
}

// A Reserve that refuses keeps the value out and tells nobody.
func TestWorldReserveRefuses(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	var changed []string
	h := &recorder{}
	h.world = State{World: true, Changed: func(k string) { changed = append(changed, k) },
		Reserve: func(d int64) error {
			if d > 100 {
				return errors.New("this world is full right now")
			}
			return nil
		}}
	out, _, err := worldHook(t, r, h, Hook{Name: "on_enter"}, `
local ok, err = state.world.update("k", function() return string.rep("x", 200) end)
say(tostring(ok) .. " " .. err .. " " .. tostring(state.world.get("k")))`)
	if err != nil || out != "say:nil state.world.update: this world is full right now nil" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	if len(changed) != 0 {
		t.Errorf("changed = %q", changed)
	}
}

// ReadOnlyWorld refuses set and update, without calling fn, and without
// counting writes; get and state.player still work, also after a
// question.
func TestWorldReadOnlyHook(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	h := &recorder{}
	h.world.World = true
	h.world.vals = map[string][]byte{}
	if _, _, err := worldHook(t, r, h, Hook{Name: "on_enter"}, `state.world.set("door", "open")`); err != nil {
		t.Fatal(err)
	}
	out, th, err := worldHook(t, r, h, Hook{Name: "on_change", ReadOnlyWorld: true}, `
local called = false
local ok, err = state.world.set("door", "shut")
local ok2, err2 = state.world.update("door", function() called = true return "shut" end)
say(tostring(ok) .. " " .. err)
say(tostring(ok2) .. " " .. err2 .. " " .. tostring(called))
for i = 1, 50 do state.player.set("p", i) end -- the refused writes did not count
say(state.world.get("door"))
ui.dialog("?", {"a"})
say(tostring(state.world.set("door", "shut")))`)
	if err != nil || th == nil {
		t.Fatalf("err = %v, th = %v", err, th)
	}
	want := "say:nil state.world.set: on_change cannot change world state|" +
		"say:nil state.world.update: on_change cannot change world state false|say:open"
	if out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	h.out = nil
	if _, err := r.Resume(h, th, 1); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:nil" {
		t.Errorf("after the question: %q", got)
	}
}

// noWorld is a host without world state.
type noWorld struct{ recorder }

func (*noWorld) WorldState() *State { return nil }

func TestWorldStateMissing(t *testing.T) {
	for _, call := range []string{`state.world.get("k")`, `state.world.set("k", 1)`, `state.world.update("k", function() return 1 end)`} {
		r := New(DefaultLimits)
		env := load(t, r, `function on_enter() `+call+` end`)
		_, err := r.Run(&noWorld{}, env, "on_enter", "p")
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "keeps no world state") {
			t.Errorf("%s: err = %v", call, err)
		}
		_, err = r.Load("main.lua", call)
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "no player here") {
			t.Errorf("%s in the main chunk: err = %v", call, err)
		}
		r.Close()
	}
}

// Two hosts sharing one world State see each other's values.
func TestWorldSharedBetweenHosts(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	shared := &State{World: true}
	a, b := &sharedWorld{world: shared}, &sharedWorld{world: shared}
	env := load(t, r, `function on_input(p, text)
  if text == "take" then
    local mine = false
    state.world.update("crown", function(o) if o then return o end mine = true return p.name end)
    say(tostring(mine))
  else
    say(tostring(state.world.get("crown")))
  end
end`)
	for _, step := range []struct {
		h    *sharedWorld
		name string
		text string
		want string
	}{{a, "ann", "take", "say:true"}, {b, "bob", "take", "say:false"}, {b, "bob", "look", "say:ann"}, {a, "ann", "look", "say:ann"}} {
		step.h.out = nil
		if _, err := r.Run(step.h, env, "on_input", step.name, step.text); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(step.h.out, "|"); got != step.want {
			t.Errorf("%s %s: %q, want %q", step.name, step.text, got, step.want)
		}
	}
}

type sharedWorld struct {
	recorder
	world *State
}

func (h *sharedWorld) WorldState() *State { return h.world }
