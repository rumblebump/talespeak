package script

import (
	"errors"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// T-0024: what gopher-lua keeps in a table that ForEach does not show.

// Empty array slots count: filling an array and clearing it leaves the
// slots, so an Instance doing it again and again must break.
func TestRetainedCountsEmptyArraySlots(t *testing.T) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	tbl := L.NewTable()
	for i := 1; i <= 100000; i++ {
		tbl.RawSetInt(i, lua.LTrue)
	}
	for i := 1; i <= 100000; i++ {
		tbl.RawSetInt(i, lua.LNil)
	}
	if got := retained([]lua.LValue{tbl}, nil, 1<<40); got < 100000*sizeSlot {
		t.Errorf("retained = %d, want the 100000 empty slots counted", got)
	}

	r := New(Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 1 << 20, StateMemory: 64 << 20})
	defer r.Close()
	env := load(t, r, `t = {} m = 0
function on_enter() m = m + 20000 t[m] = true t[m] = nil end`)
	for i := range 20 {
		if _, err := r.Run(&recorder{}, env, "on_enter", "p"); errors.Is(err, ErrStateMemory) {
			return
		} else if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	t.Errorf("20 calls keeping 20000 empty slots each, past PlayerMemory: kept %d, no ErrStateMemory", r.Kept(env))
}

// Removed hash keys stay in gopher-lua's key list with what they hold:
// long strings and tables used as keys must count.
func TestRetainedCountsRemovedKeys(t *testing.T) {
	for _, tt := range []struct{ name, src string }{
		{"string keys", `local k = string.rep("x", 100000) .. n t[k] = true t[k] = nil`},
		{"table keys", `local k = {string.rep("x", 100000) .. n} t[k] = true t[k] = nil`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := New(Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 1 << 20, StateMemory: 64 << 20})
			defer r.Close()
			env := load(t, r, `t = {} n = 0
function on_enter() for i = 1, 4 do n = n + 1 `+tt.src+` end end`)
			for i := range 10 {
				_, err := r.Run(&recorder{}, env, "on_enter", "p")
				if errors.Is(err, ErrStateMemory) {
					return
				}
				if err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
			}
			t.Errorf("40 removed keys of 100 KB: kept %d, no ErrStateMemory", r.Kept(env))
		})
	}
}

// A write far past the array's end goes to the hash part at once, instead
// of filling up to 1 GiB of empty slots in one Go loop.
func TestHugeArrayIndex(t *testing.T) {
	for _, expr := range []string{"6e7", "2^26 - 1", "2^31", tostr(MaxArrayIndex)} {
		start := time.Now()
		out, _, err := runHook(t, DefaultLimits, `function on_enter() local t = {} t[`+expr+`] = true say(tostring(t[`+expr+`]) .. " " .. #t) end`)
		if err != nil || len(out) != 1 || out[0] != "say:true 0" {
			t.Errorf("t[%s]: out = %q, err = %v", expr, out, err)
		}
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Errorf("t[%s] took %v", expr, d)
		}
	}
	// Just below the limit the gap is filled, at most 4 MiB.
	out, _, err := runHook(t, roomy, `function on_enter() local t = {} t[`+tostr(MaxArrayIndex-1)+`] = true say(#t) end`)
	if err != nil || len(out) != 1 || out[0] != "say:"+tostr(MaxArrayIndex-1) {
		t.Errorf("just below the limit: out = %q, err = %v", out, err)
	}
}

// table.insert refuses to grow a list past MaxArrayIndex-1 entries, where
// gopher-lua would keep entries no read finds.
func TestTableInsertLimit(t *testing.T) {
	for _, call := range []string{"table.insert(t, true)", "table.insert(t, 1, true)"} {
		_, _, err := runHook(t, roomy, `function on_enter() local t = {} t[`+tostr(MaxArrayIndex-1)+`] = true `+call+` end`)
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "at most") {
			t.Errorf("%s on a full list: err = %v", call, err)
		}
	}
	out, _, err := runHook(t, DefaultLimits, `function on_enter() local t = {} for i = 1, 1000 do table.insert(t, i) end table.insert(t, 1, 0) say(#t .. " " .. t[1] .. " " .. t[1001]) end`)
	if err != nil || len(out) != 1 || out[0] != "say:1001 0 1000" {
		t.Errorf("ordinary inserts: out = %q, err = %v", out, err)
	}
}

func tostr(n int) string { return lua.LNumber(n).String() }
