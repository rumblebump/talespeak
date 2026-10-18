package script

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type recorder struct {
	out    []string
	state  State
	timers map[string]time.Duration
}

func (h *recorder) Say(text string, how Voice) {
	switch {
	case how.Hide:
		text = "(spoken only) " + text
	case how.Speak:
		text = "(spoken) " + text
	}
	if how.From != "" {
		text = "[" + how.From + "] " + text
	}
	h.out = append(h.out, "say:"+text)
}
func (h *recorder) DescribeRoom() { h.out = append(h.out, "describe") }

// Go knows two rooms, cell and hall; Room is "cell".
func (h *recorder) Go(room string) bool {
	if room != "cell" && room != "hall" {
		return false
	}
	h.out = append(h.out, "go:"+room)
	return true
}

func (h *recorder) Room() string { return "cell" }

func (h *recorder) PlayerState() *State { return &h.state }

// StartTimer keeps at most MaxTimers; a timer's time never passes.
func (h *recorder) StartTimer(name string, d time.Duration, show string) error {
	if _, ok := h.timers[name]; !ok && len(h.timers) >= MaxTimers {
		return errors.New("too many timers")
	}
	if h.timers == nil {
		h.timers = map[string]time.Duration{}
	}
	h.timers[name] = d
	h.out = append(h.out, "timer:"+name+"="+d.String()+"|"+show)
	return nil
}

func (h *recorder) StopTimer(name string) bool {
	_, ok := h.timers[name]
	delete(h.timers, name)
	return ok
}

func (h *recorder) TimerLeft(name string) (time.Duration, bool) {
	d, ok := h.timers[name]
	return d, ok
}

func (h *recorder) Items(items []Item) {
	s := "items:"
	for _, it := range items {
		s += it.ID + "=" + it.Label + ";"
	}
	h.out = append(h.out, s)
}

// Image knows one image, map.png.
func (h *recorder) Image(name, caption string) bool {
	if name != "map.png" {
		return false
	}
	h.out = append(h.out, "image:"+name+"|"+caption)
	return true
}

func load(t *testing.T, r *Runtime, src string) *Env {
	t.Helper()
	env, err := r.Load("test.lua", src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return env
}

func TestDialogRoundTrip(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `
function on_enter(player)
  room.describe()
  local n, label = ui.dialog("Hello " .. player.name, {"Left", "Right"})
  say("picked " .. n .. " " .. label)
  local m = ui.dialog("Again?", {"Yes"})
  say("then " .. m)
end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "Ada")
	if err != nil || th == nil {
		t.Fatalf("Run = %v, %v; want a paused thread", th, err)
	}
	if th.Dialog.Title != "Hello Ada" || strings.Join(th.Dialog.Options, ",") != "Left,Right" {
		t.Fatalf("dialog = %+v", th.Dialog)
	}
	th, err = r.Resume(h, th, 2)
	if err != nil || th == nil || th.Dialog.Title != "Again?" {
		t.Fatalf("Resume = %+v, %v; want the second dialog", th, err)
	}
	th, err = r.Resume(h, th, 1)
	if err != nil || th != nil {
		t.Fatalf("Resume = %v, %v; want finished", th, err)
	}
	want := "describe|say:picked 2 Right|say:then 1"
	if got := strings.Join(h.out, "|"); got != want {
		t.Errorf("outputs = %q, want %q", got, want)
	}
}

func TestResumeRejectsChoiceOutOfRange(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter() ui.dialog("?", {"a", "b"}) end`)
	th, err := r.Run(&recorder{}, env, "on_enter", "p")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []int{0, 3, -1} {
		if _, err := r.Resume(&recorder{}, th, c); err == nil {
			t.Errorf("Resume(%d) = nil error", c)
		}
	}
}

func TestMissingHookIsNoOp(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `x = 1`)
	if th, err := r.Run(&recorder{}, env, "on_enter", "p"); th != nil || err != nil {
		t.Errorf("Run = %v, %v", th, err)
	}
}

