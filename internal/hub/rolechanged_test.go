package hub

import (
	"strings"
	"sync"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// helpOf is what /help tells s.
func helpOf(s ws.Session) string {
	for _, m := range s.Handle(ws.Message{Type: "input", Text: "/help"}) {
		if m.Type == "notice" {
			return m.Text
		}
	}
	return ""
}

// Hub.RoleChanged reaches the loaded world: the account's sessions whose
// role changed end, others keep playing; a world that is not loaded, or
// a hub that closed, is left alone.
func TestHubRoleChanged(t *testing.T) {
	f := newFixture(t, 0)
	gm, ms := f.hub.NewSession("", ws.Who{World: 1, Account: 7, GM: true})
	if gm == nil {
		t.Fatalf("refused: %+v", ms)
	}
	defer gm.Close()
	player, _ := f.hub.NewSession("", ws.Who{World: 1, Account: 8})
	defer player.Close()
	elsewhere, _ := f.hub.NewSession("", ws.Who{World: 2, Account: 7, GM: true})
	defer elsewhere.Close()
	if !strings.Contains(helpOf(gm), "/say") {
		t.Fatalf("the GM's /help = %q", helpOf(gm))
	}

	f.hub.RoleChanged(1, 7, store.MemberGM)     // the same role: nothing
	f.hub.RoleChanged(3, 7, store.MemberPlayer) // not loaded
	f.hub.RoleChanged(99, 7, "")                // no such world
	if !works(gm) {
		t.Fatal("a RoleChanged that changed nothing ended the GM's game")
	}

	f.hub.RoleChanged(1, 7, store.MemberPlayer)
	if ms, ok := gm.(ws.Pusher).Pending(); ok || len(ms) != 1 || !strings.Contains(ms[0].Message, "role in this world has changed") {
		t.Errorf("demoted GM: %+v, %v", ms, ok)
	}
	if !works(player) || !works(elsewhere) {
		t.Error("another account's game, or the account's game in another world, ended")
	}
	if f.hub.Online(1) != 1 || f.hub.Online(2) != 1 {
		t.Errorf("online: %d in 1, %d in 2", f.hub.Online(1), f.hub.Online(2))
	}
	if f.hub.Loaded() != 2 {
		t.Errorf("loaded %d; RoleChanged must not load worlds", f.hub.Loaded())
	}
	// The client connects again with the new role.
	again, _ := f.hub.NewSession("", ws.Who{World: 1, Account: 7})
	defer again.Close()
	if strings.Contains(helpOf(again), "/say") {
		t.Errorf("the demoted player's /help = %q", helpOf(again))
	}

	f.hub.Close()
	f.hub.RoleChanged(1, 8, "") // after Close: no panic
}

// RoleChanged from many goroutines while players join, play and leave:
// the race detector stays quiet and no session of the account keeps the
// old role.
func TestHubRoleChangedConcurrent(t *testing.T) {
	f := newFixture(t, 0)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _ := f.hub.NewSession("", ws.Who{World: 1, Account: int64(1 + i%2), GM: true})
			if s == nil {
				return
			}
			for range 10 {
				s.Handle(ws.Message{Type: "input", Text: "/players"})
			}
			if i%3 == 0 {
				s.Close()
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.hub.RoleChanged(1, int64(1+i%2), store.MemberPlayer)
		}()
	}
	wg.Wait()
	f.hub.RoleChanged(1, 1, store.MemberPlayer)
	f.hub.RoleChanged(1, 2, store.MemberPlayer)
	if n := f.hub.Online(1); n != 0 {
		t.Errorf("%d GM sessions still play after their demotion", n)
	}
}
