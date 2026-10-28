package sqlite

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
)

func TestCreateProfile(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	bob := mustAccount(t, s, "Bob", store.RolePlayer)
	p, err := s.CreateProfile(ctx, ann.ID, "Hero", "tiny-rules", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 || p.Account != ann.ID || p.Name != "Hero" || p.Ruleset != "tiny-rules" || p.Version != "1.0.0" || p.Generation != 0 ||
		p.Undo != "" || p.Created.IsZero() {
		t.Errorf("profile = %+v", p)
	}
	if _, err := s.CreateProfile(ctx, ann.ID, "hero", "other", "1.0.0"); !errors.Is(err, store.ErrNameTaken) {
		t.Errorf("same name in another case: %v", err)
	}
	if _, err := s.CreateProfile(ctx, bob.ID, "Hero", "tiny-rules", "1.0.0"); err != nil {
		t.Errorf("another account's same name: %v", err)
	}
	for i := 1; i < store.MaxProfiles; i++ {
		if _, err := s.CreateProfile(ctx, ann.ID, fmt.Sprint("P", i), "tiny-rules", "1.0.0"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateProfile(ctx, ann.ID, "One more", "tiny-rules", "1.0.0"); !errors.Is(err, store.ErrProfiles) {
		t.Errorf("past MaxProfiles: %v", err)
	}
	list, err := s.AccountProfiles(ctx, ann.ID)
	if err != nil || len(list) != store.MaxProfiles || list[0].ID != p.ID {
		t.Errorf("profiles = %d, %v", len(list), err)
	}
	if got, err := s.Profile(ctx, p.ID); err != nil || got != p {
		t.Errorf("Profile = %+v, %v", got, err)
	}
	if _, err := s.Profile(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown profile: %v", err)
	}
}

// Binding a character writes the imports once, starts the progress, and
// never moves a character to another profile.
func TestBindProfile(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "dragon-isle", "1.0.0", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	bob := mustAccount(t, s, "Bob", store.RolePlayer)
	p, _ := s.CreateProfile(ctx, ann.ID, "Hero", "tiny-rules", "1.0.0")
	other, _ := s.CreateProfile(ctx, ann.ID, "Other", "tiny-rules", "1.0.0")
	b := store.Bind{World: w.ID, Account: ann.ID, Name: "Ann", Profile: p.ID, Pack: "dragon-isle", Version: "1.0.0",
		Imports: []store.Change{{Key: "flags.rescued_fox", Value: []byte{2}}}}
	c, err := s.BindProfile(ctx, b)
	if err != nil || c.Profile != p.ID || c.Name != "Ann" {
		t.Fatalf("bind = %+v, %v", c, err)
	}
	if vals, _ := s.PlayerState(ctx, c.ID); string(vals["flags.rescued_fox"]) != "\x02" {
		t.Errorf("imports = %v", vals)
	}
	// Again: no second import over what the story changed meanwhile.
	if err := s.Save(ctx, store.Save{World: w.ID, Character: c.ID, PlayerState: []store.Change{{Key: "flags.rescued_fox", Value: []byte{1}}}}); err != nil {
		t.Fatal(err)
	}
	if c2, err := s.BindProfile(ctx, b); err != nil || c2.ID != c.ID {
		t.Fatalf("bind again = %+v, %v", c2, err)
	}
	if vals, _ := s.PlayerState(ctx, c.ID); string(vals["flags.rescued_fox"]) != "\x01" {
		t.Errorf("imports written twice: %v", vals)
	}
	if got, _ := s.Character(ctx, w.ID, ann.ID, "Ann"); got.Profile != p.ID {
		t.Errorf("Character = %+v", got)
	}
	b.Profile = other.ID
	if _, err := s.BindProfile(ctx, b); !errors.Is(err, store.ErrProfileChanged) {
		t.Errorf("another profile: %v", err)
	}
	b.Account = bob.ID
	if _, err := s.BindProfile(ctx, b); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("someone else's profile: %v", err)
	}
	prog, err := s.AccountProgress(ctx, ann.ID)
	if err != nil || len(prog) != 1 || prog[0] != (store.Progress{Profile: p.ID, Pack: "dragon-isle", Version: "1.0.0", Status: store.ProgressPlaying}) {
		t.Errorf("progress = %+v, %v", prog, err)
	}
	bound, err := s.BoundProfiles(ctx, ann.ID)
	if err != nil || len(bound) != 1 || bound[w.ID] != p.ID {
		t.Errorf("bound = %v, %v", bound, err)
	}
	if err := s.FinishWorld(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if bound, _ := s.BoundProfiles(ctx, ann.ID); len(bound) != 0 {
		t.Errorf("bound in a finished world: %v", bound)
	}
	if prog, _ := s.AccountProgress(ctx, ann.ID); len(prog) != 1 {
		t.Errorf("progress outlives the world: %+v", prog)
	}
}

// A save writes the sheet, the progress and the exports; once the
// profile migrated, a save from before writes none of them.
func TestSaveProfile(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "forest-start", "1.0.0", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	p, _ := s.CreateProfile(ctx, ann.ID, "Hero", "tiny-rules", "1.0.0")
	c, err := s.BindProfile(ctx, store.Bind{World: w.ID, Account: ann.ID, Name: "Ann", Profile: p.ID, Pack: "forest-start", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	sv := store.Save{World: w.ID, Character: c.ID, Profile: p.ID, ProfileState: []store.Change{{Key: "level", Value: []byte{3, 4}}},
		Progress: &store.ProgressSave{Pack: "forest-start", Version: "1.0.1", Complete: true, Exports: []store.Change{{Key: "flags.rescued_fox", Value: []byte{2}}}}}
	if sv.Empty() {
		t.Fatal("a profile save is empty")
	}
	if err := s.Save(ctx, sv); err != nil {
		t.Fatal(err)
	}
	if vals, _ := s.ProfileState(ctx, p.ID); string(vals["level"]) != "\x03\x04" {
		t.Errorf("sheet = %v", vals)
	}
	if ex, _ := s.Exports(ctx, p.ID, "forest-start"); string(ex["flags.rescued_fox"]) != "\x02" || len(ex) != 1 {
		t.Errorf("exports = %v", ex)
	}
	prog, _ := s.AccountProgress(ctx, ann.ID)
	if len(prog) != 1 || prog[0].Status != store.ProgressCompleted || prog[0].Version != "1.0.1" {
		t.Errorf("progress = %+v", prog)
	}
	// Completed stays completed; a nil export goes.
	sv = store.Save{World: w.ID, Character: c.ID, Profile: p.ID,
		Progress: &store.ProgressSave{Pack: "forest-start", Version: "1.0.1", Exports: []store.Change{{Key: "flags.rescued_fox"}}}}
	if err := s.Save(ctx, sv); err != nil {
		t.Fatal(err)
	}
	if prog, _ := s.AccountProgress(ctx, ann.ID); prog[0].Status != store.ProgressCompleted {
		t.Errorf("completed went back: %+v", prog)
	}
	if ex, _ := s.Exports(ctx, p.ID, "forest-start"); len(ex) != 0 {
		t.Errorf("exports = %v", ex)
	}

	// A save naming a profile its character does not play changes nothing.
	other, _ := s.CreateProfile(ctx, ann.ID, "Other", "tiny-rules", "1.0.0")
	if err := s.Save(ctx, store.Save{World: w.ID, Character: c.ID, Profile: other.ID, ProfileState: []store.Change{{Key: "level", Value: []byte{9}}}}); err != nil {
		t.Fatal(err)
	}
	if vals, _ := s.ProfileState(ctx, other.ID); len(vals) != 0 {
		t.Errorf("a save wrote another profile: %v", vals)
	}

	// After a migration, the save of a world that loaded generation 0
	// writes nothing to the sheet, but the rest of the save.
	if err := s.MigrateProfile(ctx, p.ID, 0, "1.0.0", "2.0.0", map[string][]byte{"stats": {6}}); err != nil {
		t.Fatal(err)
	}
	sv = store.Save{World: w.ID, Character: c.ID, Room: "glade", Profile: p.ID, ProfileGen: 0, ProfileState: []store.Change{{Key: "level", Value: []byte{3, 8}}}}
	if err := s.Save(ctx, sv); err != nil {
		t.Fatal(err)
	}
	if vals, _ := s.ProfileState(ctx, p.ID); !maps.EqualFunc(vals, map[string][]byte{"stats": {6}}, func(a, b []byte) bool { return string(a) == string(b) }) {
		t.Errorf("an old save wrote the migrated sheet: %v", vals)
	}
	if got, _ := s.Character(ctx, w.ID, ann.ID, "Ann"); got.Room != "glade" {
		t.Errorf("room = %q", got.Room)
	}
}

// A migration keeps a snapshot; undo brings it back once.
func TestMigrateAndUndoProfile(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	p, _ := s.CreateProfile(ctx, ann.ID, "Hero", "tiny-rules", "1.0.0")
	w, _ := s.OpenWorld(ctx, "forest-start", "1.0.0", nil)
	c, _ := s.BindProfile(ctx, store.Bind{World: w.ID, Account: ann.ID, Name: "Ann", Profile: p.ID, Pack: "forest-start", Version: "1.0.0"})
	if err := s.Save(ctx, store.Save{World: w.ID, Character: c.ID, Profile: p.ID, ProfileState: []store.Change{{Key: "level", Value: []byte{3, 4}}, {Key: "name", Value: []byte{5}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UndoProfile(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("undo without a migration: %v", err)
	}
	if err := s.MigrateProfile(ctx, p.ID, 0, "1.1.0", "2.0.0", nil); !errors.Is(err, store.ErrProfileChanged) {
		t.Errorf("migrate from the wrong version: %v", err)
	}
	if err := s.MigrateProfile(ctx, p.ID, 1, "1.0.0", "2.0.0", nil); !errors.Is(err, store.ErrProfileChanged) {
		t.Errorf("migrate from the wrong generation: %v", err)
	}
	if err := s.MigrateProfile(ctx, p.ID, 0, "1.0.0", "2.0.0", map[string][]byte{"stats": {6}}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Profile(ctx, p.ID)
	if got.Version != "2.0.0" || got.Generation != 1 || got.Undo != "1.0.0" {
		t.Errorf("migrated = %+v", got)
	}
	if err := s.SetProfileVersion(ctx, p.ID, 0, "2.1.0"); !errors.Is(err, store.ErrProfileChanged) {
		t.Errorf("set version at an old generation: %v", err)
	}
	if err := s.SetProfileVersion(ctx, p.ID, 1, "2.1.0"); err != nil {
		t.Fatal(err)
	}
	// A second migration keeps only the newest snapshot.
	if err := s.MigrateProfile(ctx, p.ID, 1, "2.1.0", "3.0.0", map[string][]byte{"stats": {7}}); err != nil {
		t.Fatal(err)
	}
	undone, err := s.UndoProfile(ctx, p.ID)
	if err != nil || undone.Version != "2.1.0" || undone.Generation != 3 || undone.Undo != "" {
		t.Errorf("undo = %+v, %v", undone, err)
	}
	if vals, _ := s.ProfileState(ctx, p.ID); len(vals) != 1 || string(vals["stats"]) != "\x06" {
		t.Errorf("sheet after undo = %v", vals)
	}
	if _, err := s.UndoProfile(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a second undo: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM profile_snapshot_state`); n != 0 {
		t.Errorf("snapshot rows left: %d", n)
	}
}
