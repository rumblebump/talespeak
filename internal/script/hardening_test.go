package script

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Tests for the fixes from the T-0003 security review.

// hook loads src as a script with an on_enter hook body and runs it,
// answering every dialog with option 1.
func hook(t *testing.T, body string) ([]string, error) {
	t.Helper()
	r := New(Limits{Time: 2 * time.Second, CallMemory: 64 << 20, StateMemory: 64 << 20})
	defer r.Close()
	env, err := r.Load("h.lua", "function on_enter(player)\n"+body+"\nend")
	if err != nil {
		return nil, err
	}
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	for th != nil && err == nil {
		th, err = r.Resume(h, th, 1)
	}
	return h.out, err
}

func TestDialogWaitsAnywhere(t *testing.T) {
	for name, body := range map[string]string{
		"pcall":      `local ok, n = pcall(ui.dialog, "?", {"a"}) say(tostring(ok) .. n)`,
		"sort":       `local n table.sort({2, 1}, function(a, b) n = ui.dialog("?", {"a"}) return a < b end) say("true" .. n)`,
		"__index":    `local t = setmetatable({}, {__index = function() return ui.dialog("?", {"a"}) end}) say("true" .. t.x)`,
		"__tostring": `local t = setmetatable({}, {__tostring = function() return "true" .. ui.dialog("?", {"a"}) end}) say(tostring(t))`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := hook(t, body)
			if err != nil || strings.Join(out, "|") != "say:true1" {
				t.Errorf("out = %q, err = %v", out, err)
			}
		})
	}
}

func TestCancelEndsPausedHook(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter()
  local ok, err = pcall(ui.dialog, "?", {"a"})
  say("after: " .. tostring(err))
  ui.dialog("again?", {"b"}) -- refused: the player left
  say("not reached")
end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(th, err)
	}
	r.Cancel(th)
	if r.Paused() != 0 {
		t.Errorf("Paused = %d after Cancel", r.Paused())
	}
	if len(h.out) != 0 {
		t.Errorf("a canceled hook's output reached the player: %q", h.out)
	}
	if _, err := r.Resume(h, th, 1); err == nil {
		t.Error("Resume after Cancel = nil error")
	}
}

func TestCloseEndsPausedHooks(t *testing.T) {
	r := New(DefaultLimits)
	env := load(t, r, `function on_enter() ui.dialog("?", {"a"}) end`)
	for range 10 {
		if _, err := r.Run(&recorder{}, env, "on_enter", "p"); err != nil {
			t.Fatal(err)
		}
	}
	r.Close()
	if r.Paused() != 0 {
		t.Errorf("Paused = %d after Close", r.Paused())
	}
	if _, err := r.Run(&recorder{}, env, "on_enter", "p"); !errors.Is(err, ErrClosed) {
		t.Errorf("Run after Close = %v", err)
	}
	r.Close() // twice is fine
}

func TestLibrariesAreReadOnly(t *testing.T) {
	for _, body := range []string{
		`string.rep = nil`,
		`rawset(string, "rep", nil)`,
		`ui.dialog = nil`,
		`rawset(ui, "dialog", nil)`,
		`math.pi = 3`,
		`table.insert = nil`,
		`setmetatable(string, {})`,
		`getmetatable("").__index.rep = nil`,
		`getmetatable(_G).__index.say = nil`,
	} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

func TestGlobalsDoNotLeak(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	load(t, r, `_G.say = nil say = nil x = 1 _G.y = 2`)
	b := load(t, r, `function on_enter() say(tostring(x) .. tostring(y) .. tostring(_G.x)) end`)
	h := &recorder{}
	if _, err := r.Run(h, b, "on_enter", "p"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.out, "|") != "say:nilnilnil" {
		t.Errorf("out = %q", h.out)
	}
}

func TestPatternFunctionsAllRefused(t *testing.T) {
	for _, name := range []string{"gfind", "gmatch", "gsub", "match"} {
		if _, err := hook(t, `string.`+name+`("aaa", "a", "b")`); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "patterns are off") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestConcat(t *testing.T) {
	out, err := hook(t, `say("a" .. 1 .. "b" .. 2.5) local s = "x" say(s .. s .. (s .. s))`)
	if err != nil || strings.Join(out, "|") != "say:a1b2.5|say:xxxx" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	for _, body := range []string{`local x = "a" .. {}`, `local x = "a" .. nil`, `local x = true .. "a"`} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "concatenate") {
			t.Errorf("%s: err = %v", body, err)
		}
	}
}

func TestConcatUsesFirstResultOnly(t *testing.T) {
	out, err := hook(t, `local function two() return "a", "b" end
say(two() .. two() .. "!")`)
	if err != nil || out[0] != "say:aa!" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestConcatNameIsReserved(t *testing.T) {
	for _, src := range []string{
		`local __concat = 1`,
		`__concat = print`,
		`function __concat() end`,
		`local function f(__concat) end`,
		`for __concat = 1, 2 do end`,
		`for k, __concat in pairs({}) do end`,
		`local x = __concat("a", "b")`,
	} {
		r := New(DefaultLimits)
		if _, err := r.Load("r.lua", src); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("%s: err = %v", src, err)
		}
		r.Close()
	}
}

