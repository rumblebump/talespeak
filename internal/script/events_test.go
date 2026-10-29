package script

import (
	"errors"
	"strings"
	"testing"
)

// Names that read as the engine's, a game master's or staff's are
// reserved; names that only look alike are not.
func TestReservedName(t *testing.T) {
	for name, want := range map[string]bool{
		"GM": true, "gm": true, "G.M.": true, "GM Carl": true, "Carl GM": true, "Game Master": true, "TheGameMaster": true,
		"game-master": true, "Admin": true, "AdminBob": true, "admin_2": true, "Administrator": true, "Moderator": true,
		"Teacher": true, "Teacher Owl": true, "System": true, "Server": true, "Talespeak": true, "Staff": true, "Mods": true,
		"Whisp": false, "Holmes": false, "Badminton": false, "Gemma": false, "Agma": false, "Systemic": false,
		"Mira": false, "Wiggins": false, "Master Wu": false, "Modest Mouse": false, "": false,
		"G​M": true, "SysOp": true, "TheGM": true, "CarlGM": true, "ADMIN": true, "gm2": true, "Game_Master_1": true,
		"Staffer": false, "Modder": false, "Gmelin": false, "Servera": false,
	} {
		if got := ReservedName(name); got != want {
			t.Errorf("ReservedName(%q) = %v, want %v", name, got, want)
		}
	}
}

// eventHost is the recorder with room events: it records them, and
// refuses every one with refuse.
type eventHost struct {
	recorder
	refuse error
}

func (h *eventHost) Event(text string, all bool) error {
	if h.refuse != nil {
		return h.refuse
	}
	if all {
		text += " (all)"
	}
	h.out = append(h.out, "event:"+text)
	return nil
}

// runEvent runs on_enter() body on h and returns what it recorded.
func runEvent(t *testing.T, h Host, body string) (string, error) {
	t.Helper()
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, "function on_enter()\n"+body+"\nend")
	_, err := r.Run(h, env, "on_enter", "p")
	switch h := h.(type) {
	case *eventHost:
		return strings.Join(h.out, "|"), err
	case *recorder:
		return strings.Join(h.out, "|"), err
	}
	return "", err
}

func TestRoomEventArguments(t *testing.T) {
	for _, tc := range []struct{ body, want, err string }{
		{`say(tostring(room.event("A door opens.")))`, "event:A door opens.|say:true", ""},
		{`room.event("x", {all = true})`, "event:x (all)", ""},
		{`room.event("x", {all = false})`, "event:x", ""},
		{`room.event("x", {})`, "event:x", ""},
		{`room.event("x", nil)`, "event:x", ""},
		{`room.event(42)`, "event:42", ""},
		{`room.event(string.rep("é", 250))`, "event:" + strings.Repeat("é", 250), ""},
		{`room.event("x", {all = "yes"})`, "", "all must be true or false"},
		{`room.event("x", {all = 1})`, "", "all must be true or false"},
		{`room.event("x", "all")`, "", "table expected"},
		{`room.event()`, "", "string expected"},
		{`room.event({})`, "", "string expected"},
		{`room.event(" \n ")`, "", "an event is 1 to 500 bytes"},
		{`room.event(string.rep("é", 250) .. "x")`, "", "an event is 1 to 500 bytes"},
		{`room.event("\255")`, "", "an event is 1 to 500 bytes"},
	} {
		got, err := runEvent(t, &eventHost{}, tc.body)
		if tc.err == "" && (err != nil || got != tc.want) {
			t.Errorf("%s: %q, %v; want %q", tc.body, got, err, tc.want)
		}
		if tc.err != "" && (!errors.Is(err, ErrScript) || !strings.Contains(err.Error(), tc.err) || got != "") {
			t.Errorf("%s: %q, %v; want the error %q", tc.body, got, err, tc.err)
		}
	}
}

// A host without Events plays alone: room.event is a script error. In
// the main chunk there is no player to tell it for.
func TestRoomEventWithoutEvents(t *testing.T) {
	if _, err := runEvent(t, &recorder{}, `room.event("x")`); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "this world keeps no room events") {
		t.Errorf("err = %v", err)
	}
	r := New(DefaultLimits)
	defer r.Close()
	if _, err := r.Load("main.lua", `room.event("x")`); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "no player here") {
		t.Errorf("main chunk: %v", err)
	}
}

