package script

import (
	"errors"
	"strings"
	"testing"
)

func TestRoomGo(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `
function on_enter(player) say(player.name .. " in " .. player.room) room.go("hall") end
function on_input(player, text) room.go(text) end
function on_click() room.go("hall") ui.dialog("Stay?", {"yes"}) end
function on_long() room.go(string.rep("x", 65)) end`)
	h := &recorder{}
	if th, err := r.Run(h, env, "on_enter", "Ada"); th != nil || err != nil {
		t.Fatalf("Run = %v, %v", th, err)
	}
	if got := strings.Join(h.out, "|"); got != "say:Ada in cell|go:hall" {
		t.Errorf("out = %q", got)
	}
	for _, tt := range []struct{ hook, arg, want string }{
		{"on_input", "attic", `no room "attic" in this pack`},
		{"on_click", "", "a hook cannot wait for an answer after it"},
		{"on_long", "", "at most 64 bytes"},
	} {
		_, err := r.Run(h, env, tt.hook, "Ada", tt.arg)
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.hook, err, tt.want)
		}
	}
	if r.Paused() != 0 {
		t.Errorf("%d hooks paused", r.Paused())
	}
}

// room.go needs a player, like say.
func TestRoomGoOutsideHook(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	if _, err := r.Load("top.lua", `room.go("hall")`); err == nil || !strings.Contains(err.Error(), "no player here") {
		t.Errorf("Load = %v", err)
	}
}
