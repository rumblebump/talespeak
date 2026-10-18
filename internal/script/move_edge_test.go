package script

import (
	"errors"
	"strings"
	"testing"
)

// Edge cases of room.go (T-0022) in the runtime: pcall around it and
// around the waits after it, every kind of wait, argument types, the id
// length limit and the output cap.

func TestRoomGoEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" for a hook that ends without error
		wantOut string
	}{
		{"pcall around a wait after room.go catches the error",
			`function on_enter() room.go("hall") local ok, e = pcall(ui.dialog, "?", {"x"}) say(tostring(ok)) end`,
			"", "go:hall|say:false"},
		{"pcall around room.go still marks the move",
			`function on_enter() pcall(room.go, "hall") ui.dialog("?", {"x"}) end`,
			"cannot wait", "go:hall"},
		{"pcall around an unknown room keeps the earlier move",
			`function on_enter() room.go("hall") local ok = pcall(room.go, "attic") say(tostring(ok)) end`,
			"", "go:hall|say:false"},
		{"an unknown room does not block a later wait",
			`function on_enter() pcall(room.go, "attic") say("still here") end`,
			"", "say:still here"},
		{"ui.select after room.go",
			`function on_enter() room.go("hall") ui.select("?", {"a", "b"}) end`,
			"cannot wait", "go:hall"},
		{"ui.ask after room.go",
			`function on_enter() room.go("hall") ui.ask("?") end`,
			"cannot wait", "go:hall"},
		{"the last room.go is passed on last",
			`function on_enter() room.go("hall") room.go("cell") end`,
			"", "go:hall|go:cell"},
		{"a number is turned into a string id",
			`function on_enter() room.go(1) end`,
			`no room "1"`, ""},
		{"nil is a script error",
			`function on_enter() room.go() end`,
			"string expected", ""},
		{"a table is a script error",
			`function on_enter() room.go({}) end`,
			"string expected", ""},
		{"the empty id is no room",
			`function on_enter() room.go("") end`,
			`no room ""`, ""},
		{"64 bytes is checked against the pack",
			`function on_enter() room.go(string.rep("x", 64)) end`,
			"no room", ""},
		{"room.go counts toward the output cap",
			`function on_enter() for i = 1, 100 do room.go("hall") end end`,
			"", strings.TrimSuffix(strings.Repeat("go:hall|", 100), "|")},
		{"the 101st output fails",
			`function on_enter() for i = 1, 100 do room.go("hall") end say("x") end`,
			"more than 100 outputs", strings.TrimSuffix(strings.Repeat("go:hall|", 100), "|")},
		{"the room table is read-only",
			`function on_enter() room.go = function() end end`,
			"read-only", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(DefaultLimits)
			defer r.Close()
			env := load(t, r, tt.src)
			h := &recorder{}
			th, err := r.Run(h, env, "on_enter", "Ada")
			if th != nil {
				t.Fatalf("hook paused in %+v", th.Dialog)
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("err = %v", err)
			case tt.wantErr != "" && (!errors.Is(err, ErrScript) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("err = %v, want ErrScript with %q", err, tt.wantErr)
			}
			if got := strings.Join(h.out, "|"); got != tt.wantOut {
				t.Errorf("out = %q, want %q", got, tt.wantOut)
			}
			if r.Paused() != 0 {
				t.Errorf("%d hooks paused", r.Paused())
			}
		})
	}
}