// A refused event returns nil and the reason; it still counts.
func TestRoomEventRefused(t *testing.T) {
	got, err := runEvent(t, &eventHost{refuse: ErrTyped}, `
local ok, why = room.event("x")
say(tostring(ok) .. " " .. why)
for i = 1, 5 do room.event("y") end`)
	if !strings.HasPrefix(got, "say:nil room.event: it holds words a player typed") || !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "more than 5 room events") {
		t.Errorf("got %q, err %v", got, err)
	}
}

// MaxEvents counts per call: a hook that waits for an answer gets a new
// count when it goes on.
func TestRoomEventsPerCall(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter()
  for i = 1, 5 do room.event("before " .. i) end
  ui.dialog("Go on?", {"Yes"})
  for i = 1, 5 do room.event("after " .. i) end
  say("done")
end`)
	h := &eventHost{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatalf("th %v, err %v", th, err)
	}
	if _, err := r.Resume(h, th, 1); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if n := strings.Count(strings.Join(h.out, "|"), "event:"); n != 10 || h.out[len(h.out)-1] != "say:done" {
		t.Errorf("out = %q", h.out)
	}
}

// State.Check is asked about the key and every string of a value, nested
// keys included, and only for a write that keeps something.
func TestStateCheck(t *testing.T) {
	h := &recorder{}
	var asked []string
	h.world.Check = func(s string) error {
		asked = append(asked, s)
		if strings.Contains(s, "secret") {
			return ErrTyped
		}
		return nil
	}
	for _, tc := range []struct{ body, want string }{
		{`say(tostring(state.world.set("k", "fine")))`, "say:true"},
		{`say(select(2, state.world.set("secret", 1)))`, "say:state.world.set: it holds words a player typed"},
		{`say(select(2, state.world.set("k", {a = {b = {"secret"}}})))`, "say:state.world.set: it holds words a player typed"},
		{`say(select(2, state.world.set("k", {secret = true})))`, "say:state.world.set: it holds words a player typed"},
		{`say(select(2, state.world.update("k", function() return {"x", "a secret"} end)))`, "say:state.world.update: it holds words a player typed"},
		{`say(tostring(state.world.set("k", nil)))`, "say:true"},
		{`say(tostring(state.player.set("p", "secret")))`, "say:true"}, // state.player is the player's own
	} {
		h.out = nil
		got, err := runState(t, h, tc.body)
		if err != nil || !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s = %q, %v; want %q", tc.body, got, err, tc.want)
		}
	}
	if v, ok := h.world.String("k"); ok || v != "" {
		t.Errorf("k = %q after it was removed", v)
	}
	asked = nil
	if _, err := runState(t, h, `state.world.set("k", {n = 1, list = {"a", "b"}})`); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(asked, ","); !strings.HasPrefix(got, "k,") || !strings.Contains(got, "list") || !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Errorf("asked %q", got)
	}
}

// A pack's speaker cannot be the game master or staff, in any of the
// calls that take from.
func TestFromReserved(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	for _, body := range []string{
		`say("x", {from = "GM"})`,
		`ui.dialog("t", {"a"}, {from = "Game Master"})`,
		`ui.select("t", {"a", "b"}, {from = "AdminBob"})`,
		`ui.ask("q", {from = "Server"})`,
	} {
		env := load(t, r, "function on_enter() "+body+" end")
		h := &recorder{}
		if _, err := r.Run(h, env, "on_enter", "p"); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "from cannot name the game master") || len(h.out) != 0 {
			t.Errorf("%s: %v, out %q", body, err, h.out)
		}
	}
}

// FuzzReservedName: any name with a reserved word set apart is reserved.
func FuzzReservedName(f *testing.F) {
	for _, s := range []string{"Mira", "AdminBob", "G.M.", "", "日本", "\xff", "Badminton"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		_ = ReservedName(name)
		for _, w := range []string{"GM", "admin", "Teacher"} {
			if !ReservedName(name+" "+w) || !ReservedName(w+" "+name) {
				t.Fatalf("%q with %q is not reserved", name, w)
			}
		}
	})
}
