package script

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// T-0029: timer.start, timer.cancel, timer.left, question timeouts, Expire
// and the options of RunHook, against the recorder host.

func TestTimerStartArguments(t *testing.T) {
	tests := []struct {
		name, call string
		want       string // what the recorder saw, or "" for an error
	}{
		{"plain", `timer.start("guards", 60)`, "timer:guards=1m0s|"},
		{"shown", `timer.start("guards", 5, {show = "Hurry"})`, "timer:guards=5s|Hurry"},
		{"fraction", `timer.start("g", 1.5)`, "timer:g=1.5s|"},
		{"shortest", `timer.start("g", 1)`, "timer:g=1s|"},
		{"longest", `timer.start("g", 86400)`, "timer:g=24h0m0s|"},
		{"show nil", `timer.start("g", 2, {show = nil})`, "timer:g=2s|"},
		{"options nil", `timer.start("g", 2, nil)`, "timer:g=2s|"},
		{"too short", `timer.start("g", 0.999)`, ""},
		{"zero", `timer.start("g", 0)`, ""},
		{"negative", `timer.start("g", -5)`, ""},
		{"too long", `timer.start("g", 86400.5)`, ""},
		{"nan", `timer.start("g", 0/0)`, ""},
		{"infinite", `timer.start("g", math.huge)`, ""},
		{"minus infinite", `timer.start("g", -math.huge)`, ""},
		{"string seconds", `timer.start("g", "5")`, ""},
		{"no seconds", `timer.start("g")`, ""},
		{"empty name", `timer.start("", 5)`, ""},
		{"upper case name", `timer.start("Guards", 5)`, ""},
		{"long name", `timer.start("` + strings.Repeat("a", 65) + `", 5)`, ""},
		{"name with a space", `timer.start("a b", 5)`, ""},
		{"table name", `timer.start({}, 5)`, ""},
		{"show not a string", `timer.start("g", 5, {show = 1})`, ""},
		{"show empty", `timer.start("g", 5, {show = ""})`, ""},
		{"show blank", `timer.start("g", 5, {show = "   "})`, ""},
		{"show two lines", `timer.start("g", 5, {show = "a\nb"})`, ""},
		{"show too long", `timer.start("g", 5, {show = string.rep("x", 101)})`, ""},
		{"show bad utf-8", `timer.start("g", 5, {show = "\255"})`, ""},
		{"options not a table", `timer.start("g", 5, "show")`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := runHook(t, DefaultLimits, `function on_enter() `+tt.call+` end`)
			if tt.want == "" {
				if !errors.Is(err, ErrScript) {
					t.Errorf("err = %v, out = %q, want a script error", err, out)
				}
				if len(out) != 0 {
					t.Errorf("a refused timer reached the host: %q", out)
				}
				return
			}
			if err != nil || len(out) != 1 || out[0] != tt.want {
				t.Errorf("out = %q, err = %v, want %q", out, err, tt.want)
			}
		})
	}
}

func TestTimerCancelAndLeft(t *testing.T) {
	out, _, err := runHook(t, DefaultLimits, `function on_enter()
  say(tostring(timer.left("g")) .. " " .. tostring(timer.cancel("g")))
  timer.start("g", 90)
  say(tostring(timer.left("g")))
  say(tostring(timer.cancel("g")) .. " " .. tostring(timer.cancel("g")) .. " " .. tostring(timer.left("g")))
end`)
	want := []string{"say:nil false", "timer:g=1m30s|", "say:90", "say:true false nil"}
	if err != nil || strings.Join(out, ";") != strings.Join(want, ";") {
		t.Errorf("out = %q, err = %v", out, err)
	}
	for _, call := range []string{`timer.cancel("")`, `timer.left("X")`, `timer.cancel()`, `timer.left(1/0)`} {
		if _, _, err := runHook(t, DefaultLimits, `function on_enter() `+call+` end`); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v", call, err)
		}
	}
}

