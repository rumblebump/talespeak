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
				if len(th.Dialog.Options) < 1 || len(th.Dialog.Options) > MaxDialogOptions {
					t.Fatalf("dialog with %d options", len(th.Dialog.Options))
				}
				th, err = r.Resume(h, th, 1)
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
