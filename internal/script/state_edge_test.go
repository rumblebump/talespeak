package script

import (
	"errors"
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Edge cases of state.player (T-0023): size accounting, limits, number and
// string keys, fresh copies, writes per hook across a dialog, calls without
// a player, and a fuzzed value round trip. They go through the Lua API,
// State.Size, State.Reserve and the exported limits only, not through the
// encoding of kept values.

// stateRunner is one Runtime and Env with a recorder whose Reserve records
// every delta.
type stateRunner struct {
	t      *testing.T
	r      *Runtime
	env    *Env
	h      *recorder
	deltas []int64
	refuse bool // Reserve refuses growth
}

func newStateRunner(t *testing.T, src string) *stateRunner {
	t.Helper()
	sr := &stateRunner{t: t, r: New(stateLimits), h: &recorder{}}
	t.Cleanup(sr.r.Close)
	sr.env = load(t, sr.r, src)
	sr.h.state.Reserve = func(d int64) error {
		if sr.refuse && d > 0 {
			return errors.New("full")
		}
		sr.deltas = append(sr.deltas, d)
		return nil
	}
	return sr
}

// input runs on_input(p, text) and returns what it said.
func (sr *stateRunner) input(text string) string {
	sr.t.Helper()
	sr.h.out = nil
	if _, err := sr.r.Run(sr.h, sr.env, "on_input", "p", text); err != nil {
		sr.t.Fatalf("%s: %v", text, err)
	}
	return strings.Join(sr.h.out, "|")
}

func (sr *stateRunner) reserved() (n int64) {
	for _, d := range sr.deltas {
		n += d
	}
	return n
}

// checkReserved fails unless the deltas passed to Reserve add up to Size.
func (sr *stateRunner) checkReserved(step string) {
	sr.t.Helper()
	if got, size := sr.reserved(), sr.h.state.Size(); got != size {
		sr.t.Errorf("%s: Reserve deltas add up to %d, Size = %d", step, got, size)
	}
}

// TestStateReserveDeltas: overwrites and removals pass their change of
// size to Reserve, so the deltas add up to Size after every write and to
// zero once everything is removed. Writes the limits or Reserve refuse
// change nothing.
func TestStateReserveDeltas(t *testing.T) {
	sr := newStateRunner(t, `
function on_input(p, text)
  if text == "small" then assert(state.player.set("a", "x"))
  elseif text == "big" then assert(state.player.set("a", string.rep("x", 1000)))
  elseif text == "table" then assert(state.player.set("b", {1, 2, {c = "d"}}))
  elseif text == "same" then assert(state.player.set("b", {1, 2, {c = "d"}}))
  elseif text == "missing" then assert(state.player.set("missing", nil))
  elseif text == "refused" then
    local deep = {} local c = deep for i = 1, 9 do c.x = {} c = c.x end
    say(tostring(select(2, state.player.set("c", deep))))
    say(tostring(select(2, state.player.set("a", string.rep("y", 2000)))))
    say(tostring(select(2, state.player.set("new", 1))))
    say(string.len(state.player.get("a")))
  elseif text == "shrink" then assert(state.player.set("a", "x"))
  elseif text == "clear" then assert(state.player.set("a", nil)) assert(state.player.set("b", nil))
  end
end`)
	var last int64
	for _, tt := range []struct {
		step string
		grow int // 1 grows, -1 shrinks, 0 same size
	}{
		{"small", 1}, {"big", 1}, {"table", 1}, {"same", 0}, {"missing", 0}, {"small", -1},
	} {
		sr.input(tt.step)
		sr.checkReserved(tt.step)
		size := sr.h.state.Size()
		if (tt.grow > 0 && size <= last) || (tt.grow < 0 && size >= last) || (tt.grow == 0 && size != last) {
			t.Errorf("%s: Size %d after %d, want it to change by sign %d", tt.step, size, last, tt.grow)
		}
		last = size
	}
	sr.input("big")
	before, n := sr.h.state.Size(), len(sr.deltas)
	sr.refuse = true
	want := "say:state.player.set: tables nested deeper than " + strconv.Itoa(MaxStateDepth) + "|say:state.player.set: full|say:state.player.set: full|say:1000"
	if got := sr.input("refused"); got != want {
		t.Errorf("refused = %q, want %q", got, want)
	}
	if sr.h.state.Size() != before || len(sr.deltas) != n {
		t.Errorf("refused writes changed Size %d -> %d, Reserve deltas %v", before, sr.h.state.Size(), sr.deltas[n:])
	}
	sr.input("shrink") // shrinking passes while Reserve refuses growth
	sr.checkReserved("shrink")
	sr.refuse = false
	sr.input("clear")
	if sr.h.state.Size() != 0 || sr.reserved() != 0 {
		t.Errorf("after clear: Size = %d, reserved = %d", sr.h.state.Size(), sr.reserved())
	}
}

// TestStateBoundaries: a value well under each limit is kept, one past it
// is refused with nil and the reason.
func TestStateBoundaries(t *testing.T) {
	nest := `local function nest(n) local t = {} for i = 2, n do t = {t} end return t end`
	big := strconv.Itoa(MaxStateValueBytes)
	for _, tt := range []struct {
		name, pre, value, want string // want "" for true
	}{
		{"string under the value limit", "", `string.rep("x", ` + strconv.Itoa(MaxStateValueBytes-100) + `)`, ""},
		{"string of the value limit", "", `string.rep("x", ` + big + `)`, "value larger than " + big + " bytes"},
		{"string in a table past the limit", "", `{s = string.rep("x", ` + big + `)}`, "value larger than " + big + " bytes"},
		{"numbers past the limit", "", `(function() local t = {} for i = 1, ` + big + ` do t[i] = i end return t end)()`, "value larger than " + big + " bytes"},
		{"strings past the limit", "", `(function() local t = {} for i = 1, 400 do t["k" .. i] = string.rep("v", 40) end return t end)()`, "value larger than " + big + " bytes"},
		{"tables at the depth limit", nest, `nest(` + strconv.Itoa(MaxStateDepth) + `)`, ""},
		{"tables one past", nest, `nest(` + strconv.Itoa(MaxStateDepth+1) + `)`, "tables nested deeper than " + strconv.Itoa(MaxStateDepth)},
		{"a cycle", "", `(function() local t = {} t.self = t return t end)()`, "tables nested deeper than " + strconv.Itoa(MaxStateDepth)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runState(t, &recorder{}, tt.pre+"\nlocal ok, why = state.player.set('k', "+tt.value+")\nsay(tostring(ok) .. ' ' .. tostring(why))")
			want := "say:true nil"
			if tt.want != "" {
				want = "say:nil state.player.set: " + tt.want
			}
			if err != nil || out != want {
				t.Errorf("out = %q, err = %v, want %q", out, err, want)
			}
		})
	}
}