// timer.left never reports a negative time: a timer whose deadline just
// passed but has not fired yet has 0 seconds left.
func TestTimerLeftNeverNegative(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter() say(tostring(timer.left("late"))) end`)
	h := &lateTimers{}
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.out, ";") != "say:0" {
		t.Errorf("out = %q", h.out)
	}
}

// lateTimers has one timer, late, whose deadline passed a second ago.
type lateTimers struct{ recorder }

func (h *lateTimers) TimerLeft(name string) (time.Duration, bool) {
	return -time.Second, name == "late"
}

// The host refusing a timer is not an error: timer.start returns nil and
// the reason.
func TestTimerStartRefused(t *testing.T) {
	out, _, err := runHook(t, DefaultLimits, `function on_enter()
  for i = 1, 11 do
    local ok, why = timer.start("t" .. i, 10)
    if not ok then say(why) end
  end
  local ok = timer.start("t1", 20) -- replacing a running one is not refused
  say(tostring(ok))
end`)
	if err != nil || len(out) != 13 || out[10] != "say:timer.start: too many timers" || out[11] != "timer:t1=20s|" || out[12] != "say:true" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// Timers need a player: the main chunk has none.
func TestTimerOutsideHook(t *testing.T) {
	for _, call := range []string{`timer.start("g", 5)`, `timer.cancel("g")`, `timer.left("g")`} {
		r := New(DefaultLimits)
		_, err := r.Load("main.lua", call)
		r.Close()
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "no player here") {
			t.Errorf("%s in the main chunk: err = %v", call, err)
		}
	}
}

// A shown timer is output, so it counts toward MaxOutputs; a secret one
// is not.
func TestTimerShownCountsAsOutput(t *testing.T) {
	for _, tt := range []struct {
		show string
		ok   bool
	}{{`{show = "x"}`, false}, {`nil`, true}} {
		src := strings.ReplaceAll(`function on_enter() for i = 1, 101 do timer.start("t", 5, SHOW) end end`, "SHOW", tt.show)
		_, _, err := runHook(t, DefaultLimits, src)
		if (err == nil) != tt.ok {
			t.Errorf("101 timer.start with %s: err = %v", tt.show, err)
		}
	}
}

func TestQuestionTimeoutArguments(t *testing.T) {
	tests := []struct {
		name, call string
		want       time.Duration // 0: refused
	}{
		{"dialog", `ui.dialog("?", {"a"}, {timeout = 30})`, 30 * time.Second},
		{"dialog fraction", `ui.dialog("?", {"a"}, {timeout = 2.5})`, 2500 * time.Millisecond},
		{"dialog with from", `ui.dialog("?", {"a"}, {timeout = 1, from = "Guide"})`, time.Second},
		{"select", `ui.select("?", {"a", "b"}, {timeout = 86400})`, 24 * time.Hour},
		{"ask", `ui.ask("?", {timeout = 10})`, 10 * time.Second},
		{"dialog zero", `ui.dialog("?", {"a"}, {timeout = 0})`, 0},
		{"dialog too short", `ui.dialog("?", {"a"}, {timeout = 0.5})`, 0},
		{"dialog too long", `ui.dialog("?", {"a"}, {timeout = 86401})`, 0},
		{"dialog nan", `ui.dialog("?", {"a"}, {timeout = 0/0})`, 0},
		{"dialog string", `ui.dialog("?", {"a"}, {timeout = "30"})`, 0},
		{"select negative", `ui.select("?", {"a"}, {timeout = -1})`, 0},
		{"ask infinite", `ui.ask("?", {timeout = math.huge})`, 0},
		{"ask table", `ui.ask("?", {timeout = {}})`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, th, err := runHook(t, DefaultLimits, `function on_enter() `+tt.call+` end`)
			if tt.want == 0 {
				if !errors.Is(err, ErrScript) || th != nil {
					t.Errorf("err = %v, th = %v, want a script error", err, th)
				}
				return
			}
			if err != nil || th == nil || th.Dialog.Timeout != tt.want {
				t.Fatalf("err = %v, th = %+v, want a question with timeout %v", err, th, tt.want)
			}
		})
	}
	// Without a timeout, a question has none.
	if _, th, err := runHook(t, DefaultLimits, `function on_enter() ui.dialog("?", {"a"}, {from = "Guide"}) end`); err != nil || th.Dialog.Timeout != 0 {
		t.Errorf("no timeout: err = %v, th = %+v", err, th)
	}
}

// Expire makes the open question return nil, for each kind of question,
// and the hook goes on: it can ask again.
func TestExpireReturnsNil(t *testing.T) {
	tests := []struct{ name, ask, want string }{
		{"dialog", `local n, l = ui.dialog("?", {"a"}, {timeout = 5}) say(tostring(n) .. " " .. tostring(l))`, "say:nil nil"},
		{"select", `local n, l = ui.select("?", {"a", "b"}, {timeout = 5}) say(tostring(n) .. " " .. tostring(l))`, "say:nil nil"},
		{"ask", `local a, b = ui.ask("?", {timeout = 5}) say(tostring(a) .. " " .. tostring(b) .. " " .. select("#", ui.ask("again?", {timeout = 1})))`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(DefaultLimits)
			defer r.Close()
			env := load(t, r, `function on_enter() `+tt.ask+` local n = ui.dialog("Next?", {"x"}) say("next " .. n) end`)
			h := &recorder{}
			th, err := r.Run(h, env, "on_enter", "p")
			if err != nil || th == nil {
				t.Fatalf("Run: %v", err)
			}
			th, err = r.Expire(h, th)
			if tt.name == "ask" {
				// The second ui.ask waits now; it expires as well and returns
				// one nil.
				if err != nil || th == nil || !th.Dialog.Ask {
					t.Fatalf("after the first expiry: th = %+v, err = %v", th, err)
				}
				th, err = r.Expire(h, th)
				tt.want = "say:nil nil 1"
			}
			if err != nil || th == nil || th.Dialog.Title != "Next?" {
				t.Fatalf("after Expire: th = %+v, err = %v", th, err)
			}
			if len(h.out) == 0 || h.out[len(h.out)-1] != tt.want {
				t.Errorf("out = %q, want %q last", h.out, tt.want)
			}
			if _, err := r.Resume(h, th, 1); err != nil {
				t.Fatal(err)
			}
			if h.out[len(h.out)-1] != "say:next 1" {
				t.Errorf("after the second question: %q", h.out)
			}
			if r.Paused() != 0 {
				t.Errorf("%d hooks paused", r.Paused())
			}
		})
	}
}

func TestExpireRefuses(t *testing.T) {
	r := New(DefaultLimits)
	env := load(t, r, `function on_enter() local n = ui.dialog("?", {"a"}) say("got " .. n) end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(err)
	}
	// A question without a timeout cannot expire, and stays answerable.
	if _, err := r.Expire(h, th); !errors.Is(err, ErrAnswer) {
		t.Errorf("Expire without a timeout: %v, want ErrAnswer", err)
	}
	if r.Paused() != 1 {
		t.Fatalf("the refused Expire ended the hook")
	}
	if _, err := r.Resume(h, th, 1); err != nil || h.out[len(h.out)-1] != "say:got 1" {
		t.Fatalf("Resume after the refused Expire: %v, out %q", err, h.out)
	}
	// A hook that is no longer waiting cannot expire.
	if _, err := r.Expire(h, th); !errors.Is(err, ErrScript) {
		t.Errorf("Expire of a finished hook: %v, want ErrScript", err)
	}

	env2 := load(t, r, `function on_enter() ui.dialog("?", {"a"}, {timeout = 2}) end`)
	th2, err := r.Run(h, env2, "on_enter", "p")
	if err != nil || th2 == nil {
		t.Fatal(err)
	}
	r.Cancel(th2)
	if _, err := r.Expire(h, th2); !errors.Is(err, ErrScript) {
		t.Errorf("Expire of a canceled hook: %v, want ErrScript", err)
	}
	th3, err := r.Run(h, env2, "on_enter", "p")
	if err != nil || th3 == nil {
		t.Fatal(err)
	}
	r.Close()
	if _, err := r.Expire(h, th3); !errors.Is(err, ErrScript) {
		t.Errorf("Expire after Close: %v, want ErrScript", err)
	}
}

