package script

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// stateLimits are the default limits with a long time limit: the state
// tests build tables of thousands of entries, which under -race can take
// longer than the default 100 ms on a busy runner.
var stateLimits = func() Limits { l := DefaultLimits; l.Time = 10 * time.Second; return l }()

// runState runs on_enter(src body) with a fresh recorder and returns what
// it said and the error.
func runState(t *testing.T, h *recorder, body string) (string, error) {
	t.Helper()
	r := New(stateLimits)
	defer r.Close()
	env := load(t, r, "function on_enter(player)\n"+body+"\nend")
	h.out = nil
	_, err := r.Run(h, env, "on_enter", "p")
	return strings.Join(h.out, "|"), err
}

func TestStatePlayerRoundTrip(t *testing.T) {
	h := &recorder{}
	out, err := runState(t, h, `
local t = {n = 3, list = {"a", "b"}, [2] = true, deep = {x = {y = 1.5}}}
assert(state.player.set("bag", t) == true)
t.n = 99 -- a copy was kept
local got = state.player.get("bag")
got.n = 42 -- and a copy is returned
local again = state.player.get("bag")
say(again.n .. " " .. again.list[2] .. " " .. tostring(again[2]) .. " " .. again.deep.x.y)
say(tostring(state.player.get("nothing")))
state.player.set("bag", nil)
say(tostring(state.player.get("bag")))`)
	if err != nil || out != "say:3 b true 1.5|say:nil|say:nil" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	if h.state.Size() != 0 || len(h.state.vals) != 0 {
		t.Errorf("state after removing = %d bytes, %d keys", h.state.Size(), len(h.state.vals))
	}
}

// Limits refuse a write with nil and a reason. Each body ends with the
// write that is refused, as ok, why.
func TestStatePlayerLimits(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"value size", `ok, why = state.player.set("k", string.rep("x", 16 * 1024))`, "value larger than 16384 bytes"},
		{"value size in a table", `local t = {} for i = 1, 3000 do t[i] = i end ok, why = state.player.set("k", t)`, "value larger than 16384 bytes"},
		{"depth", `local t = {} local c = t for i = 1, 8 do c.x = {} c = c.x end ok, why = state.player.set("k", t)`, "nested deeper than 8"},
		{"cycle", `local t = {} t.self = t ok, why = state.player.set("k", t)`, "nested deeper than 8"},
		{"total", `local big = string.rep("x", 16000) for i = 1, 16 do assert(state.player.set("k" .. i, big)) end ok, why = state.player.set("last", big)`, "larger than 262144 bytes"},
		{"writes", `for i = 1, 50 do state.player.set("k", i) end ok, why = state.player.set("k", 51)`, "more than 50 writes in one hook"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runState(t, &recorder{}, tt.body+"\nsay(tostring(ok) .. ' ' .. tostring(why))")
			if err != nil || !strings.HasPrefix(out, "say:nil state.player.set: ") || !strings.Contains(out, tt.want) {
				t.Errorf("out = %q, err = %v, want %q", out, err, tt.want)
			}
		})
	}
}

// 500 keys over several calls; the 501st is refused, changing a key is not.
func TestStatePlayerKeyLimit(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `
n = 0
function on_enter()
  for i = 1, 50 do n = n + 1 assert(state.player.set("k" .. n, n)) end
end
function on_input()
  assert(state.player.set("k1", "changed"))
  local ok, why = state.player.set("one more", 1)
  say(tostring(ok) .. " " .. why)
end`)
	h := &recorder{}
	for range 10 {
		if _, err := r.Run(h, env, "on_enter", "p"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Run(h, env, "on_input", "p", "x"); err != nil || h.out[len(h.out)-1] != "say:nil state.player.set: more than 500 keys" {
		t.Errorf("out = %q, err = %v", h.out, err)
	}
}

// Values that can never be kept, bad keys and calls outside a hook are
// script errors.
func TestStatePlayerErrors(t *testing.T) {
	for _, tt := range []struct{ body, want string }{
		{`state.player.set("k", function() end)`, "function values cannot be kept"},
		{`state.player.set("k", {f = function() end})`, "function values cannot be kept"},
		{`state.player.set("k", 0/0)`, "numbers must be finite"},
		{`state.player.set("k", {[true] = 1})`, "keys must be strings or numbers"},
		{`state.player.set("", 1)`, "keys are 1 to 64 bytes"},
		{`state.player.get(string.rep("k", 65))`, "keys are 1 to 64 bytes"},
		{`state.player.set({}, 1)`, "string expected"},
		{`state.player = {}`, "read-only"},
	} {
		h := &recorder{}
		if _, err := runState(t, h, tt.body); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.body, err, tt.want)
		}
	}
	r := New(DefaultLimits)
	defer r.Close()
	if _, err := r.Load("top.lua", `state.player.set("k", 1)`); err == nil || !strings.Contains(err.Error(), "no player here") {
		t.Errorf("top level: %v", err)
	}
}

func TestStatePlayerReserve(t *testing.T) {
	h := &recorder{}
	var reserved int64
	h.state.Reserve = func(delta int64) error {
		if reserved+delta > 100 {
			return errors.New("world full")
		}
		reserved += delta
		return nil
	}
	out, err := runState(t, h, `
assert(state.player.set("a", string.rep("x", 50)))
say(tostring(select(2, state.player.set("b", string.rep("x", 50)))))
assert(state.player.set("a", "short"))
assert(state.player.set("a", nil))`)
	if err != nil || out != "say:state.player.set: world full" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	if reserved != 0 || h.state.Size() != 0 {
		t.Errorf("reserved = %d, size = %d, want 0", reserved, h.state.Size())
	}
}