func TestEnvsAreSeparate(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	a := load(t, r, `secret = "a"; function on_enter() say(secret) end`)
	b := load(t, r, `function on_enter() say(tostring(secret)) end`)
	h := &recorder{}
	if _, err := r.Run(h, a, "on_enter", "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(h, b, "on_enter", "p"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:a|say:nil" {
		t.Errorf("outputs = %q", got)
	}
}

func TestLimits(t *testing.T) {
	limits := Limits{Time: 50 * time.Millisecond, CallMemory: 16 << 20, StateMemory: 64 << 20}
	tests := []struct {
		name string
		src  string
		want error
	}{
		{"endless loop", `while true do end`, ErrTimeout},
		{"loop catching the timeout", `while true do pcall(function() while true do end end) end`, ErrTimeout},
		{"recursion", `local function f() return 1 + f() end f()`, ErrScript},
		{"string.rep", `string.rep("x", 1e10)`, ErrScript},
		{"method rep", `("xy"):rep(1e9)`, ErrScript},
		{"format width", `string.format("%999999d", 1)`, ErrScript},
		{"format precision", `string.format("%.500f", 1)`, ErrScript},
		{"table.concat", `local s = string.rep("x", 1e6) local t = {} for i = 1, 100 do t[i] = s end table.concat(t)`, ErrScript},
		{"unpack", `unpack({}, 1, 1e8)`, ErrScript},
		{"doubling string", `local s = "x" for i = 1, 40 do s = s .. s end`, ErrScript},
		{"wide concat", `local s = string.rep("x", 1e6) local t = s .. s .. s`, ErrScript},
		{"string.byte", `string.byte(string.rep("x", 1e6), 1, -1)`, ErrScript},
		{"gsub", `string.gsub("aaa", "a", "b")`, ErrScript},
		{"find pattern", `string.find("aaa", "a+")`, ErrScript},
		{"say outside a hook", `say("hi")`, ErrScript},
		{"dialog outside a hook", `ui.dialog("?", {"a"})`, ErrScript},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := limits
			if tt.want != ErrTimeout {
				l.Time = 5 * time.Second // so a slow race-detector run cannot time out first
			}
			r := New(l)
			defer r.Close()
			start := time.Now()
			_, err := r.Load("bad.lua", tt.src)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Load = %v, want %v", err, tt.want)
			}
			if d := time.Since(start); d > time.Second {
				t.Errorf("took %v", d)
			}
		})
	}
}

func TestCallMemory(t *testing.T) {
	// A long time limit, so the memory budget is what stops these.
	limits := Limits{Time: 5 * time.Second, CallMemory: 16 << 20, StateMemory: 64 << 20}
	for name, src := range map[string]string{
		"growing table": `local t = {} for i = 1, 1e9 do t[i] = i end`,
		"many strings":  `local t = {} for i = 1, 1e9 do t[i] = "x" .. i end`,
	} {
		t.Run(name, func(t *testing.T) {
			r := New(limits)
			defer r.Close()
			if _, err := r.Load("bad.lua", src); !errors.Is(err, ErrMemory) {
				t.Fatalf("Load = %v, want ErrMemory", err)
			}
		})
	}
}

func TestStateMemoryAcrossCalls(t *testing.T) {
	r := New(Limits{Time: time.Second, CallMemory: 64 << 20, StateMemory: 8 << 20})
	defer r.Close()
	env := load(t, r, `keep = {} function on_enter() local s = string.rep("x", 1e6) .. tostring(#keep) keep[#keep + 1] = s end`)
	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, err = r.Run(&recorder{}, env, "on_enter", "p")
	}
	if !errors.Is(err, ErrStateMemory) {
		t.Fatalf("err = %v, want ErrStateMemory", err)
	}
	if _, err := r.Run(&recorder{}, env, "on_enter", "p"); !errors.Is(err, ErrStateMemory) {
		t.Errorf("next call err = %v, want ErrStateMemory", err)
	}
}

func TestSandboxHidesUnsafeGlobals(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	names := []string{"io", "os", "debug", "package", "channel", "coroutine",
		"collectgarbage", "dofile", "getfenv", "load", "loadfile", "loadstring",
		"module", "newproxy", "print", "setfenv", "_printregs"}
	// require is the sandbox's own: it runs only the pack's lib modules
	// (require_test.go), never Lua's package loader.
	for _, name := range names {
		env := load(t, r, `function on_enter() say(type(`+name+`)) end`)
		h := &recorder{}
		if _, err := r.Run(h, env, "on_enter", "p"); err != nil {
			t.Fatal(err)
		}
		if h.out[0] != "say:nil" {
			t.Errorf("%s is %s, want nil", name, h.out[0])
		}
	}
}

func TestPlainFindWorks(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter() say(tostring(string.find("a.b", ".", 1, true))) say(string.format("%5.2f", 1.5)) say(table.concat({"a", 1}, "-")) end`)
	h := &recorder{}
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, "|"); got != "say:2|say: 1.50|say:a-1" {
		t.Errorf("outputs = %q", got)
	}
}