// After Expire, the hook runs under a new call's time limit, and a hook
// that then runs too long is a timeout like after an answer.
func TestExpireThenTimeout(t *testing.T) {
	r := New(Limits{Time: 50 * time.Millisecond, CallMemory: DefaultLimits.CallMemory, StateMemory: DefaultLimits.StateMemory})
	defer r.Close()
	env := load(t, r, `function on_enter() local n = ui.dialog("?", {"a"}, {timeout = 1}) if n == nil then while true do end end end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(err)
	}
	if _, err := r.Expire(h, th); !errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
	}
	if r.Paused() != 0 {
		t.Errorf("%d hooks paused", r.Paused())
	}
}

func TestRunHookKeysAndArgs(t *testing.T) {
	tests := []struct {
		name string
		h    Hook
		want string
	}{
		{"keys then args", Hook{Name: "on_change", Player: "p", Keys: []string{"door", "lamp"}, Args: []string{"x"}}, "say:p door,lamp x 3"},
		{"empty keys", Hook{Name: "on_change", Player: "p", Keys: []string{}}, "say:p  nil 2"},
		{"no keys", Hook{Name: "on_change", Player: "p", Args: []string{"x"}}, "say:p x nil 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(DefaultLimits)
			defer r.Close()
			env := load(t, r, `function on_change(p, a, b, c)
  local first = type(a) == "table" and table.concat(a, ",") or tostring(a)
  local n = 1
  if a ~= nil then n = 2 end
  if b ~= nil then n = 3 end
  if c ~= nil then n = 4 end
  say(p.name .. " " .. first .. " " .. tostring(b) .. " " .. n)
end`)
			h := &recorder{}
			if _, err := r.RunHook(h, env, tt.h); err != nil {
				t.Fatal(err)
			}
			if len(h.out) != 1 || h.out[0] != tt.want {
				t.Errorf("out = %q, want %q", h.out, tt.want)
			}
		})
	}
}

// NoWait refuses every kind of question, and nothing stays paused.
func TestRunHookNoWait(t *testing.T) {
	for _, ask := range []string{`ui.dialog("?", {"a"})`, `ui.select("?", {"a"})`, `ui.ask("?")`, `ui.dialog("?", {"a"}, {timeout = 5})`} {
		r := New(DefaultLimits)
		env := load(t, r, `function on_timer(p) say("before") `+ask+` say("after") end`)
		h := &recorder{}
		th, err := r.RunHook(h, env, Hook{Name: "on_timer", Player: "p", NoWait: true})
		if th != nil || !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "question open already") {
			t.Errorf("%s: th = %v, err = %v", ask, th, err)
		}
		if strings.Join(h.out, ";") != "say:before" || r.Paused() != 0 {
			t.Errorf("%s: out = %q, paused %d", ask, h.out, r.Paused())
		}
		// pcall cannot get around it either.
		env2 := load(t, r, `function on_timer(p) local ok, err = pcall(function() return `+ask+` end) say(tostring(ok)) end`)
		h2 := &recorder{}
		if th, err := r.RunHook(h2, env2, Hook{Name: "on_timer", Player: "p", NoWait: true}); th != nil || err != nil || strings.Join(h2.out, ";") != "say:false" {
			t.Errorf("%s in pcall: th = %v, err = %v, out = %q", ask, th, err, h2.out)
		}
		r.Close()
	}
}

func TestRunHookMissing(t *testing.T) {
	r := New(DefaultLimits)
	env := load(t, r, `x = 1`)
	if th, err := r.RunHook(&recorder{}, env, Hook{Name: "on_timer", Player: "p", NoWait: true}); th != nil || err != nil {
		t.Errorf("missing hook: th = %v, err = %v", th, err)
	}
	r.Close()
	if _, err := r.RunHook(&recorder{}, env, Hook{Name: "on_timer", Player: "p"}); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: err = %v, want ErrClosed", err)
	}
}