// A hook may wait first and move after the answer; the flag that forbids
// waiting is per hook, so the next hook of the same Env may wait again.
func TestRoomGoAfterResume(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `
function on_enter(p)
  local n = ui.dialog("Go?", {"hall", "stay"})
  if n == 1 then room.go("hall") end
  local m = ui.dialog("And then?", {"x"})
end
function on_input(p, text) local s = ui.ask("Again?") say(s) end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "Ada")
	if err != nil || th == nil {
		t.Fatalf("Run = %v, %v", th, err)
	}
	th, err = r.Resume(h, th, 1)
	if th != nil || !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "cannot wait") {
		t.Fatalf("Resume = %v, %v; want the second wait to fail", th, err)
	}
	if got := strings.Join(h.out, "|"); got != "go:hall" {
		t.Errorf("out = %q", got)
	}
	th, err = r.Run(h, env, "on_input", "Ada", "x")
	if err != nil || th == nil || !th.Dialog.Ask {
		t.Fatalf("a new hook cannot wait: %v, %v", th, err)
	}
	if _, err := r.Answer(h, th, "yes"); err != nil {
		t.Errorf("Answer = %v", err)
	}
	if r.Paused() != 0 {
		t.Errorf("%d hooks paused", r.Paused())
	}
}

// A paused hook whose player left runs to its end without a host: room.go
// then fails like say, and nothing reaches the old host.
func TestRoomGoWhileCanceled(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter() pcall(ui.dialog, "?", {"x"}) local ok, e = pcall(room.go, "hall") gone = tostring(e) end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "Ada")
	if err != nil || th == nil {
		t.Fatalf("Run = %v, %v", th, err)
	}
	r.Cancel(th)
	if len(h.out) != 0 {
		t.Errorf("out after Cancel = %q", h.out)
	}
	if got := env.table.RawGetString("gone").String(); !strings.Contains(got, "no player here") {
		t.Errorf("room.go while canceled = %q", got)
	}
	if r.Paused() != 0 {
		t.Errorf("%d hooks paused", r.Paused())
	}
}

// player.room is the host's room at the start of the hook, in every hook,
// and changing it in Lua moves no one.
func TestPlayerRoom(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `
function on_enter(p) say(p.room) p.room = "hall" say(p.room) end
function on_input(p, text) say(p.room .. " " .. text) end
function on_click(p, id) say(p.room .. " " .. id) end`)
	h := &recorder{}
	for _, call := range [][]string{{"on_enter"}, {"on_input", "x"}, {"on_click", "box"}} {
		if _, err := r.Run(h, env, call[0], "Ada", call[1:]...); err != nil {
			t.Fatalf("%s: %v", call[0], err)
		}
	}
	if got := strings.Join(h.out, "|"); got != "say:cell|say:hall|say:cell x|say:cell box" {
		t.Errorf("out = %q", got)
	}
}

// FuzzRoomGo calls room.go with any id: it either reaches the host (a room
// the host knows) or is a script error, and never pauses.
func FuzzRoomGo(f *testing.F) {
	for _, id := range []string{"hall", "cell", "", "attic", strings.Repeat("x", 64), strings.Repeat("x", 65), "hall\x00", "../hall", "HALL"} {
		f.Add(id)
	}
	r := New(DefaultLimits)
	f.Cleanup(r.Close)
	env, err := r.Load("fuzz.lua", `function on_input(p, id) room.go(id) ui.dialog("?", {"x"}) end
function on_click(p, id) room.go(id) end`)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, id string) {
		known := id == "hall" || id == "cell"
		h := &recorder{}
		th, err := r.Run(h, env, "on_click", "p", id)
		if th != nil {
			t.Fatal("on_click paused")
		}
		if known != (err == nil) {
			t.Fatalf("room.go(%q) = %v", id, err)
		}
		if err != nil && !errors.Is(err, ErrScript) {
			t.Fatalf("room.go(%q) = %v, want ErrScript", id, err)
		}
		if want := "go:" + id; known != (strings.Join(h.out, "|") == want) {
			t.Fatalf("room.go(%q) out = %q", id, h.out)
		}
		// Waiting after room.go fails whatever the id.
		if th, err := r.Run(h, env, "on_input", "p", id); th != nil || !errors.Is(err, ErrScript) {
			t.Fatalf("wait after room.go(%q) = %v, %v", id, th, err)
		}
		if r.Paused() != 0 {
			t.Fatalf("%d hooks paused", r.Paused())
		}
	})
}
