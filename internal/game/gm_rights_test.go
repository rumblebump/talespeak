package game

import (
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// rightsRoom tells, from on_timer, which GM rights
// the hook has: player.gm, an engine permission and a state.game write.
const rightsRoom = `
local function rights(p, at)
  local ok = state.game.set("k", at)
  say(at .. ": gm=" .. tostring(p.gm) .. " can=" .. tostring(p:can("world.pause")) .. " set=" .. tostring(ok))
end
function on_input(p, text)
  if text == "room" then timer.start("turn", 60, {room = true})
  elseif text == "go" then room.go("b")
  end
end
function on_timer(p, name, kind) rights(p, "timer") end`

// A room timer a player started that falls to a GM in GM mode (the
// starter left the room) runs without the GM's rights.
func TestRoomTimerFallsToGMWithoutRights(t *testing.T) {
	w, c := clockWorld(t, rightsRoom, timerRoomB)
	gm, _ := w.NewSession("", ws.Who{Account: 7, Name: "ann", GM: true})
	defer gm.Close()
	kid, _ := w.NewSession("", ws.Who{Account: 8, Name: "bob"})
	defer kid.Close()
	input(kid, "room")
	input(kid, "go")
	pending(t, gm)
	c.Advance(61 * time.Second)
	if s := texts(pending(t, gm)); !strings.Contains(s, "timer: gm=false can=false set=nil") {
		t.Errorf("a fallen room timer ran with %q", s)
	}
	if v := w.game.Size(); v != 0 {
		t.Errorf("state.game holds %d bytes after the refused write", v)
	}

	// The GM's own room timer keeps their rights.
	input(gm, "room")
	c.Advance(61 * time.Second)
	if s := texts(pending(t, gm)); !strings.Contains(s, "timer: gm=true can=true set=true") {
		t.Errorf("the GM's own room timer ran with %q", s)
	}
}
