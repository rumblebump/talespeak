package script

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// State.Encoded and State.Load (T-0034): what a world with a store writes
// and reads back. Load holds stored values to the limits a script's writes
// are held to.

// encodedAll returns every key's encoding of s, as a store keeps them.
func encodedAll(t *testing.T, s *State) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for k := range s.vals {
		v, ok := s.Encoded(k)
		if !ok {
			t.Fatalf("Encoded(%q) missing", k)
		}
		out[k] = append([]byte(nil), v...)
	}
	return out
}

// Values set by a script, encoded, loaded into a new State and read by a
// script come back equal; Size counts the same.
func TestStateLoadRoundTrip(t *testing.T) {
	h := &recorder{}
	if out, err := runState(t, h, `
assert(state.player.set("bag", {n = 3, list = {"a", "b"}, deep = {x = {y = 1.5}}, [7] = false}))
assert(state.player.set("gold", -12))
assert(state.player.set("name", "Mira"))
assert(state.player.set("flag", true))`); err != nil {
		t.Fatalf("set: %q, %v", out, err)
	}
	if _, ok := h.state.Encoded("missing"); ok {
		t.Error("Encoded(missing) reports a value")
	}
	vals := encodedAll(t, &h.state)
	if len(vals) != 4 {
		t.Fatalf("encoded %d keys, want 4", len(vals))
	}

	h2 := &recorder{}
	var reserved int64
	h2.state.Reserve = func(d int64) error { reserved += d; return nil }
	if err := h2.state.Load(vals); err != nil {
		t.Fatal(err)
	}
	if h2.state.Size() != h.state.Size() || reserved != h.state.Size() {
		t.Errorf("Size after Load = %d, reserved %d; want %d", h2.state.Size(), reserved, h.state.Size())
	}
	// Load copies: changing the map's bytes later changes nothing.
	for _, v := range vals {
		for i := range v {
			v[i] = 0xff
		}
	}
	r := New(stateLimits)
	defer r.Close()
	env := load(t, r, `function on_enter()
local b = state.player.get("bag")
say(b.n .. " " .. b.list[2] .. " " .. b.deep.x.y .. " " .. tostring(b[7]) .. " " .. state.player.get("gold") .. " " ..
  state.player.get("name") .. " " .. tostring(state.player.get("flag")) .. " " .. tostring(state.player.get("missing")))
end`)
	if _, err := r.Run(h2, env, "on_enter", "p"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h2.out, "|"); got != "say:3 b 1.5 false -12 Mira true nil" {
		t.Errorf("after Load: %q", got)
	}
}

// Load replaces what the State kept and passes the change of size to
// Reserve; an empty or nil map empties it.
func TestStateLoadReplaces(t *testing.T) {
	var s State
	var deltas []int64
	s.Reserve = func(d int64) error { deltas = append(deltas, d); return nil }
	if err := s.Load(map[string][]byte{"a": {tagTrue}, "bb": {tagString, 1, 'x'}}); err != nil {
		t.Fatal(err)
	}
	if s.Size() != 1+1+2+3 {
		t.Errorf("Size = %d, want 7", s.Size())
	}
	if err := s.Load(map[string][]byte{"c": {tagFalse}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Encoded("a"); ok || s.Size() != 2 {
		t.Errorf("after the second Load: a kept = %v, Size = %d", ok, s.Size())
	}
	if err := s.Load(nil); err != nil || s.Size() != 0 {
		t.Errorf("Load(nil) = %v, Size = %d", err, s.Size())
	}
	if want := []int64{7, -5, -2}; fmt.Sprint(deltas) != fmt.Sprint(want) {
		t.Errorf("Reserve deltas = %v, want %v", deltas, want)
	}
}

// Stored values past the limits are refused, and the State keeps what it
// had; values at the limits load.
func TestStateLoadBounds(t *testing.T) {
	big := func(n int) []byte { return append([]byte{tagString}, bytes.Repeat([]byte{'x'}, n-1)...) }
	keys := func(n int) map[string][]byte {
		m := map[string][]byte{}
		for i := range n {
			m[fmt.Sprint("k", i)] = []byte{tagTrue}
		}
		return m
	}
	values := func(n, size int) map[string][]byte {
		m := map[string][]byte{}
		for i := range n {
			m[fmt.Sprintf("k%02d", i)] = big(size)
		}
		return m
	}
	tests := []struct {
		name  string
		world bool
		vals  map[string][]byte
		want  string // "" loads
	}{
		{"player keys at the limit", false, keys(MaxPlayerStateKeys), ""},
		{"player keys past the limit", false, keys(MaxPlayerStateKeys + 1), "more than 500 keys"},
		{"world keys at the limit", true, keys(MaxWorldStateKeys), ""},
		{"world keys past the limit", true, keys(MaxWorldStateKeys + 1), "more than 5000 keys"},
		{"empty key", false, map[string][]byte{"": {tagTrue}}, "out of bounds"},
		{"key at the limit", false, map[string][]byte{strings.Repeat("k", MaxStateKeyBytes): {tagTrue}}, ""},
		{"key past the limit", false, map[string][]byte{strings.Repeat("k", MaxStateKeyBytes+1): {tagTrue}}, "out of bounds"},
		{"nil value", false, map[string][]byte{"k": nil}, "out of bounds"},
		{"empty value", false, map[string][]byte{"k": {}}, "out of bounds"},
		{"value at the limit", false, map[string][]byte{"k": big(MaxStateValueBytes)}, ""},
		{"value past the limit", false, map[string][]byte{"k": big(MaxStateValueBytes + 1)}, "out of bounds"},
		{"player total under the limit", false, values(15, MaxStateValueBytes), ""},
		{"player total past the limit", false, values(16, MaxStateValueBytes), "larger than 262144 bytes"},
		{"world total under its limit", true, values(16, MaxStateValueBytes), ""},
		{"world total past its limit", true, values(4<<20/MaxStateValueBytes, MaxStateValueBytes), "larger than 4194304 bytes"},
		{"world name in the message", true, map[string][]byte{"": {tagTrue}}, "stored world state"},
		{"player name in the message", false, map[string][]byte{"": {tagTrue}}, "stored player state"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := State{World: tt.world}
			reserveCalls := 0
			s.Reserve = func(int64) error { reserveCalls++; return nil }
			if err := s.Load(map[string][]byte{"before": {tagTrue}}); err != nil {
				t.Fatal(err)
			}
			reserveCalls = 0
			err := s.Load(tt.vals)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Load = %v, want it loaded", err)
				}
				if len(s.vals) != len(tt.vals) {
					t.Errorf("loaded %d keys, want %d", len(s.vals), len(tt.vals))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load = %v, want %q", err, tt.want)
			}
			if _, ok := s.Encoded("before"); !ok || len(s.vals) != 1 || s.Size() != int64(len("before")+1) || reserveCalls != 0 {
				t.Errorf("a refused Load changed the State: %d keys, Size %d, Reserve called %d times", len(s.vals), s.Size(), reserveCalls)
			}
		})
	}
}

