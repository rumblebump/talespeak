package profile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
)

// The packs of content/ruleset-test: tiny-rules 1.0.0 and 2.0.0, and the
// stories forest-start, dragon-isle (continues forest-start) and
// sky-tower (on 2.x).
func loadPack(t *testing.T, name string) *pack.Pack {
	t.Helper()
	p, err := pack.Load(os.DirFS(filepath.Join("..", "..", "content", "ruleset-test", name)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return p
}

type fixture struct {
	t      *testing.T
	st     *sqlite.Store
	cat    *pack.Catalog
	s      *Service
	ann    store.Account
	worlds map[string]store.World // by pack id
}

func newFixture(t *testing.T, packs ...*pack.Pack) *fixture {
	t.Helper()
	if len(packs) == 0 {
		for _, name := range []string{"tiny-rules", "tiny-rules-2", "forest-start", "dragon-isle", "sky-tower"} {
			packs = append(packs, loadPack(t, name))
		}
	}
	cat, err := pack.NewCatalog(packs...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{t: t, st: st, cat: cat, worlds: map[string]store.World{}}
	f.s = New(Config{Store: st, Packs: cat, Limits: script.DefaultLimits, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Runs: func(w store.World) (*pack.Pack, error) { return cat.Pick(w.PackID, w.PackVersion) }})
	if f.ann, err = st.CreateAccount(ctx, "Ann", "x", store.RolePlayer); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"forest-start", "dragon-isle", "sky-tower"} {
		if cat.Newest(id) == nil {
			continue
		}
		w, err := st.CreateWorld(ctx, store.NewWorld{PackID: id, PackVersion: cat.Newest(id).Version, Visibility: store.VisibilityPublic, CreatedBy: f.ann.ID})
		if err != nil {
			t.Fatal(err)
		}
		f.worlds[id] = w
	}
	return f
}

func (f *fixture) cards() map[int64]*Card {
	f.t.Helper()
	var list []store.World
	for _, w := range f.worlds {
		list = append(list, w)
	}
	cards, err := f.s.Cards(context.Background(), f.ann.ID, list)
	if err != nil {
		f.t.Fatal(err)
	}
	return cards
}

func (f *fixture) card(pack string) *Card { return f.cards()[f.worlds[pack].ID] }

func (f *fixture) pick(pack string, id int64, name string) (Option, error) {
	return f.s.Pick(context.Background(), f.ann.ID, "Ann", f.worlds[pack], id, name)
}

// finish plays pack to its end for profile p, as progress.export and
// progress.complete do in a hook.
func (f *fixture) finish(pack string, p int64, exports ...store.Change) {
	f.t.Helper()
	ctx := context.Background()
	c, err := f.st.Character(ctx, f.worlds[pack].ID, f.ann.ID, "Ann")
	if err != nil {
		f.t.Fatal(err)
	}
	sv := store.Save{World: f.worlds[pack].ID, Character: c.ID, Profile: p,
		Progress: &store.ProgressSave{Pack: pack, Version: "1.0.0", Complete: true, Exports: exports}}
	if err := f.st.Save(ctx, sv); err != nil {
		f.t.Fatal(err)
	}
}

// A new profile plays a story on its ruleset; the card then shows it as
// the world's character.
func TestPickNewProfile(t *testing.T) {
	f := newFixture(t)
	c := f.card("forest-start")
	if c == nil || c.Ruleset != "Tiny Rules 1.0.0" || !c.New || c.Locked != "" || len(c.Options) != 0 || c.Bound != nil {
		t.Fatalf("card = %+v", c)
	}
	o, err := f.pick("forest-start", 0, "  Fox   Friend ")
	if err != nil || !o.OK || o.Name != "Fox Friend" || o.Version != "1.0.0" {
		t.Fatalf("pick = %+v, %v", o, err)
	}
	c = f.card("forest-start")
	if c.Bound == nil || c.Bound.ID != o.ID || !c.Bound.OK || c.New {
		t.Errorf("card after = %+v", c)
	}
	// Picking again is fine; another profile is not.
	if _, err := f.pick("forest-start", o.ID, ""); err != nil {
		t.Errorf("again: %v", err)
	}
	var refused *Refusal
	if _, err := f.pick("forest-start", 0, "Other"); !errors.As(err, &refused) || !strings.Contains(refused.Reason, "is Fox Friend") {
		t.Errorf("a second profile: %v", err)
	}
	// The same name again, in another world.
	if _, err := f.pick("sky-tower", 0, "fox friend"); !errors.As(err, &refused) || !strings.Contains(refused.Reason, "named fox friend already") {
		t.Errorf("same name: %v", err)
	}
	if _, err := f.pick("sky-tower", 0, "1x"); !errors.As(err, &refused) {
		t.Errorf("bad name: %v", err)
	}
	if _, err := f.pick("sky-tower", 9999, ""); !errors.As(err, &refused) || refused.Reason != "That character does not exist." {
		t.Errorf("unknown profile: %v", err)
	}
}

// A 2.x profile cannot go back to a 1.x story, and a profile of another
// ruleset never fits.
func TestIncompatibleProfiles(t *testing.T) {
	f := newFixture(t)
	sky, err := f.pick("sky-tower", 0, "Climber")
	if err != nil || sky.Version != "2.0.0" {
		t.Fatalf("pick = %+v, %v", sky, err)
	}
	// Listed with the reason, and a new profile is still possible.
	c := f.card("forest-start")
	if len(c.Options) != 1 || c.Options[0].OK || !strings.Contains(c.Options[0].Reason, "older 1.0.0") || !c.New || c.Locked != "" {
		t.Errorf("card = %+v", c)
	}
	story, rules := loadPack(t, "forest-start"), loadPack(t, "tiny-rules")
	p, _ := f.st.Profile(context.Background(), sky.ID)
	if fit := Check(story, rules, f.cat, p, nil, true); fit.OK || !strings.Contains(fit.Reason, "plays Tiny Rules 2.0.0, and this world runs the older 1.0.0") {
		t.Errorf("2.x in 1.x: %+v", fit)
	}
	var refused *Refusal
	if _, err := f.pick("forest-start", sky.ID, ""); !errors.As(err, &refused) || !strings.Contains(refused.Reason, "older 1.0.0") {
		t.Errorf("pick 2.x for 1.x: %v", err)
	}
	p.Ruleset = "dnd5e"
	if fit := Check(story, rules, f.cat, p, nil, true); fit.OK || fit.Reason != "Climber is a character of dnd5e, and this story is for Tiny Rules." {
		t.Errorf("other ruleset: %+v", fit)
	}
	p.Ruleset, p.Version = "tiny-rules", "0.9.0"
	if fit := Check(story, rules, f.cat, p, nil, true); fit.OK || !strings.Contains(fit.Reason, "Nothing moves Climber") {
		t.Errorf("0.x: %+v", fit)
	}
	p.Version = "x"
	if fit := Check(story, rules, f.cat, p, nil, true); fit.OK {
		t.Errorf("bad version fits: %+v", fit)
	}
	// An older version of the same line fits and moves up.
	p.Version = "1.0.0"
	newer := *rules
	newer.Version, newer.V = "1.2.0", pack.Version{Major: 1, Minor: 2}
	if fit := Check(story, &newer, f.cat, p, nil, true); !fit.OK || fit.Upgrade != "" {
		t.Errorf("1.0 in 1.2: %+v", fit)
	}
}

// Dragon Isle is locked until Forest Start is finished, then carries the
// exported flag into the new world.
func TestContinuation(t *testing.T) {
	f := newFixture(t)
	c := f.card("dragon-isle")
	if c.New || c.Locked != "Finish Forest Start first." {
		t.Fatalf("card = %+v", c)
	}
	var refused *Refusal
	if _, err := f.pick("dragon-isle", 0, "Hero"); !errors.As(err, &refused) || refused.Reason != "Finish Forest Start first." {
		t.Errorf("new profile: %v", err)
	}
	hero, err := f.pick("forest-start", 0, "Hero")
	if err != nil {
		t.Fatal(err)
	}
	c = f.card("dragon-isle")
	if len(c.Options) != 1 || c.Options[0].OK || c.Options[0].Reason != "Finish Forest Start first." || c.Locked != "Finish Forest Start first." {
		t.Errorf("card while playing = %+v", c)
	}
	f.finish("forest-start", hero.ID, store.Change{Key: "flags.rescued_fox", Value: []byte{2}})
	c = f.card("dragon-isle")
	if len(c.Options) != 1 || !c.Options[0].OK || c.Locked != "" || c.New {
		t.Errorf("card after = %+v", c)
	}
	if _, err := f.pick("dragon-isle", hero.ID, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ch, _ := f.st.Character(ctx, f.worlds["dragon-isle"].ID, f.ann.ID, "Ann")
	vals, _ := f.st.PlayerState(ctx, ch.ID)
	if ch.Profile != hero.ID || string(vals["flags.rescued_fox"]) != "\x02" {
		t.Errorf("character = %+v, state %v", ch, vals)
	}
	if c := f.card("dragon-isle"); c.Bound == nil || c.Bound.ID != hero.ID {
		t.Errorf("bound = %+v", c)
	}
}

// A 1.x profile migrates to enter a 2.x story; undo brings its sheet
// back.
func TestMigration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hero, err := f.pick("forest-start", 0, "Hero")
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := f.st.Character(ctx, f.worlds["forest-start"].ID, f.ann.ID, "Ann")
	level := []byte{3, 6} // the encoding of 3
	if err := f.st.Save(ctx, store.Save{World: f.worlds["forest-start"].ID, Character: ch.ID, Profile: hero.ID,
		ProfileState: []store.Change{{Key: "level", Value: level}}}); err != nil {
		t.Fatal(err)
	}
	c := f.card("sky-tower")
	if len(c.Options) != 1 || !c.Options[0].OK || c.Options[0].Upgrade != "2.0.0" || !c.New {
		t.Fatalf("card = %+v", c)
	}
	o, err := f.pick("sky-tower", hero.ID, "")
	if err != nil || o.Version != "2.0.0" {
		t.Fatalf("pick = %+v, %v", o, err)
	}
	p, _ := f.st.Profile(ctx, hero.ID)
	sheet, _ := f.st.ProfileState(ctx, hero.ID)
	if p.Version != "2.0.0" || p.Undo != "1.0.0" || p.Generation != 1 || sheet["level"] != nil || sheet["stats"] == nil {
		t.Errorf("migrated = %+v, sheet %v", p, sheet)
	}
	// The forest world now needs it checked again: it no longer fits.
	if c := f.card("forest-start"); c.Bound == nil || c.Bound.OK {
		t.Errorf("forest card = %+v", c.Bound)
	}
	undone, err := f.st.UndoProfile(ctx, hero.ID)
	if err != nil || undone.Version != "1.0.0" {
		t.Fatalf("undo = %+v, %v", undone, err)
	}
	sheet, _ = f.st.ProfileState(ctx, hero.ID)
	if string(sheet["level"]) != string(level) || sheet["stats"] != nil {
		t.Errorf("sheet after undo = %v", sheet)
	}
	if c := f.card("forest-start"); c.Bound == nil || !c.Bound.OK {
		t.Errorf("forest after undo = %+v", c.Bound)
	}
	// The sky world's character is bound to it; picking it again migrates again.
	if o, err := f.pick("sky-tower", hero.ID, ""); err != nil || o.Version != "2.0.0" {
		t.Errorf("again = %+v, %v", o, err)
	}
}

// A migration that fails leaves the profile as it was.
func TestMigrationFails(t *testing.T) {
	broken := fstest.MapFS{
		"pack.yaml":        {Data: []byte("id: tiny-rules\nversion: 2.0.0\nkind: ruleset\ntitle: Tiny Rules\n")},
		"migrations/2.lua": {Data: []byte(`function migrate(s) error("bad sheet") end`)},
		"lib/sheet.lua":    {Data: []byte("return {}")},
	}
	b, err := pack.Load(broken)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, loadPack(t, "tiny-rules"), b, loadPack(t, "forest-start"), loadPack(t, "sky-tower"))
	hero, err := f.pick("forest-start", 0, "Hero")
	if err != nil {
		t.Fatal(err)
	}
	var refused *Refusal
	if _, err := f.pick("sky-tower", hero.ID, ""); !errors.As(err, &refused) || !strings.Contains(refused.Reason, "migration failed") {
		t.Errorf("pick = %v", err)
	}
	p, _ := f.st.Profile(context.Background(), hero.ID)
	if p.Version != "1.0.0" || p.Undo != "" || p.Generation != 0 {
		t.Errorf("profile = %+v", p)
	}
}

// A ruleset without a migration into each major version moves nobody.
func TestMigrationGap(t *testing.T) {
	r3, err := pack.Load(fstest.MapFS{
		"pack.yaml":        {Data: []byte("id: tiny-rules\nversion: 3.0.0\nkind: ruleset\ntitle: Tiny Rules\n")},
		"migrations/3.lua": {Data: []byte(`function migrate(s) end`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := store.Profile{ID: 1, Name: "Hero", Ruleset: "tiny-rules", Version: "1.4.0"}
	if fit := Check(loadPack(t, "sky-tower"), r3, nil, p, nil, false); fit.OK || !strings.Contains(fit.Reason, "Nothing moves Hero from Tiny Rules 1.4.0 to 3.0.0") {
		t.Errorf("gap: %+v", fit)
	}
	p.Version = "2.1.0"
	if fit := Check(loadPack(t, "sky-tower"), r3, nil, p, nil, false); !fit.OK || fit.Upgrade != "3.0.0" {
		t.Errorf("2 to 3: %+v", fit)
	}
}

func TestValidName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Hero", "Hero"}, {"  Fox   Friend ", "Fox Friend"}, {"a-b_c 9", "a-b_c 9"}, {"", ""}, {"   ", ""},
		{"9lives", ""}, {"Zoë", ""}, {"a<b", ""}, {strings.Repeat("a", MaxName), strings.Repeat("a", MaxName)}, {strings.Repeat("a", MaxName+1), ""},
	} {
		got, err := ValidName(tc.in)
		if got != tc.want || (tc.want == "") != (err != nil) {
			t.Errorf("ValidName(%q) = %q, %v", tc.in, got, err)
		}
	}
}

// Worlds without a ruleset get no card, and Pick refuses them.
func TestNoRuleset(t *testing.T) {
	plain, err := pack.Load(fstest.MapFS{
		"pack.yaml":    {Data: []byte("id: plain\nversion: 1.0.0\ntitle: P\nstart: r\n")},
		"rooms/r.yaml": {Data: []byte("id: r\ntitle: R\ndescription: A room.\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, plain, loadPack(t, "tiny-rules"), loadPack(t, "forest-start"))
	w, err := f.st.CreateWorld(context.Background(), store.NewWorld{PackID: "plain", PackVersion: "1.0.0", Visibility: store.VisibilityPublic, CreatedBy: f.ann.ID})
	if err != nil {
		t.Fatal(err)
	}
	cards, err := f.s.Cards(context.Background(), f.ann.ID, []store.World{w})
	if err != nil || len(cards) != 0 {
		t.Errorf("cards = %v, %v", cards, err)
	}
	if _, err := f.s.Pick(context.Background(), f.ann.ID, "Ann", w, 0, "Hero"); !errors.Is(err, ErrNoRuleset) {
		t.Errorf("pick = %v", err)
	}
	gone := store.World{ID: 99, PackID: "missing"}
	var refused *Refusal
	if _, err := f.s.Pick(context.Background(), f.ann.ID, "Ann", gone, 0, "Hero"); !errors.As(err, &refused) {
		t.Errorf("pick without pack = %v", err)
	}
}
