package account

import (
	"errors"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
)

// A card shows the pack version its world runs; a world no installed
// version can run says why and cannot be played; a new world records
// the version it starts on; the pack list shows the versions.
func TestLobbyVersions(t *testing.T) {
	runs := func(w store.World) (Pack, error) {
		switch {
		case w.PackID == "test-pack" && w.PackVersion == "9.0.0":
			return Pack{}, errors.New("This world needs another version of its pack than this server has.")
		case w.PackID == "test-pack":
			return Pack{ID: "test-pack", Title: "Test pack 1.4", Version: "1.4.0"}, nil
		}
		return Pack{ID: "demo", Title: "The damp cell", Version: "1.0.0"}, nil
	}
	packs := []Pack{{ID: "demo", Title: "The damp cell", Version: "1.0.0"}, {ID: "test-pack", Title: "Test pack", Version: "1.4.0"}}
	f := newFixture(t, Config{Packs: packs, Runs: runs})
	ann := f.signUp("ann")
	if l := ann.lobby(); len(l.Packs) != 2 || l.Packs[1].Version != "1.4.0" {
		t.Errorf("packs = %+v", l.Packs)
	}
	made, code := ann.create(`{"pack":"test-pack","visibility":"public"}`)
	if code != 200 || made.World.Version != "1.4.0" || made.World.PackTitle != "Test pack 1.4" || made.World.Unavailable != "" || !made.World.Play {
		t.Fatalf("created = %d %+v", code, made.World)
	}
	w, err := f.st.World(t.Context(), made.World.ID)
	if err != nil || w.PackVersion != "1.4.0" {
		t.Errorf("stored world = %+v, %v", w, err)
	}
	if c, ok := ann.card(0); !ok || c.Version != "1.0.0" {
		t.Errorf("main world card = %+v", c)
	}

	if err := f.st.SetPackVersion(t.Context(), made.World.ID, "9.0.0"); err != nil {
		t.Fatal(err)
	}
	c, ok := ann.card(made.World.ID)
	if !ok || c.Play || c.Version != "" || c.Unavailable == "" || c.PackTitle != "test-pack" || c.Title != "test-pack" || !c.Finish {
		t.Errorf("card of a world no version runs = %+v", c)
	}
	if code := ann.get(f.ws(made.World.ID), nil); code == 200 {
		t.Error("a world no version runs can be played")
	}
}