// TestStateSparseTable: a table with a few entries far apart, more slots
// than the limit on slots scanned, is refused: getting it back would build
// every array slot up to its largest index (gopher-lua appends one nil per
// slot). Only set runs here, since a get of such a value allocates the
// whole array.
func TestStateSparseTable(t *testing.T) {
	for _, build := range []string{
		`local t = {} t[200000] = true`,
		`local t = {1} t[200000] = 2`,
		`local t = {inner = {}} t.inner[200000] = "x"`,
	} {
		limits := DefaultLimits
		limits.Time = 10 * time.Second // building the table takes long with -race
		r := New(limits)
		h := &recorder{}
		env := load(t, r, "function on_enter()\n"+build+"\nsay(tostring(state.player.set('k', t)))\nend")
		_, err := r.Run(h, env, "on_enter", "p")
		r.Close()
		if out := strings.Join(h.out, "|"); err != nil || out != "say:nil" {
			t.Errorf("%s: out = %q, err = %v; want the sparse table refused", build, out, err)
		}
	}
}

// TestStateTotalBoundary fills one player's state with large values until a
// write is refused: it is refused only near MaxPlayerStateBytes, changes
// nothing, and a write that shrinks a value still passes.
func TestStateTotalBoundary(t *testing.T) {
	sr := newStateRunner(t, `
n = 0
function on_input(p, text)
  if text == "fill" then
    n = n + 1
    local ok, why = state.player.set("k" .. n, string.rep("x", `+strconv.Itoa(MaxStateValueBytes/2)+`))
    say(tostring(ok) .. " " .. tostring(why))
  elseif text == "shrink" then say(tostring(state.player.set("k1", "y")))
  else say(tostring(state.player.set("small", true))) end
end`)
	var refused string
	for i := 0; i < 2*MaxPlayerStateBytes/MaxStateValueBytes+2 && refused == ""; i++ {
		before := sr.h.state.Size()
		if got := sr.input("fill"); got != "say:true nil" {
			refused = got
			if sr.h.state.Size() != before {
				t.Errorf("refused write changed Size %d -> %d", before, sr.h.state.Size())
			}
		}
	}
	size := sr.h.state.Size()
	if !strings.Contains(refused, "player state larger than "+strconv.Itoa(MaxPlayerStateBytes)+" bytes") {
		t.Fatalf("refused = %q, Size = %d", refused, size)
	}
	if size > MaxPlayerStateBytes || size < MaxPlayerStateBytes-MaxStateValueBytes {
		t.Errorf("refused at Size %d, want within one value of %d", size, MaxPlayerStateBytes)
	}
	if got := sr.input("shrink"); got != "say:true" {
		t.Errorf("shrink = %q", got)
	}
	if got := sr.input("small"); got != "say:true" {
		t.Errorf("small after shrink = %q", got)
	}
	sr.checkReserved("full")
}

