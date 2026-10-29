package game

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
	"github.com/rumblebump/talespeak/internal/ws"
)

// rulesetPack loads a pack of content/ruleset-test.
func rulesetPack(t *testing.T, name string) *pack.Pack {
	t.Helper()
	p, err := pack.Load(os.DirFS(filepath.Join("..", "..", "content", "ruleset-test", name)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return p
}

// rulesetWorld opens a store with a world of story on ruleset, and an
// account Ann with a profile Hero on it.
func rulesetWorld(t *testing.T, story, ruleset *pack.Pack) (*sqlite.Store, store.World, ws.Who, store.Profile) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: real SQLite file")
	}
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	world, err := st.OpenWorld(ctx, story.ID, story.Version, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAccount(ctx, "Ann", "x", store.RolePlayer)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProfile(ctx, a.ID, "Hero", ruleset.ID, ruleset.Version)
	if err != nil {
		t.Fatal(err)
	}
	return st, world, ws.Who{Account: a.ID, Name: a.Name}, p
}

func newRulesetWorld(t *testing.T, st *sqlite.Store, world store.World, story, ruleset *pack.Pack) *World {
	t.Helper()
	w, err := NewWorld(story, script.DefaultLimits, quiet, WithStore(st, world.ID), WithRuleset(ruleset, st))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// A story requires its ruleset's modules and reads pack; a player whose
// character plays no profile is sent to the lobby; one that does keeps
// its sheet, exports and completion in the profile.
func TestRulesetWorld(t *testing.T) {
	forest, rules := rulesetPack(t, "forest-start"), rulesetPack(t, "tiny-rules")
	st, world, who, hero := rulesetWorld(t, forest, rules)
	w := newRulesetWorld(t, st, world, forest, rules)
	ctx := context.Background()

	s, first := w.NewSession("", who)
	if s != nil || errorText(first) != "Pick a character for this world in the lobby first." {
		t.Fatalf("unbound = %v, %q", s, errorText(first))
	}
	if _, err := st.BindProfile(ctx, store.Bind{World: world.ID, Account: who.Account, Name: who.Name, Profile: hero.ID, Pack: forest.ID, Version: forest.Version}); err != nil {
		t.Fatal(err)
	}
	s, first = w.NewSession("", who)
	if s == nil || !strings.Contains(texts(first), "Forest Start 1.0.0 on tiny-rules 1.0.0. Level 1.") {
		t.Fatalf("bound = %q", texts(first))
	}
	if got := texts(s.Handle(ws.Message{Type: "click", Item: "fox"})); got != "You free the fox. Level 2. Forest Start is finished." {
		t.Errorf("click = %q", got)
	}
	s.Close()
	sheet, _ := st.ProfileState(ctx, hero.ID)
	ex, _ := st.Exports(ctx, hero.ID, forest.ID)
	prog, _ := st.AccountProgress(ctx, who.Account)
	if sheet["level"] == nil || ex["flags.rescued_fox"] == nil || len(prog) != 1 || prog[0].Status != store.ProgressCompleted {
		t.Errorf("sheet %v, exports %v, progress %+v", sheet, ex, prog)
	}
	// The sheet comes back on the next join.
	s, first = w.NewSession("", who)
	defer s.Close()
	if !strings.Contains(texts(first), "Level 2.") {
		t.Errorf("again = %q", texts(first))
	}
}

// A profile that moved on to another major version, or that a migration
// changed since the world loaded it, is not played in the old world.
func TestRulesetWorldRechecks(t *testing.T) {
	forest, rules := rulesetPack(t, "forest-start"), rulesetPack(t, "tiny-rules")
	st, world, who, hero := rulesetWorld(t, forest, rules)
	ctx := context.Background()
	if _, err := st.BindProfile(ctx, store.Bind{World: world.ID, Account: who.Account, Name: who.Name, Profile: hero.ID, Pack: forest.ID, Version: forest.Version}); err != nil {
		t.Fatal(err)
	}
	w := newRulesetWorld(t, st, world, forest, rules)
	s, _ := w.NewSession("", who)
	if s == nil {
		t.Fatal("no session")
	}
	// Migrated elsewhere while playing here: the level-up is dropped.
	if err := st.MigrateProfile(ctx, hero.ID, 0, "1.0.0", "2.0.0", map[string][]byte{"stats": {2}}); err != nil {
		t.Fatal(err)
	}
	s.Handle(ws.Message{Type: "click", Item: "fox"})
	s.Close()
	if sheet, _ := st.ProfileState(ctx, hero.ID); sheet["level"] != nil || sheet["stats"] == nil {
		t.Errorf("an old world wrote the migrated sheet: %v", sheet)
	}
	if _, first := w.NewSession("", who); !strings.Contains(errorText(first), "has to be checked again") {
		t.Errorf("2.x in a 1.x world: %q", errorText(first))
	}
}

// A profile on an older version of the line moves up to the world's
// ruleset when it joins.
func TestRulesetWorldMovesUp(t *testing.T) {
	forest, rules := rulesetPack(t, "forest-start"), rulesetPack(t, "tiny-rules")
	newer := *rules
	newer.Version, newer.V = "1.1.0", pack.Version{Major: 1, Minor: 1}
	st, world, who, hero := rulesetWorld(t, forest, rules)
	ctx := context.Background()
	if _, err := st.BindProfile(ctx, store.Bind{World: world.ID, Account: who.Account, Name: who.Name, Profile: hero.ID, Pack: forest.ID, Version: forest.Version}); err != nil {
		t.Fatal(err)
	}
	w := newRulesetWorld(t, st, world, forest, &newer)
	s, first := w.NewSession("", who)
	if s == nil {
		t.Fatalf("join = %q", texts(first))
	}
	defer s.Close()
	if p, _ := st.Profile(ctx, hero.ID); p.Version != "1.1.0" {
		t.Errorf("version = %q", p.Version)
	}
}

// A story module of the same name as a ruleset module wins; without a
// ruleset, state.profile and progress raise an error.
func TestRulesetModulesAndNoProfile(t *testing.T) {
	forest, rules := rulesetPack(t, "forest-start"), rulesetPack(t, "tiny-rules")
	own := *forest
	own.Modules = map[string]string{"sheet": `return {level = function() return 42 end, level_up = function() return 43 end}`}
	st, world, who, hero := rulesetWorld(t, &own, rules)
	ctx := context.Background()
	if _, err := st.BindProfile(ctx, store.Bind{World: world.ID, Account: who.Account, Name: who.Name, Profile: hero.ID, Pack: own.ID, Version: own.Version}); err != nil {
		t.Fatal(err)
	}
	w := newRulesetWorld(t, st, world, &own, rules)
	s, first := w.NewSession("", who)
	if s == nil || !strings.Contains(texts(first), "Level 42.") {
		t.Fatalf("own module: %q", texts(first))
	}
	s.Close()

	// The same story without a ruleset: its script fails to find the
	// module, so the world does not start.
	if _, err := NewWorld(forest, script.DefaultLimits, quiet); err == nil || !strings.Contains(err.Error(), "no module") {
		t.Errorf("without a ruleset: %v", err)
	}
}

// errorText is the message of the first error in ms.
func errorText(ms []ws.Message) string {
	for _, m := range ms {
		if m.Type == "error" {
			return m.Message
		}
	}
	return ""
}