// A Reserve that refuses (the world's script memory is full) refuses the
// Load, which then changes nothing.
func TestStateLoadReserveRefuses(t *testing.T) {
	full := errors.New("full")
	s := State{Reserve: func(d int64) error {
		if d > 0 {
			return full
		}
		return nil
	}}
	if err := s.Load(map[string][]byte{"k": {tagTrue}}); !errors.Is(err, full) {
		t.Fatalf("Load = %v, want the Reserve error", err)
	}
	if s.Size() != 0 || len(s.vals) != 0 {
		t.Errorf("refused Load kept %d keys, Size %d", len(s.vals), s.Size())
	}
}

// A value that is damaged inside loads, and fails as a script error when a
// script gets it; the other keys still work.
func TestStateLoadDamagedValue(t *testing.T) {
	for _, damaged := range [][]byte{
		{0xee},                                  // unknown tag
		{tagString, 5, 'a'},                     // string longer than its bytes
		{tagTable, tagInt, 2},                   // table without its end
		{tagTrue, tagTrue},                      // bytes after the value
		{tagFloat, 1, 2, 3},                     // short float
		{tagInt, 0x80},                          // cut varint
		{tagTable, tagTable, tagEnd, 2, tagEnd}, // a table as a key
	} {
		h := &recorder{}
		if err := h.state.Load(map[string][]byte{"bad": damaged, "ok": {tagTrue}}); err != nil {
			t.Fatalf("Load(%v) = %v", damaged, err)
		}
		r := New(stateLimits)
		env := load(t, r, `function on_enter() say(tostring(state.player.get("ok"))) state.player.get("bad") end`)
		_, err := r.Run(h, env, "on_enter", "p")
		r.Close()
		if fmt.Sprint(h.out) != "[say:true]" {
			t.Errorf("%v: the good key = %v", damaged, h.out)
		}
		if damaged[0] == tagTable && damaged[1] == tagTable {
			// A table key is not something the encoder writes, but RawSet
			// takes it; either answer is fine as long as nothing panics.
			continue
		}
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "damaged") {
			t.Errorf("get of %v: err = %v, want a damaged value script error", damaged, err)
		}
	}
}

// FuzzStateLoad loads random bytes as a stored value and gets it from a
// script: it never panics or hangs, and either decodes or is a script error
// naming the damage.
func FuzzStateLoad(f *testing.F) {
	for _, seed := range [][]byte{
		{tagTrue}, {tagInt, 6}, {tagString, 2, 'h', 'i'}, {tagTable, tagInt, 2, tagTrue, tagEnd},
		{tagTable, tagInt, 0xfe, 0xff, 0xff, 0xff, 0x0f, tagTrue, tagEnd}, // a huge array index
		{tagTable, tagTable, tagTable, tagTable, tagTable, tagTable, tagTable, tagTable, tagTable, tagTable},
		{tagFloat, 0, 0, 0, 0, 0, 0, 0xf8, 0x7f}, // NaN
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		h := &recorder{}
		err := h.state.Load(map[string][]byte{"k": data})
		if len(data) == 0 || len(data) > MaxStateValueBytes {
			if err == nil {
				t.Fatalf("Load took a value of %d bytes", len(data))
			}
			return
		}
		if err != nil {
			t.Fatalf("Load = %v", err)
		}
		r := New(Limits{Time: time.Second, CallMemory: 64 << 20, StateMemory: 64 << 20})
		defer r.Close()
		env := load(t, r, `function on_enter() local v = state.player.get("k") say(type(v)) end`)
		if _, err := r.Run(h, env, "on_enter", "p"); err != nil {
			if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "damaged") {
				t.Fatalf("get: %v", err)
			}
		}
	})
}