// TestStateKeyBoundary: a 64-byte key of any bytes is fine, also when it
// holds NULs or is not UTF-8; keys that differ in one byte are different.
func TestStateKeyBoundary(t *testing.T) {
	out, err := runState(t, &recorder{}, `
local k64 = string.rep("\255", `+strconv.Itoa(MaxStateKeyBytes-1)+`) .. "\0"
assert(state.player.set(k64, 1))
assert(state.player.set("a\0b", 2))
assert(state.player.set("a", 3))
assert(state.player.set("a\0", 4))
say(state.player.get(k64) .. state.player.get("a\0b") .. state.player.get("a") .. state.player.get("a\0") .. tostring(state.player.get("a\0c")))
local ok = pcall(state.player.set, k64 .. "x", 1)
say(tostring(ok))
ok = pcall(state.player.get, k64 .. "x")
say(tostring(ok))`)
	if err != nil || out != "say:1234nil|say:false|say:false" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// TestStateHugeTableStopsEarly: copying stops at the value limit, before
// it reaches a function placed after it.
func TestStateHugeTableStopsEarly(t *testing.T) {
	for _, tt := range []struct{ name, build string }{
		{"array", `local t = {} for i = 1, 20000 do t[i] = i end t[20001] = function() end`},
		{"nested", `local inner = {} for i = 1, 20000 do inner[i] = true end local t = {inner, function() end}`},
		{"shared", `local x = {} for i = 1, 100 do x[i] = i end local t = {} for i = 1, 1000 do t[i] = x end t[1001] = function() end`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runState(t, &recorder{}, tt.build+"\nsay(tostring(select(2, state.player.set('k', t))))")
			if err != nil || out != "say:state.player.set: value larger than "+strconv.Itoa(MaxStateValueBytes)+" bytes" {
				t.Errorf("out = %q, err = %v", out, err)
			}
		})
	}
}