func TestFormat(t *testing.T) {
	tests := []struct{ format, args, want string }{
		{"%d|%5d|%-5d|%05d", "42, 42, 42, 42", "42|   42|42   |00042"},
		{"%i %u", "-3.7, 3", "-3 3"},
		{"%x %X %o %c", "255, 255, 8, 65", "ff FF 10 A"},
		{"%f %.2f %e %g", "1.5, 1.5, 1500, 0.1", "1.500000 1.50 1.500000e+03 0.1"},
		{"%s %5s %.2s", `"abc", "ab", "abc"`, "abc    ab ab"},
		{"%s %s", `1, "x"`, "1 x"},
		{"%q", `"a\"b\n"`, `"a\"b\` + "\n" + `"`},
		{"100%%", "", "100%"},
	}
	for _, tt := range tests {
		args := ""
		if tt.args != "" {
			args = ", " + tt.args
		}
		out, err := hook(t, `say(string.format("`+strings.ReplaceAll(tt.format, `"`, `\"`)+`"`+args+`))`)
		if err != nil || len(out) != 1 || out[0] != "say:"+tt.want {
			t.Errorf("format(%q, %s) = %q, %v; want %q", tt.format, tt.args, out, err, tt.want)
		}
	}
	for _, f := range []string{"%[1]s", "%v", "%p", "%T", "%#v", "%100d", "%.100f", "%.[1]9f", "%", "%5", "%------d"} {
		if _, err := hook(t, `string.format("`+f+`", 1)`); !errors.Is(err, ErrScript) {
			t.Errorf("format %q: err = %v, want a script error", f, err)
		}
	}
	for _, args := range []string{"", ", true", ", {}", ", setmetatable({}, {__tostring = function() return \"x\" end})"} {
		if _, err := hook(t, `string.format("%s"`+args+`)`); !errors.Is(err, ErrScript) {
			t.Errorf("%%s with %q: err = %v, want a script error", args, err)
		}
	}
	if _, err := hook(t, `string.format("%d")`); !errors.Is(err, ErrScript) {
		t.Errorf("missing argument: err = %v", err)
	}
	// The length check runs before each piece is added.
	if _, err := hook(t, `local s = string.rep("x", 1e6) string.format("%s%s", s, s)`); !errors.Is(err, ErrScript) {
		t.Errorf("large result: err = %v", err)
	}
}

func TestHugeNumberArguments(t *testing.T) {
	for _, body := range []string{
		`unpack({}, -2^63, 0)`,
		`unpack({}, 0/0)`,
		`unpack({}, -1e300, 1)`,
		`string.byte("abc", -2^62, 2^62)`,
		`table.concat({}, "", 1, 2^62)`,
	} {
		start := time.Now()
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v", body, err)
		}
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Errorf("%s took %v", body, d)
		}
	}
	if out, err := hook(t, `say(string.rep("ab", 0/0) .. string.rep("", 1e12) .. string.rep("x", -1))`); err != nil || out[0] != "say:" {
		t.Errorf("rep edge cases: %q, %v", out, err)
	}
}

func TestVarargsRefused(t *testing.T) {
	for _, src := range []string{`local function f(...) end`, `local x = ...`, `function on_enter(...) end`} {
		r := New(DefaultLimits)
		if _, err := r.Load("v.lua", src); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "varargs") {
			t.Errorf("%s: err = %v", src, err)
		}
		r.Close()
	}
}

func TestSortLimit(t *testing.T) {
	if _, err := hook(t, `local t = {} for i = 1, 20000 do t[i] = -i end table.sort(t) table.sort(t, function(a, b) return a > b end)`); err != nil {
		t.Errorf("20000 entries: %v", err)
	}
	if _, err := hook(t, `local t = {} for i = 1, 20001 do t[i] = i end table.sort(t)`); !errors.Is(err, ErrScript) {
		t.Errorf("20001 entries: err = %v", err)
	}
}

func TestCancelHasSmallBudget(t *testing.T) {
	r := New(Limits{Time: 10 * time.Second, CallMemory: 32 << 20, StateMemory: 64 << 20})
	defer r.Close()
	env := load(t, r, `function on_enter() pcall(ui.dialog, "?", {"a"}) while true do end end`)
	th, err := r.Run(&recorder{}, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(th, err)
	}
	start := time.Now()
	r.Cancel(th)
	if d := time.Since(start); d > time.Second {
		t.Errorf("Cancel took %v; a hook that ignores leaving gets only a short budget", d)
	}
}

func TestSubCopies(t *testing.T) {
	out, err := hook(t, `local s = string.rep("abc", 1000) say(s:sub(2, 4) .. string.sub(s, -2))`)
	if err != nil || out[0] != "say:bcabc" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}
