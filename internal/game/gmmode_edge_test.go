package game

import (
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// GM mode (T-0011) where it meets the other parts of the batch: the
// engine's permissions and a pack's, state.game writes, a question left
// open across a switch, and a paused world.

// permRoom asks player:can, of the player and of everyone in the room,
// and writes state.game after a question.
const permRoom = `
perm.define("door.open", {gm = true, player = false})
perm.define("door.knock", {gm = false, player = true})
function on_enter(p) end
function on_input(p, text)
  if text == "can" then
    say(tostring(p:can("world.pause")) .. " " .. tostring(p:can("door.open")) .. " " .. tostring(p:can("door.knock")))
  elseif text == "others" then
    local out = {}
    for _, o in ipairs(room.players()) do out[#out + 1] = tostring(o.gm) .. "/" .. tostring(o:can("timer.edit")) end
    table.sort(out)
    say(table.concat(out, ","))
  elseif text == "ask" then
    ui.dialog("Open the door?", {"Yes", "No"})
    local ok, why = state.game.set("door", "open")
    say("game " .. tostring(ok) .. " " .. tostring(why) .. " can " .. tostring(p:can("world.pause")) .. " gm " .. tostring(p.gm))
  elseif text == "write" then
    local ok, why = state.game.set("door", "shut")
    say("game " .. tostring(ok) .. " " .. tostring(why))
  end
end`

// The engine's permissions and a pack's follow GM mode as /gm switches
// it, for the game master and as the other players see them.
func TestPermsFollowGMMode(t *testing.T) {
	w, _ := clockWorld(t, permRoom, `function on_enter() end`)
	gm, _ := w.NewSession("", ws.Who{Account: 7, Name: "ann", GM: true, AsPlayer: true})
	defer gm.Close()
	p, _ := w.NewSession("", ws.Who{Account: 8, Name: "bob"})
	defer p.Close()
	steps := []struct {
		cmd          string
		gmCan, other string
	}{
		{"", "false false true", "false/false,false/false"},
		{"/gm on", "true true false", "false/false,true/true"},
		{"/gm off", "false false true", "false/false,false/false"},
	}
	for _, st := range steps {
		if st.cmd != "" {
			input(gm, st.cmd)
		}
		if got := texts(input(gm, "can")); got != st.gmCan {
			t.Errorf("after %q the GM can %q, want %q", st.cmd, got, st.gmCan)
		}
		if got := lastText(input(p, "others")); got != st.other {
			t.Errorf("after %q the player sees %q, want %q", st.cmd, got, st.other)
		}
		if got := texts(input(p, "can")); got != "false false true" {
			t.Errorf("after %q the player can %q", st.cmd, got)
		}
	}
}

// /gm waits while the GM's question is open: the hook waiting on it goes
// on with the player.gm, player:can and state.game rights it started
// with, all three the same, and /gm works once it is answered.
func TestGMModeDuringQuestion(t *testing.T) {
	w, _ := clockWorld(t, permRoom, `function on_enter() end`)
	gm, _ := w.NewSession("", ws.Who{Account: 7, Name: "ann", GM: true})
	defer gm.Close()
	if got := gm.Handle(ws.Message{Type: "input", Text: "ask"}); len(got) == 0 || got[len(got)-1].Type != "dialog" {
		t.Fatalf("ask = %+v", got)
	}
	if n := notices(input(gm, "/gm off")); len(n) != 1 || !strings.Contains(n[0], "Answer the question first") {
		t.Fatalf("/gm off with a question open = %q", n)
	}
	if len(w.auditLog) != 0 {
		t.Errorf("a refused /gm was audited: %+v", w.auditLog)
	}
	if got := texts(gm.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "game true nil can true gm true" {
		t.Errorf("the hook after the refused /gm off: %q", got)
	}
	if n := notices(input(gm, "/gm off")); len(n) != 1 || !strings.Contains(n[0], "GM mode is off") {
		t.Fatalf("/gm off once answered = %q", n)
	}
	gm.Handle(ws.Message{Type: "input", Text: "ask"})
	if n := notices(input(gm, "/gm on")); len(n) != 1 || !strings.Contains(n[0], "Answer the question first") {
		t.Fatalf("/gm on with a question open = %q", n)
	}
	got := texts(gm.Handle(ws.Message{Type: "choose", ID: "d2", Option: 1}))
	if !strings.Contains(got, "game nil state.game.set: only a game master in GM mode changes state.game") || !strings.HasSuffix(got, "can false gm false") {
		t.Errorf("the hook started with GM mode off: %q", got)
	}
}

// A game master who switches GM mode off in a paused world waits like
// any player, but /gm still works, and with it /resume.
func TestGMModeOffWhilePaused(t *testing.T) {
	w, _ := clockWorld(t, permRoom, `function on_enter() end`)
	gm, _ := w.NewSession("", ws.Who{Account: 7, Name: "ann", GM: true})
	defer gm.Close()
	input(gm, "/pause")
	if got := texts(input(gm, "write")); got != "game true nil" {
		t.Errorf("GM mode on, paused: %q", got)
	}
	input(gm, "/gm off")
	if n := notices(input(gm, "write")); len(n) != 1 || n[0] != "server: "+pausedText {
		t.Errorf("GM mode off, paused: %q", n)
	}
	if n := notices(input(gm, "/resume")); len(n) != 1 || !strings.Contains(n[0], "There is no command /resume") {
		t.Errorf("/resume with GM mode off = %q", n)
	}
	if n := notices(input(gm, "/gm on")); len(n) != 1 || !strings.Contains(n[0], "GM mode is on") {
		t.Fatalf("/gm on while paused = %q", n)
	}
	if got := input(gm, "/resume"); len(got) == 0 || got[0].Type != "pause" || got[0].Paused {
		t.Errorf("/resume = %+v", got)
	}
	if got := texts(input(gm, "can")); got != "true true false" {
		t.Errorf("after resume: %q", got)
	}
}

// A game master taken out of the world while it is paused, or demoted
// while playing as a player, loses the session; the world stays paused
// for the game master who joins next.
func TestRoleChangedWhilePaused(t *testing.T) {
	w, _ := clockWorld(t, permRoom, `function on_enter() end`)
	gm, _ := w.NewSession("", ws.Who{Account: 7, Name: "ann", GM: true})
	defer gm.Close()
	asPlayer, _ := w.NewSession("", ws.Who{Account: 9, Name: "cy", GM: true, AsPlayer: true})
	defer asPlayer.Close()
	input(gm, "/pause")
	pending(t, asPlayer)
	if n := w.RoleChanged(9, store.MemberPlayer); n != 1 {
		t.Errorf("demoting a GM who plays as a player ended %d sessions", n)
	}
	if n := w.RoleChanged(7, ""); n != 1 {
		t.Errorf("taking the GM out ended %d sessions", n)
	}
	for _, s := range []ws.Session{gm, asPlayer} {
		if ms, ok := s.(ws.Pusher).Pending(); ok || errorText(ms) != roleChangedMessage {
			t.Errorf("last messages %+v, open %v", ms, ok)
		}
	}
	next, first := w.NewSession("", ws.Who{Account: 10, Name: "dee", GM: true})
	defer next.Close()
	if len(first) == 0 || first[0].Type != "pause" || !first[0].Paused {
		t.Fatalf("the next GM joins %+v", first)
	}
	if got := input(next, "/resume"); len(got) == 0 || got[0].Type != "pause" || got[0].Paused {
		t.Errorf("/resume = %+v", got)
	}
}

// lastText is the text of the last message of ms.
func lastText(ms []ws.Message) string {
	if len(ms) == 0 {
		return ""
	}
	return ms[len(ms)-1].Text
}