// TestStateHugeGoTable: a table of a million entries, more than a call
// could build in its time, is refused at the value limit within the time
// of one call, before the function at its end.
func TestStateHugeGoTable(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter() say(tostring(select(2, state.player.set("k", huge)))) end`)
	tb := r.L.CreateTable(1<<20, 0)
	for i := 1; i <= 1<<20; i++ {
		tb.RawSetInt(i, lua.LNumber(i))
	}
	tb.RawSetInt(1<<20+1, r.L.NewFunction(func(*lua.LState) int { return 0 }))
	env.table.RawSetString("huge", tb)
	h := &recorder{}
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil || len(h.out) != 1 || !strings.HasPrefix(h.out[0], "say:state.player.set: ") {
		t.Errorf("out = %q, err = %v", h.out, err)
	}
}

// TestStateNumbers: integer and float keys stay apart, a float key with an
// integer value is that integer, -0 as a key is 0, -0 as a value keeps its
// sign, and large and tiny numbers survive.
func TestStateNumbers(t *testing.T) {
	out, err := runState(t, &recorder{}, `
local nz = 0 / -1
local inf = 1 / 0
assert(1 / nz == -inf)
local t = {[1] = "one", [2] = "two", [2.5] = "half", [-3] = "neg", [0] = "zero", [1e15] = "big", [2^53] = "pow", ["1"] = "str", [0.1] = "tenth"}
assert(state.player.set("k", t))
local g = state.player.get("k")
say(g[1] .. g[2] .. g[2.5] .. g[-3] .. g[0] .. g[nz] .. g[1e15] .. g[2^53] .. g["1"] .. g[0.1] .. #g .. g[4 / 2])
assert(state.player.set("nz", nz))
say(tostring(1 / state.player.get("nz") == -inf))
assert(state.player.set("v", {nz, 2^53 + 2, -1e308, 5e-324, math.huge, 0.1 + 0.2}))
local v = state.player.get("v")
say(tostring(1 / v[1] == -inf) .. tostring(v[2] == 2^53 + 2) .. tostring(v[3] == -1e308) .. tostring(v[4] == 5e-324) .. tostring(v[5] == math.huge) .. tostring(v[6] == 0.1 + 0.2))
local n = 0 for k in pairs(state.player.get("k")) do n = n + 1 end
say(n)`)
	if err != nil || out != "say:onetwohalfnegzerozerobigpowstrtenth2two|say:true|say:truetruetruetruetruetrue|say:9" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	for _, body := range []string{
		`state.player.set("k", 1/0)`,
		`state.player.set("k", -1/0)`,
		`state.player.set("k", {[1/0] = 1})`,
		`state.player.set("k", {x = {y = 0/0}})`,
	} {
		if _, err := runState(t, &recorder{}, body); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "numbers must be finite") {
			t.Errorf("%s: err = %v", body, err)
		}
	}
}

// TestStateStringBytes: strings keep every byte, NULs and invalid UTF-8
// included, as values, table keys and table values.
func TestStateStringBytes(t *testing.T) {
	out, err := runState(t, &recorder{}, `
local s = "a\0b\255\254" .. string.char(0xC3, 0x28) .. "\0"
assert(state.player.set("v", s))
assert(state.player.set("t", {[s] = s, [s .. "x"] = 1, list = {s, "\0", ""}}))
local g, t = state.player.get("v"), state.player.get("t")
say(tostring(g == s) .. " " .. #g .. " " .. tostring(t[s] == s) .. " " .. t[s .. "x"] .. " " .. tostring(t.list[1] == s) .. " " .. #t.list[2] .. " " .. #t.list[3] .. " " .. #t.list)`)
	if err != nil || out != "say:true 8 true 1 true 1 0 3" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// TestStateGetIsFresh: every get returns a new table, nested tables too,
// with no metatable, and changing one changes neither the state nor other
// copies. A table's metatable is not kept, and no metamethod runs.
func TestStateGetIsFresh(t *testing.T) {
	out, err := runState(t, &recorder{}, `
assert(state.player.set("t", {inner = {1}}))
local a, b = state.player.get("t"), state.player.get("t")
assert(a ~= b and a.inner ~= b.inner, "fresh tables")
a.inner[1] = 99 a.extra = true
local c = state.player.get("t")
say(c.inner[1] .. " " .. tostring(c.extra) .. " " .. b.inner[1])
local x = {1}
assert(state.player.set("dag", {x, x}))
local d = state.player.get("dag")
say(tostring(d[1] ~= d[2]) .. " " .. d[1][1] .. d[2][1])
local calls = 0
local m = setmetatable({a = 1}, {__index = function() calls = calls + 1 return 5 end, __newindex = function() calls = calls + 1 end, __len = function() calls = calls + 1 return 0 end})
assert(state.player.set("m", m))
local g = state.player.get("m")
say(tostring(getmetatable(g)) .. " " .. tostring(g.b) .. " " .. g.a .. " " .. calls)
assert(state.player.set("lib", math))
say(tostring(next(state.player.get("lib"))))`)
	if err != nil || out != "say:1 nil 1|say:true 11|say:nil nil 1 0|say:nil" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// TestStateWritesPerHook: one hook gets 50 writes, also when it waits in
// ui.dialog in between; the next hook gets 50 again. Refused writes and
// removals count, reads do not.
func TestStateWritesPerHook(t *testing.T) {
	sr := newStateRunner(t, `
function on_enter()
  for i = 1, `+strconv.Itoa(MaxStateWrites-1)+` do assert(state.player.set("k", i)) end
  assert(state.player.set("gone", nil))
  say(tostring(select(2, state.player.set("k", 0))))
  for i = 1, 1000 do state.player.get("k") end
  ui.dialog("?", {"x"})
  say(tostring(select(2, state.player.set("k", 0))))
  say(state.player.get("k"))
end
function on_input()
  for i = 1, `+strconv.Itoa(MaxStateWrites)+` do assert(state.player.set("k", 100 + i)) end
  say(tostring(select(2, state.player.set("k", 0))))
end`)
	refused := "say:state.player.set: more than " + strconv.Itoa(MaxStateWrites) + " writes"
	th, err := sr.r.Run(sr.h, sr.env, "on_enter", "p")
	if err != nil || th == nil || len(sr.h.out) != 1 || !strings.HasPrefix(sr.h.out[0], refused) {
		t.Fatalf("before the dialog: out = %q, th = %v, err = %v", sr.h.out, th, err)
	}
	sr.h.out = nil
	th, err = sr.r.Resume(sr.h, th, 1)
	if err != nil || th != nil || len(sr.h.out) != 2 || !strings.HasPrefix(sr.h.out[0], refused) || sr.h.out[1] != "say:"+strconv.Itoa(MaxStateWrites-1) {
		t.Errorf("after the dialog, the same hook: out = %q, th = %v, err = %v", sr.h.out, th, err)
	}
	if got := sr.input("x"); !strings.HasPrefix(got, refused) || strings.Count(got, "|") != 0 {
		t.Errorf("next hook = %q", got)
	}
}

// noStateHost is a host without player state.
type noStateHost struct{ recorder }

func (*noStateHost) PlayerState() *State { return nil }

// TestStateWithoutPlayer: a host without state, and a hook whose player
// left (Cancel), get script errors and change nothing.
func TestStateWithoutPlayer(t *testing.T) {
	for _, body := range []string{`state.player.get("k")`, `state.player.set("k", 1)`} {
		r := New(DefaultLimits)
		env := load(t, r, "function on_enter() "+body+" end")
		if _, err := r.Run(&noStateHost{}, env, "on_enter", "p"); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "keeps no player state") {
			t.Errorf("%s: err = %v", body, err)
		}
		r.Close()
	}

	sr := newStateRunner(t, `
function on_enter()
  assert(state.player.set("before", 1))
  pcall(ui.dialog, "?", {"x"})
  local ok, why = pcall(state.player.set, "after", 1)
  left = tostring(ok) .. " " .. tostring(why)
  pcall(state.player.set, "before", nil)
end
function on_input() say(left .. " " .. tostring(state.player.get("before")) .. " " .. tostring(state.player.get("after"))) end`)
	th, err := sr.r.Run(sr.h, sr.env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatalf("th = %v, err = %v", th, err)
	}
	size := sr.h.state.Size()
	sr.r.Cancel(th)
	if sr.h.state.Size() != size || size == 0 {
		t.Errorf("Size %d -> %d after the player left", size, sr.h.state.Size())
	}
	if got := sr.input("x"); !strings.HasPrefix(got, "say:false") || !strings.Contains(got, "no player here") || !strings.HasSuffix(got, " 1 nil") {
		t.Errorf("out = %q", got)
	}
	sr.checkReserved("cancel")
}

// TestStateSizeCoversMemory: Size is what state.player counts toward the
// world's and the address's memory, so it must not be far below what the
// kept values hold. A full player state of empty tables is the worst case.
func TestStateSizeCoversMemory(t *testing.T) {
	sr := newStateRunner(t, `
function on_input(p, key)
  local n = 4096
  while n >= 1 do
    local t = {} for i = 1, n do t[i] = {} end
    if state.player.set(key, t) then say("kept") return end
    n = math.floor(n / 2)
  end
  say("full")
end`)
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	before := ms.HeapAlloc
	for i := 0; i < MaxPlayerStateKeys; i++ {
		if sr.input("k"+strconv.Itoa(i)) != "say:kept" {
			break
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&ms)
	real := int64(ms.HeapAlloc) - int64(before)
	runtime.KeepAlive(sr.h)
	counted := sr.h.state.Size()
	if counted < MaxPlayerStateBytes/2 {
		t.Fatalf("filled only %d bytes", counted)
	}
	if real > 2*counted {
		t.Errorf("a player state counted as %d bytes holds about %d bytes of heap (%.1fx)", counted, real, float64(real)/float64(counted))
	}
}

// fuzzValue builds a Lua value from fuzz bytes: every kind of value,
// tables nested past the limit, shared and cyclic tables, functions,
// boolean and table keys, and numbers from raw bits (NaN, Inf, -0).
type fuzzValue struct {
	L      *lua.LState
	b      []byte
	tables []*lua.LTable
	nodes  int
}

func (d *fuzzValue) next() byte {
	if len(d.b) == 0 {
		return 0
	}
	c := d.b[0]
	d.b = d.b[1:]
	return c
}

func (d *fuzzValue) value(depth int) lua.LValue {
	if d.nodes++; d.nodes > 4000 || depth > 12 {
		return lua.LNil
	}
	c := d.next()
	switch c % 9 {
	case 1:
		return lua.LTrue
	case 2:
		return lua.LFalse
	case 3:
		var u uint64
		for range 8 {
			u = u<<8 | uint64(d.next())
		}
		return lua.LNumber(math.Float64frombits(u))
	case 4:
		return lua.LNumber(int8(d.next()))
	case 5:
		n := int(d.next())
		if c&0x80 != 0 {
			n *= 80 // up to 20400 bytes, past the value limit
		}
		s := make([]byte, n)
		for i := range s {
			s[i] = 'x'
		}
		copy(s, d.b)
		return lua.LString(s)
	case 6:
		n := int(d.next() % 8)
		if c&0x80 != 0 {
			n *= 200
		}
		t := d.L.NewTable()
		d.tables = append(d.tables, t)
		for range n {
			k, v := d.value(depth+1), d.value(depth+1)
			if k == lua.LNil || v == lua.LNil {
				continue
			}
			if f, ok := k.(lua.LNumber); ok {
				if math.IsNaN(float64(f)) || (f == lua.LNumber(math.Trunc(float64(f))) && f > 4096 && f < lua.LNumber(lua.MaxArrayIndex)) {
					continue // NaN keys cannot exist; big array indexes allocate in gopher-lua itself
				}
			}
			t.RawSet(k, v)
		}
		return t
	case 7:
		return d.L.NewFunction(func(*lua.LState) int { return 0 })
	case 8:
		if len(d.tables) > 0 {
			return d.tables[int(d.next())%len(d.tables)]
		}
	}
	return lua.LNil
}

// sameValue compares Lua values by content; numbers by value and sign.
func sameValue(a, b lua.LValue) bool {
	switch a := a.(type) {
	case lua.LNumber:
		bn, ok := b.(lua.LNumber)
		return ok && a == bn && math.Signbit(float64(a)) == math.Signbit(float64(bn))
	case *lua.LTable:
		bt, ok := b.(*lua.LTable)
		if !ok {
			return false
		}
		na, nb, same := 0, 0, true
		a.ForEach(func(k, v lua.LValue) {
			na++
			same = same && sameValue(v, bt.RawGet(k))
		})
		bt.ForEach(func(lua.LValue, lua.LValue) { nb++ })
		return same && na == nb && bt.Metatable == lua.LNil
	default:
		return a == b
	}
}

// valueRules is the oracle of the T-0018 value rules, without the size:
// valid is false for a function, a non-finite number, a key that is not a
// string or number, or tables nested deeper than MaxStateDepth. small is
// set when v is well within any encoding of the value limit: few bytes and
// no integer key far from 1.
func valueRules(v lua.LValue) (valid, small bool) {
	var bytes int
	maxKey := lua.LNumber(0)
	var walk func(v lua.LValue, depth int) bool
	walk = func(v lua.LValue, depth int) bool {
		switch v := v.(type) {
		case *lua.LNilType, lua.LBool:
			bytes++
			return true
		case lua.LNumber:
			bytes += 8
			return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
		case lua.LString:
			bytes += len(v) + 2
			return true
		case *lua.LTable:
			if depth >= MaxStateDepth {
				return false
			}
			bytes += 2
			ok := true
			v.ForEach(func(k, val lua.LValue) {
				if !ok {
					return
				}
				switch k := k.(type) {
				case lua.LString:
				case lua.LNumber:
					maxKey = max(maxKey, k, -k)
				default:
					ok = false
					return
				}
				ok = walk(k, depth+1) && walk(val, depth+1) && bytes <= 4*MaxStateValueBytes
			})
			return ok
		}
		return false
	}
	valid = walk(v, 0)
	return valid, valid && bytes <= 1024 && maxKey <= 64
}

// FuzzStateValue sets a fuzzed value through state.player.set and checks
// the rules: a value they forbid is never kept, a small valid one always
// is, a kept one comes back equal and fresh, refusals change nothing, and
// Reserve always sees what Size counts.
func FuzzStateValue(f *testing.F) {
	for _, seed := range [][]byte{
		{1}, {2}, {0}, {4, 0xff}, {3, 0x80, 0, 0, 0, 0, 0, 0, 0}, {3, 0x7f, 0xf8, 0, 0, 0, 0, 0, 1},
		{5, 3, 'a', 0, 0xff}, {0x85, 205}, {6, 2, 5, 1, 'k', 4, 7, 4, 1, 1},
		{6, 1, 4, 1, 8, 0}, {6, 1, 1, 4, 1}, {6, 1, 4, 1, 7}, {0x86, 7, 4, 1, 4, 2},
		{6, 1, 4, 1, 6, 1, 4, 1, 6, 1, 4, 1, 6, 1, 4, 1, 6, 1, 4, 1, 6, 1, 4, 1, 6, 1, 4, 1, 6, 1, 4, 1, 6, 0},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r := New(Limits{Time: time.Second, CallMemory: 64 << 20, StateMemory: 64 << 20})
		defer r.Close()
		env := load(t, r, `
function on_enter()
  local ok, a, b = pcall(state.player.set, "k", v)
  if not ok then result = "error" why = a
  elseif a then result = "kept" got = state.player.get("k") again = state.player.get("k")
  else result = "refused" why = b end
end
function on_input() assert(state.player.set("k", nil)) end`)
		d := &fuzzValue{L: r.L, b: data}
		v := d.value(0)
		env.table.RawSetString("v", v)
		h := &recorder{}
		var reserved int64
		h.state.Reserve = func(n int64) error { reserved += n; return nil }
		if _, err := r.Run(h, env, "on_enter", "p"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		valid, small := valueRules(v)
		result := env.table.RawGetString("result").String()
		why := env.table.RawGetString("why").String()
		switch result {
		case "kept":
			if !valid {
				t.Fatalf("kept a value the rules forbid")
			}
			got, again := env.table.RawGetString("got"), env.table.RawGetString("again")
			if !sameValue(v, got) || !sameValue(v, again) {
				t.Fatalf("get gave back another value")
			}
			if gt, ok := got.(*lua.LTable); ok && gt == again {
				t.Fatalf("two gets returned the same table")
			}
			if size := h.state.Size(); size > MaxPlayerStateBytes || (v != lua.LNil && size <= 0) {
				t.Fatalf("Size = %d", size)
			}
		case "refused":
			if small {
				t.Fatalf("refused a small valid value: %s", why)
			}
			if !strings.HasPrefix(why, "state.player.set: ") || h.state.Size() != 0 {
				t.Fatalf("refused with %q, Size = %d", why, h.state.Size())
			}
		case "error":
			if valid {
				t.Fatalf("script error for a valid value: %s", why)
			}
			if h.state.Size() != 0 {
				t.Fatalf("Size = %d after an error", h.state.Size())
			}
		default:
			t.Fatalf("result = %q", result)
		}
		if reserved != h.state.Size() {
			t.Fatalf("reserved %d, Size %d", reserved, h.state.Size())
		}
		if _, err := r.Run(h, env, "on_input", "p", "x"); err != nil || h.state.Size() != 0 || reserved != 0 {
			t.Fatalf("after removing: err = %v, Size = %d, reserved = %d", err, h.state.Size(), reserved)
		}
	})
}

// TestRuntimeReserve: Reserve shares the world's StateMemory with the
// Instances. Growth past it is refused with ErrFull and changes nothing,
// shrinking always passes, and while Reserve holds the world full a new
// Instance is refused.
func TestRuntimeReserve(t *testing.T) {
	const limit = 1 << 20
	r := New(Limits{Time: time.Second, CallMemory: 64 << 20, StateMemory: limit})
	defer r.Close()
	if err := r.Reserve(limit); err != nil {
		t.Fatalf("Reserve(limit) = %v", err)
	}
	if err := r.Reserve(1); !errors.Is(err, ErrFull) {
		t.Errorf("Reserve past the limit = %v", err)
	}
	if err := r.Reserve(-limit / 2); err != nil {
		t.Errorf("shrinking = %v", err)
	}
	if err := r.Reserve(limit / 2); err != nil {
		t.Errorf("Reserve back to the limit = %v; a refused Reserve changed the count", err)
	}
	if _, err := r.Load("pad.lua", `pad = string.rep("p", 100000)`); !errors.Is(err, ErrFull) {
		t.Errorf("Instance while Reserve fills the world = %v", err)
	}
	if err := r.Reserve(-limit); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Load("pad.lua", `pad = string.rep("p", 100000)`); err != nil {
		t.Errorf("Instance after freeing = %v", err)
	}
	if err := r.Reserve(limit); !errors.Is(err, ErrFull) {
		t.Errorf("Reserve(limit) with an Instance kept = %v", err)
	}
}
