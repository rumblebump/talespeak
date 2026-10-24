package game

import (
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// GM mode works like sudo (T-0011): a game master who joined as a player
// has no GM commands and player.gm is false, until /gm switches it on.
// Every switch is in the audit log and the room sees it.
func TestGMMode(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{Account: 7, Name: "ann", GM: true, AsPlayer: true})
	defer gm.Close()
	other, _ := w.NewSession("", ws.Who{Account: 8, Name: "bob"})
	defer other.Close()
	pending(t, gm)

	if s := texts(input(gm, "/roll")); !strings.Contains(s, "gm=false") {
		t.Errorf("player.gm with GM mode off: %q", s)
	}
	if n := notices(input(gm, "/say hello")); len(n) != 1 || !strings.Contains(n[0], "no command /say") {
		t.Errorf("/say with GM mode off = %q", n)
	}
	help := strings.Join(notices(input(gm, "/help")), "\n")
	if !strings.Contains(help, "/gm [on|off]") || !strings.Contains(help, "playing as a player") || strings.Contains(help, "/say") {
		t.Errorf("/help with GM mode off:\n%s", help)
	}
	// Players never have /gm.
	if n := notices(input(other, "/gm on")); len(n) != 1 || !strings.Contains(n[0], "no command /gm") {
		t.Errorf("a player's /gm = %q", n)
	}
	if strings.Contains(strings.Join(notices(input(other, "/help")), "\n"), "/gm") {
		t.Error("a player's /help lists /gm")
	}

	if n := notices(input(gm, "/gm bogus")); len(n) != 1 || !strings.Contains(n[0], "Type /gm on or /gm off") {
		t.Errorf("/gm bogus = %q", n)
	}
	pending(t, other)
	if n := notices(input(gm, "/GM ON")); len(n) != 1 || !strings.Contains(n[0], "GM mode is on") {
		t.Fatalf("/gm on = %q", n)
	}
	if n := notices(pending(t, other)); len(n) != 1 || n[0] != "gm traveler: acts as the game master now." {
		t.Errorf("the room saw %q", n)
	}
	if s := texts(input(gm, "/roll")); !strings.Contains(s, "gm=true") {
		t.Errorf("player.gm with GM mode on: %q", s)
	}
	if n := notices(input(gm, "/gm on")); len(n) != 1 || !strings.Contains(n[0], "on already") {
		t.Errorf("/gm on twice = %q", n)
	}
	if n := notices(input(gm, "/gm")); len(n) != 1 || !strings.Contains(n[0], "GM mode is off") { // switches over
		t.Errorf("/gm = %q", n)
	}
	if n := notices(pending(t, other)); len(n) != 1 || n[0] != "gm traveler: plays as a player again." {
		t.Errorf("the room saw %q", n)
	}
	var got []string
	for _, e := range w.auditLog {
		got = append(got, e.Action+" "+e.Detail)
	}
	if strings.Join(got, ",") != "gm mode on,gm mode off" {
		t.Errorf("audit = %q", got)
	}

	// A role change that leaves the account a GM does not end the
	// session: GM mode is the player's choice.
	if n := w.RoleChanged(7, store.MemberGM); n != 0 {
		t.Errorf("RoleChanged(gm) ended %d sessions", n)
	}
	if n := w.RoleChanged(7, store.MemberPlayer); n != 1 {
		t.Errorf("RoleChanged(player) ended %d sessions", n)
	}
}

// A game master who joins as a GM starts in GM mode, as before.
func TestGMModeStartsOn(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{Account: 7, Name: "ann", GM: true})
	defer gm.Close()
	if s := texts(input(gm, "/roll")); !strings.Contains(s, "gm=true") {
		t.Errorf("player.gm = %q", s)
	}
	// AsPlayer without GM is a plain player.
	p, _ := w.NewSession("", ws.Who{Account: 8, Name: "bob", AsPlayer: true})
	defer p.Close()
	if n := notices(input(p, "/gm on")); len(n) != 1 || !strings.Contains(n[0], "no command /gm") {
		t.Errorf("/gm of a player = %q", n)
	}
}

// Switching tells the room and so is limited like /say.
func TestGMModeSlowsDown(t *testing.T) {
	w, _ := clockWorld(t, cmdRoom, cmdRoomB)
	gm, _ := w.NewSession("", ws.Who{Account: 7, Name: "ann", GM: true})
	defer gm.Close()
	slowed := false
	for range broadcastBurst + 2 {
		if n := notices(input(gm, "/gm")); len(n) == 1 && n[0] == "server: "+slowDownMessage {
			slowed = true
		}
	}
	if !slowed {
		t.Error("/gm was never slowed down")
	}
}
