package script

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Inside a hook, Reserve compares the cheap bound only and never walks the
// world on the caller's budgets (security re-review). The check after the
// call measures, so the next hook's write succeeds.
func TestReserveInHookComparesTheBound(t *testing.T) {
	const limit = 4 << 20
	r := New(Limits{Time: time.Second, CallMemory: 32 << 20, StateMemory: limit})
	defer r.Close()
	env := load(t, r, `function on_enter() local ok, why = state.player.set("k", "v") say(tostring(why)) end`)
	h := &recorder{}
	h.state.Reserve = r.Reserve
	// What a call allocated and the world has not measured yet.
	st := r.envs[env.table]
	st.sinceWalk += limit
	r.total += limit

	if _, err := r.Run(h, env, "on_enter", "p"); err != nil {
		t.Fatalf("first hook: %v", err)
	}
	if got := strings.Join(h.out, "|"); !strings.Contains(got, "state.player.set: ") || !strings.Contains(got, ErrFull.Error()) {
		t.Errorf("first hook said %q, want the refusal from the bound", got)
	}
	if r.total > limit {
		t.Errorf("after the call total = %d: the check after it did not measure", r.total)
	}
	h.out = nil
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil || h.out[0] != "say:nil" {
		t.Errorf("second hook: out = %q, err = %v", h.out, err)
	}
	if h.state.Size() == 0 {
		t.Error("the second write was not kept")
	}
}

// A damaged encoding cannot make get fill a huge array (security
// re-review): integer keys past the scan budget are refused before
// gopher-lua grows the array, and so are many arrays that add up.
func TestDecodeRefusesLargeArrays(t *testing.T) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	table := func(keys ...float64) []byte {
		b := []byte{tagTable}
		for _, k := range keys {
			if k == float64(int64(k)) {
				b = append(b, tagInt)
				b = binary.AppendVarint(b, int64(k))
			} else {
				b = append(b, tagFloat)
				b = binary.LittleEndian.AppendUint64(b, mathBits(k))
			}
			b = append(b, tagTrue)
		}
		return append(b, tagEnd)
	}
	for _, tt := range []struct {
		name string
		b    []byte
		ok   bool
	}{
		{"at the budget", table(maxStateScan), true},
		{"past the budget", table(maxStateScan + 1), false},
		{"near the array limit", table(MaxArrayIndex - 1), false},
		{"past the array limit, in the hash part", table(1<<26 - 1), true},
		{"hash part", table(1 << 30), true},
		{"fraction", table(maxStateScan + 0.5), true},
		{"arrays adding up", append(append([]byte{tagTable, tagInt, 2}, table(maxStateScan)...), append([]byte{tagInt, 4}, append(table(maxStateScan), tagEnd)...)...), false},
	} {
		start := time.Now()
		d := decoder{L: L}
		_, rest, err := d.value(tt.b, 0)
		if tt.ok && (err != nil || len(rest) != 0) || !tt.ok && !errors.Is(err, errDamaged) {
			t.Errorf("%s: rest %d, err = %v", tt.name, len(rest), err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s took %v", tt.name, d)
		}
	}
}

func mathBits(f float64) uint64 { return math.Float64bits(f) }
