package script

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// profileRecorder is a recorder whose player plays a profile.
type profileRecorder struct {
	recorder
	sheet     State
	none      bool              // no profile: ProfileState is nil
	exports   map[string][]byte // what progress.export kept
	completed int
}

func (h *profileRecorder) ProfileState() *State {
	if h.none {
		return nil
	}
	return &h.sheet
}

func (h *profileRecorder) Export(name string, value []byte) error {
	if !strings.HasPrefix(name, "flags.") {
		return errors.New("the pack does not export " + name)
	}
	if name == "flags.full" {
		return LimitError(errors.New("too many exports"))
	}
	if h.exports == nil {
		h.exports = map[string][]byte{}
	}
	if value == nil {
		delete(h.exports, name)
	} else {
		h.exports[name] = value
	}
	return nil
}

func (h *profileRecorder) Complete() error {
	h.completed++
	return nil
}

func runProfile(t *testing.T, h Host, body string) (string, error) {
	t.Helper()
	r := New(stateLimits)
	defer r.Close()
	r.SetPack(PackInfo{ID: "forest", Version: "1.2.0", Ruleset: "tiny", RulesetVersion: "1.0.0"})
	env := load(t, r, "function on_enter(player)\n"+body+"\nend")
	_, err := r.Run(h, env, "on_enter", "p")
	var out []string
	switch h := h.(type) {
	case *profileRecorder:
		out = h.out
	case *recorder:
		out = h.out
	}
	return strings.Join(out, "|"), err
}

func TestStateProfile(t *testing.T) {
	h := &profileRecorder{}
	out, err := runProfile(t, h, `
assert(state.profile.set("level", 3))
assert(state.profile.set("bag", {"rope"}))
say(state.profile.get("level") .. " " .. state.profile.get("bag")[1] .. " " .. tostring(state.player.get("level")))`)
	if err != nil || out != "say:3 rope nil" {
		t.Fatalf("out = %q, %v", out, err)
	}
	if h.sheet.Size() == 0 || h.state.Size() != 0 {
		t.Errorf("sheet %d bytes, player %d", h.sheet.Size(), h.state.Size())
	}
	// The limits of state.player hold.
	out, err = runProfile(t, h, `local ok, why = state.profile.set("big", string.rep("x", 17000)) say(tostring(ok) .. " " .. why)`)
	if err != nil || !strings.Contains(out, "say:nil state.profile.set: value larger than") {
		t.Errorf("big value: %q, %v", out, err)
	}
}

func TestStateProfileWithoutProfile(t *testing.T) {
	for _, h := range []Host{&recorder{}, &profileRecorder{none: true}} {
		for _, call := range []string{`state.profile.get("x")`, `state.profile.set("x", 1)`, `progress.export("flags.a", true)`, `progress.complete()`} {
			_, err := runProfile(t, h, call)
			if err == nil || !strings.Contains(err.Error(), "plays no profile") {
				t.Errorf("%T %s: %v", h, call, err)
			}
		}
	}
	// Outside a hook.
	r := New(DefaultLimits)
	defer r.Close()
	if _, err := r.Load("top.lua", `state.profile.get("x")`); err == nil || !strings.Contains(err.Error(), "no player here") {
		t.Errorf("top level: %v", err)
	}
}

func TestProgress(t *testing.T) {
	h := &profileRecorder{}
	out, err := runProfile(t, h, `
assert(progress.export("flags.fox", true))
assert(progress.export("flags.count", 3))
assert(progress.export("flags.count", nil))
local ok, why = progress.export("flags.full", 1)
say(tostring(ok) .. " " .. why)
progress.complete()`)
	if err != nil || out != "say:nil progress.export: too many exports" {
		t.Fatalf("out = %q, %v", out, err)
	}
	if len(h.exports) != 1 || h.exports["flags.fox"] == nil || h.completed != 1 {
		t.Errorf("exports = %v, completed %d", h.exports, h.completed)
	}
	for _, tc := range []struct{ body, want string }{
		{`progress.export("items.rope", true)`, "does not export items.rope"},
		{`progress.export("flags.f", function() end)`, "cannot be kept"},
		{`progress.export(nil, true)`, "string expected"},
	} {
		if _, err := runProfile(t, &profileRecorder{}, tc.body); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.body, err, tc.want)
		}
	}
	// Exports count toward the writes of one hook.
	out, err = runProfile(t, &profileRecorder{}, `
for i = 1, 50 do state.player.set("k", i) end
local ok, why = progress.export("flags.fox", true)
say(tostring(ok) .. " " .. why)`)
	if err != nil || !strings.Contains(out, "more than 50 writes") {
		t.Errorf("writes: %q, %v", out, err)
	}
}

