package script

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The configured TALESPEAK_STATE_* limits (StateLimits) hold for
// state.profile, progress.export and ruleset migrations as they do for
// state.player: lower ones refuse what the defaults allow, higher ones
// allow what the defaults refuse.

// limitsWith returns the test limits with state limits st.
func limitsWith(st StateLimits) Limits {
	l := stateLimits
	l.State = st.OrDefault()
	return l
}

// runProfileLimits runs body in on_enter on a runtime with limits, for a
// player whose sheet has the same state limits, as a world gives it.
func runProfileLimits(t *testing.T, limits Limits, h *profileRecorder, body string) (string, error) {
	t.Helper()
	h.sheet.Limits = limits.State
	r := New(limits)
	defer r.Close()
	env := load(t, r, "function on_enter(player)\n"+body+"\nend")
	_, err := r.Run(h, env, "on_enter", "p")
	return strings.Join(h.out, "|"), err
}

func TestStateProfileConfiguredLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits StateLimits
		body   string
		want   string
	}{
		{"lower value", StateLimits{ValueBytes: 1 << 10}, `local ok, why = state.profile.set("v", string.rep("x", 2000)) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil state.profile.set: value larger than 1024 bytes"},
		{"higher value", StateLimits{ValueBytes: 64 << 10}, `local ok, why = state.profile.set("v", string.rep("x", 20000)) say(tostring(ok) .. " " .. tostring(why))`,
			"say:true nil"},
		{"lower keys", StateLimits{PlayerKeys: 10}, `for i = 1, 10 do assert(state.profile.set("k" .. i, i)) end
local ok, why = state.profile.set("k11", 11) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil state.profile.set: more than 10 keys"},
		{"lower size", StateLimits{PlayerBytes: 4 << 10}, `for i = 1, 3 do assert(state.profile.set("k" .. i, string.rep("x", 1000))) end
local ok, why = state.profile.set("k4", string.rep("x", 1500)) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil state.profile.set: player state larger than 4096 bytes"},
		{"lower key bytes", StateLimits{KeyBytes: 32}, `local ok, err = pcall(state.profile.set, string.rep("k", 33), 1) say(tostring(ok) .. " " .. tostring(err))`,
			"keys are 1 to 32 bytes"},
		{"lower depth", StateLimits{Depth: 2}, `local ok, why = state.profile.set("t", {{{1}}}) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil state.profile.set: tables nested deeper than 2"},
		{"lower writes", StateLimits{Writes: 1}, `assert(state.profile.set("a", 1)) local ok, why = state.profile.set("b", 2) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil state.profile.set: more than 1 writes in one hook"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runProfileLimits(t, limitsWith(tc.limits), &profileRecorder{}, tc.body)
			if err != nil || !strings.Contains(out, tc.want) {
				t.Errorf("out = %q, %v; want %q", out, err, tc.want)
			}
		})
	}
	// The default refuses what the higher limit allowed: the test above
	// does test the configured limit.
	out, err := runProfileLimits(t, stateLimits, &profileRecorder{}, `local ok, why = state.profile.set("v", string.rep("x", 20000)) say(tostring(ok) .. " " .. tostring(why))`)
	if err != nil || !strings.Contains(out, "value larger than 16384 bytes") {
		t.Errorf("default limit: %q, %v", out, err)
	}
}

func TestProgressExportConfiguredLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits StateLimits
		body   string
		want   string
		kept   int // exports kept
	}{
		{"lower value", StateLimits{ValueBytes: 1 << 10}, `local ok, why = progress.export("flags.a", string.rep("x", 2000)) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil progress.export: value larger than 1024 bytes", 0},
		{"higher value", StateLimits{ValueBytes: 64 << 10}, `local ok, why = progress.export("flags.a", string.rep("x", 20000)) say(tostring(ok) .. " " .. tostring(why))`,
			"say:true nil", 1},
		{"default value", StateLimits{}, `local ok, why = progress.export("flags.a", string.rep("x", 20000)) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil progress.export: value larger than 16384 bytes", 0},
		{"lower depth", StateLimits{Depth: 2}, `local ok, why = progress.export("flags.a", {{{1}}}) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil progress.export: tables nested deeper than 2", 0},
		{"lower writes", StateLimits{Writes: 1}, `assert(progress.export("flags.a", 1)) local ok, why = progress.export("flags.b", 2) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil progress.export: more than 1 writes in one hook", 1},
		{"writes shared with state.profile", StateLimits{Writes: 2}, `assert(state.profile.set("a", 1)) assert(progress.export("flags.a", 1))
local ok, why = state.profile.set("b", 2) say(tostring(ok) .. " " .. tostring(why))`,
			"say:nil state.profile.set: more than 2 writes in one hook", 1},
		{"higher writes", StateLimits{Writes: 100}, `for i = 1, 60 do assert(progress.export("flags.a" .. i, i)) end say("ok")`,
			"say:ok", 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &profileRecorder{}
			out, err := runProfileLimits(t, limitsWith(tc.limits), h, tc.body)
			if err != nil || out != tc.want || len(h.exports) != tc.kept {
				t.Errorf("out = %q, %v, %d exports; want %q, %d", out, err, len(h.exports), tc.want, tc.kept)
			}
		})
	}
}

func TestMigrateConfiguredLimits(t *testing.T) {
	big := map[string][]byte{"v": EncodeString(strings.Repeat("x", 20000))}
	deep := map[string][]byte{"t": encode(t, `{{{1}}}`)}
	for _, tc := range []struct {
		name   string
		limits StateLimits
		src    string
		sheet  map[string][]byte
		want   error // nil: the migration works
		text   string
	}{
		{"lower keys", StateLimits{PlayerKeys: 10}, `function migrate(s) for i = 1, 11 do s["k" .. i] = i end end`, nil, ErrScript, "more than 10 keys"},
		{"lower keys fit", StateLimits{PlayerKeys: 10}, `function migrate(s) for i = 1, 10 do s["k" .. i] = i end end`, nil, nil, ""},
		{"higher keys", StateLimits{PlayerKeys: 1000}, `function migrate(s) for i = 1, 600 do s["k" .. i] = i end end`, nil, nil, ""},
		{"lower size", StateLimits{PlayerBytes: 4 << 10}, `function migrate(s) for i = 1, 5 do s["k" .. i] = string.rep("x", 1000) end end`, nil, ErrScript, "larger than 4096 bytes"},
		{"lower value", StateLimits{ValueBytes: 1 << 10}, `function migrate(s) s.v = string.rep("x", 2000) end`, nil, ErrScript, "value larger than 1024 bytes"},
		{"higher value", StateLimits{ValueBytes: 64 << 10}, `function migrate(s) s.w = string.rep("y", 30000) end`, big, nil, ""},
		{"default value", StateLimits{}, `function migrate(s) s.w = string.rep("y", 30000) end`, nil, ErrScript, "value larger than 16384 bytes"},
		{"lower key bytes", StateLimits{KeyBytes: 32}, `function migrate(s) s[string.rep("k", 33)] = 1 end`, nil, ErrScript, "strings of 1 to 32 bytes"},
		{"higher key bytes", StateLimits{KeyBytes: 255}, `function migrate(s) s[string.rep("k", 200)] = 1 end`, nil, nil, ""},
		{"lower depth", StateLimits{Depth: 2}, `function migrate(s) s.t = {{{1}}} end`, nil, ErrScript, "nested deeper than 2"},
		// A sheet kept under higher limits does not load into a migration
		// with lower ones.
		{"sheet deeper than the limit", StateLimits{Depth: 2}, `function migrate(s) end`, deep, errDamaged, `key "t"`},
		{"higher depth", StateLimits{Depth: 32}, `function migrate(s) local t = 1 for i = 1, 20 do t = {t} end s.t = t end`, nil, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := DefaultLimits
			limits.Time = 5 * time.Second // -race is slow
			limits.State = tc.limits
			out, err := Migrate(limits, "migrations/2.lua", tc.src, tc.sheet)
			if tc.want == nil {
				if err != nil || len(out) == 0 {
					t.Errorf("Migrate = %d keys, %v", len(out), err)
				}
				return
			}
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.text) {
				t.Errorf("err = %v, want %v with %q", err, tc.want, tc.text)
			}
		})
	}
}
