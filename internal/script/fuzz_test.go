package script

import (
	"errors"
	"testing"
	"time"
)

// FuzzLoad runs outside Lua: loading and running it must end with a result
// or one of the documented errors, never a panic or a runaway call.
func FuzzLoad(f *testing.F) {
	for _, src := range []string{
		`function on_enter(p) room.describe() local n, l = ui.dialog("Hi " .. p.name, {"a", "b"}) say(l) end`,
		`function on_enter() while true do ui.dialog("?", {"x"}) end end`,
		`x = string.format("%5.2f", 1) .. table.concat({1, 2}, ",") .. tostring(unpack({1}))`,
		`local t = {} for i = 1, 100 do t[i] = string.rep("x", i) end`,
		`function on_enter() say(("a"):rep(10):upper():sub(2, 3)) end`,
		`error({})`,
		`return`,
		`function on_enter() local a = ui.ask("?", {strict = true}) say(words.normalize(a)) end`,
		`function on_enter() local n, l = ui.select("?", {"a", "b", "c"}, {min = 2}) say(table.concat(l, ",")) end`,
		`function on_enter() ui.items({{id = "a", label = "b"}}) ui.image("map.png", "c") say("x", {speak = true, show = false}) end`,
		`function on_enter() timer.start("g", 1.5, {show = "Go"}) say(tostring(timer.left("g"))) timer.cancel("g") local n = ui.dialog("?", {"a"}, {timeout = 2}) end`,
		`function on_enter() state.world.update("k", function(v) state.world.set("j", v) return {n = (v and v.n or 0) + 1} end) say(state.world.get("k").n) end`,
		`local t = {} t[2^18] = 1 table.insert(t, 1) function on_enter() pcall(state.world.update, "k", function() ui.ask("?") end) end`,
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		r := New(Limits{Time: 20 * time.Millisecond, CallMemory: 16 << 20, StateMemory: 32 << 20})
		defer r.Close()
		known := func(err error) bool {
			return err == nil || errors.Is(err, ErrScript) || errors.Is(err, ErrTimeout) || errors.Is(err, ErrMemory) || errors.Is(err, ErrStateMemory)
		}
		start := time.Now()
		env, err := r.Load("fuzz.lua", src)
		if !known(err) {
			t.Fatalf("Load = %v", err)
		}
		if err == nil {
			h := &recorder{}
			th, err := r.Run(h, env, "on_enter", "p")
			for i := 0; th != nil && i < 3; i++ {
				th, err = answerAny(t, r, h, th)
			}
			if !known(err) {
				t.Fatalf("Run = %v", err)
			}
			if len(h.out) > 4*(MaxOutputs+1) {
				t.Fatalf("%d outputs", len(h.out))
			}
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("took %v", d)
		}
	})
}

// answerAny checks the dialog th waits on and answers it with something
// it accepts: a typed word for ui.ask, the first Min options (at least
// one) for ui.select, option 1 for ui.dialog.
func answerAny(t *testing.T, r *Runtime, h Host, th *Thread) (*Thread, error) {
	t.Helper()
	d := th.Dialog
	if d.Ask {
		if len(d.Options) != 0 || d.Multiple || d.Title == "" || len(d.Title) > MaxTitleBytes {
			t.Fatalf("ask dialog %+v", d)
		}
		return r.Answer(h, th, "x")
	}
	if len(d.Options) < 1 || len(d.Options) > MaxDialogOptions {
		t.Fatalf("dialog with %d options", len(d.Options))
	}
	if !d.Multiple {
		return r.Resume(h, th, 1)
	}
	if d.Min < 0 || d.Max < 1 || d.Min > d.Max || d.Max > len(d.Options) {
		t.Fatalf("select dialog %+v", d)
	}
	choices := []int{1}
	for c := 2; c <= d.Min; c++ {
		choices = append(choices, c)
	}
	return r.Resume(h, th, choices...)
}