func TestPackInfo(t *testing.T) {
	out, err := runProfile(t, &recorder{}, `
say(pack.id .. " " .. pack.version .. " " .. pack.ruleset.id .. " " .. pack.ruleset.version)
local ok = pcall(function() pack.version = "9.9.9" end)
local ok2 = pcall(function() pack.ruleset.id = "x" end)
say(tostring(ok) .. " " .. tostring(ok2))`)
	if err != nil || out != "say:forest 1.2.0 tiny 1.0.0|say:false false" {
		t.Errorf("out = %q, %v", out, err)
	}
	r := New(DefaultLimits)
	defer r.Close()
	r.SetPack(PackInfo{ID: "demo", Version: "1.0.0"})
	env := load(t, r, `function on_enter(p) say(pack.id .. " " .. tostring(pack.ruleset)) end`)
	h := &recorder{}
	if _, err := r.Run(h, env, "on_enter", "p"); err != nil || strings.Join(h.out, "|") != "say:demo nil" {
		t.Errorf("no ruleset: %v, %v", h.out, err)
	}
}

// encode returns the encoding of a Lua value, for sheets in tests.
func encode(t *testing.T, src string) []byte {
	t.Helper()
	h := &recorder{}
	if _, err := runState(t, h, `state.player.set("v", `+src+`)`); err != nil {
		t.Fatal(err)
	}
	v, _ := h.state.Encoded("v")
	return v
}

func TestMigrate(t *testing.T) {
	sheet := map[string][]byte{"level": encode(t, "3"), "name": encode(t, `"Ann"`)}
	src := `
function migrate(sheet)
  local level = sheet.level
  sheet.level = nil
  sheet.stats = { level = level, courage = 1 }
  return sheet
end`
	out, err := Migrate(DefaultLimits, "migrations/2.lua", src, sheet)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out["level"] != nil || string(out["name"]) != string(sheet["name"]) || string(out["stats"]) != string(encode(t, "{level = 3, courage = 1}")) &&
		string(out["stats"]) != string(encode(t, "{courage = 1, level = 3}")) {
		t.Errorf("migrated = %v", out)
	}
	// Changing the sheet in place without returning it works too.
	out, err = Migrate(DefaultLimits, "m.lua", `function migrate(s) s.extra = true end`, sheet)
	if err != nil || len(out) != 3 || out["extra"] == nil {
		t.Errorf("in place: %v, %v", out, err)
	}
}

func TestMigrateErrors(t *testing.T) {
	quick := DefaultLimits
	quick.Time = 50 * time.Millisecond
	for _, tc := range []struct {
		name, src string
		sheet     map[string][]byte
		want      error
		text      string
	}{
		{"no function", `x = 1`, nil, ErrScript, "defines no function migrate"},
		{"syntax", `function migrate(`, nil, ErrScript, ""},
		{"top level error", `error("boom")`, nil, ErrScript, "boom"},
		{"error", `function migrate(s) error("nope") end`, nil, ErrScript, "nope"},
		{"not a table", `function migrate(s) return 3 end`, nil, ErrScript, "must return the sheet"},
		{"bad key", `function migrate(s) return {[1] = true} end`, nil, ErrScript, "sheet keys are strings"},
		{"bad value", `function migrate(s) return {f = function() end} end`, nil, ErrScript, "cannot be kept"},
		{"too many keys", `function migrate(s) local t = {} for i = 1, 501 do t["k" .. i] = i end return t end`, nil, ErrScript, "more than 500 keys"},
		{"too large", `function migrate(s) local t = {} for i = 1, 20 do t["k" .. i] = string.rep("x", 15000) end return t end`, nil, ErrScript, "larger than"},
		{"loops", `function migrate(s) while true do end end`, nil, ErrTimeout, ""},
		{"waits", `function migrate(s) ui.dialog("?", {"a"}) end`, nil, ErrScript, ""},
		{"says", `function migrate(s) say("hi") end`, nil, ErrScript, ""},
		{"profile", `function migrate(s) state.profile.set("x", 1) end`, nil, ErrScript, "no player here"},
		{"damaged sheet", `function migrate(s) end`, map[string][]byte{"x": {0xff}}, errDamaged, "key \"x\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Migrate(quick, "migrations/2.lua", tc.src, tc.sheet)
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.text) {
				t.Errorf("err = %v, want %v with %q", err, tc.want, tc.text)
			}
		})
	}
}
