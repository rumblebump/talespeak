package hub

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
	"github.com/rumblebump/talespeak/internal/ws"
)

// A role change while the world is still loading reaches no session
// (Hub.RoleChanged skips a world that is not loaded yet), but a player
// whose connection read the old role is already waiting for that load.
// With a store, the world reads the role again when the player enters, so
// the demoted GM plays as a player all the same.
func TestRoleChangedWhileWorldLoads(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real SQLite file")
	}
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	wd, err := st.OpenWorld(ctx, "test-pack", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ann, err := st.CreateAccount(ctx, "ann", "hash", store.RolePlayer)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, wd.ID, ann.ID, store.MemberGM); err != nil {
		t.Fatal(err)
	}
	gate, started := make(chan struct{}), make(chan struct{}, 1)
	h := New(Config{Store: st, Packs: catalog(t, testPack(t)), Log: quiet,
		Load: func(w store.World, p *pack.Pack) (*game.World, error) {
			started <- struct{}{}
			<-gate
			return game.NewWorld(p, script.DefaultLimits, quiet, game.WithStore(st, w.ID), game.WithAudit(st))
		}})
	t.Cleanup(h.Close)
	who := ws.Who{World: wd.ID, Account: ann.ID, Name: "ann", GM: true} // the role as Auth read it
	joined := make(chan ws.Session, 1)
	go func() {
		s, _ := h.NewSession("", who)
		joined <- s
	}()
	<-started
	// An admin demotes ann meanwhile: the store first, then the hub.
	if err := st.AddMember(ctx, wd.ID, ann.ID, store.MemberPlayer); err != nil {
		t.Fatal(err)
	}
	h.RoleChanged(wd.ID, ann.ID, store.MemberPlayer)
	close(gate)
	s := <-joined
	if s == nil {
		t.Fatal("refused")
	}
	defer s.Close()
	if help := helpOf(s); strings.Contains(help, "/say") {
		t.Errorf("the demoted GM plays as GM after the world loaded:\n%s", help)
	}
	// Made GM again, a new connection with a stale Who of a player is GM.
	if err := st.AddMember(ctx, wd.ID, ann.ID, store.MemberGM); err != nil {
		t.Fatal(err)
	}
	h.RoleChanged(wd.ID, ann.ID, store.MemberGM)
	who.GM = false
	again, _ := h.NewSession("", who)
	if again == nil {
		t.Fatal("refused again")
	}
	defer again.Close()
	if help := helpOf(again); !strings.Contains(help, "/say") {
		t.Errorf("the promoted player is no GM:\n%s", help)
	}
	if ms, ok := s.(ws.Pusher).Pending(); ok || len(ms) != 1 {
		t.Errorf("the old session after the promotion: %+v, %v", ms, ok)
	}
}
