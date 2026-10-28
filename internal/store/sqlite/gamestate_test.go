package sqlite

import (
	"context"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
)

// state.game per world (T-0018): CreateWorld writes the lobby's picks,
// Save sets and removes keys, each world keeps its own, FinishWorld
// deletes them.
func TestGameState(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann, err := s.CreateAccount(ctx, "ann", "x", store.RolePlayer)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", PackVersion: "1.0.0", Visibility: store.VisibilityPublic, CreatedBy: ann.ID,
		GameState: []store.Change{{Key: "death", Value: []byte("revive")}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", PackVersion: "1.0.0", Visibility: store.VisibilityPublic, CreatedBy: ann.ID})
	if err != nil {
		t.Fatal(err)
	}
	get := func(w int64) map[string][]byte {
		t.Helper()
		vals, err := s.GameState(ctx, w)
		if err != nil {
			t.Fatal(err)
		}
		return vals
	}
	if vals := get(a.ID); len(vals) != 1 || string(vals["death"]) != "revive" {
		t.Fatalf("picks at creation = %q", vals)
	}
	if vals := get(b.ID); len(vals) != 0 {
		t.Fatalf("a world without picks = %q", vals)
	}
	if err := s.Save(ctx, store.Save{World: a.ID, GameState: []store.Change{
		{Key: "death", Value: []byte("stay")}, {Key: "door", Value: []byte("open")}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, store.Save{World: b.ID, GameState: []store.Change{{Key: "door", Value: []byte("shut")}}}); err != nil {
		t.Fatal(err)
	}
	if vals := get(a.ID); len(vals) != 2 || string(vals["death"]) != "stay" || string(vals["door"]) != "open" {
		t.Errorf("after save = %q", vals)
	}
	// A nil value removes the key, in this world only.
	if err := s.Save(ctx, store.Save{World: a.ID, GameState: []store.Change{{Key: "door"}}}); err != nil {
		t.Fatal(err)
	}
	if vals := get(a.ID); len(vals) != 1 || vals["door"] != nil {
		t.Errorf("after removing door = %q", vals)
	}
	if vals := get(b.ID); string(vals["door"]) != "shut" {
		t.Errorf("the other world = %q", vals)
	}
	if (store.Save{World: a.ID, GameState: []store.Change{{Key: "x"}}}).Empty() {
		t.Error("a save of state.game alone is empty")
	}
	if err := s.FinishWorld(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if vals := get(a.ID); len(vals) != 0 {
		t.Errorf("after finishing = %q", vals)
	}
	if vals := get(b.ID); len(vals) != 1 {
		t.Errorf("finishing one world touched another: %q", vals)
	}
	if vals := get(9999); len(vals) != 0 {
		t.Errorf("an unknown world = %q", vals)
	}
}
