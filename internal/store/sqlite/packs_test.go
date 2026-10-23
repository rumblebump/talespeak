package sqlite

import (
	"context"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
)

// RecordPacks reports a version whose files changed since it was last
// installed, and nothing for new versions or the same files.
func TestRecordPacks(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	first := []store.InstalledPack{{ID: "demo", Version: "1.0.0", Hash: "aa"}, {ID: "demo", Version: "1.1.0", Hash: "bb"}, {ID: "other", Version: "1.0.0", Hash: "aa"}}
	if changed, err := s.RecordPacks(ctx, first); err != nil || len(changed) != 0 {
		t.Fatalf("first start: %v, %v", changed, err)
	}
	if changed, err := s.RecordPacks(ctx, first); err != nil || len(changed) != 0 {
		t.Errorf("the same packs again: %v, %v", changed, err)
	}
	// 1.1.0 is gone, 2.0.0 is new and 1.0.0 changed without a new version.
	next := []store.InstalledPack{{ID: "demo", Version: "1.0.0", Hash: "cc"}, {ID: "demo", Version: "2.0.0", Hash: "dd"}}
	changed, err := s.RecordPacks(ctx, next)
	if err != nil || len(changed) != 1 || changed[0] != next[0] {
		t.Errorf("after changes: %v, %v", changed, err)
	}
	// The new hash is remembered; one that was removed and comes back with
	// other files is noticed too.
	if changed, err := s.RecordPacks(ctx, append(next, store.InstalledPack{ID: "demo", Version: "1.1.0", Hash: "ee"})); err != nil || len(changed) != 1 || changed[0].Version != "1.1.0" {
		t.Errorf("1.1.0 back with other files: %v, %v", changed, err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM installed_packs`).Scan(&n); err != nil || n != 4 {
		t.Errorf("%d installed packs, %v", n, err)
	}
}

// OpenWorld keeps the newest main world keep accepts, skipping those it
// does not, and starts a new one when it accepts none.
func TestOpenWorldKeep(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	one, err := s.OpenWorld(ctx, "demo", "1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	major := func(want string) func(string) bool {
		return func(v string) bool { return v == "" || v[:1] == want }
	}
	two, err := s.OpenWorld(ctx, "demo", "2.0.0", major("2"))
	if err != nil || two.ID == one.ID || !two.Home || two.PackVersion != "2.0.0" {
		t.Fatalf("a new major version: %+v, %v", two, err)
	}
	if again, err := s.OpenWorld(ctx, "demo", "2.1.0", major("2")); err != nil || again.ID != two.ID || again.PackVersion != "2.0.0" {
		t.Errorf("the same major version: %+v, %v", again, err)
	}
	// Back to 1.x (the newest home world is skipped): the older one.
	if back, err := s.OpenWorld(ctx, "demo", "1.0.0", major("1")); err != nil || back.ID != one.ID {
		t.Errorf("back to 1.x: %+v, %v", back, err)
	}
	// The former main world is still a world of its own, listed and
	// playable while its version is installed.
	list, err := s.ListWorlds(ctx, 0, true, 0, 10)
	if err != nil || len(list) != 2 {
		t.Errorf("ListWorlds = %+v, %v", list, err)
	}
}
