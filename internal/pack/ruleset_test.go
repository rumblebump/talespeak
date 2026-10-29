package pack

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// rulesetTest loads a pack of content/ruleset-test.
func rulesetTest(t *testing.T, name string) *Pack {
	t.Helper()
	p, err := Load(os.DirFS(filepath.Join("..", "..", "content", "ruleset-test", name)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return p
}

func TestLoadRulesetPacks(t *testing.T) {
	rules := rulesetTest(t, "tiny-rules")
	if !rules.IsRuleset() || rules.Start != "" || len(rules.Rooms) != 0 || rules.Modules["sheet"] == "" || len(rules.Migrations) != 0 {
		t.Errorf("tiny-rules = %+v", rules)
	}
	rules2 := rulesetTest(t, "tiny-rules-2")
	if !strings.Contains(rules2.Migrations[2], "function migrate(sheet)") {
		t.Errorf("tiny-rules 2 migrations = %v", rules2.Migrations)
	}
	forest := rulesetTest(t, "forest-start")
	if forest.Kind != KindStory || forest.Ruleset == nil || forest.Ruleset.ID != "tiny-rules" || !forest.Ruleset.R.Contains(rules.V) ||
		forest.Ruleset.R.Contains(rules2.V) || !forest.Exported("flags.rescued_fox") || forest.Exported("flags.other") {
		t.Errorf("forest-start = %+v", forest.Manifest)
	}
	dragon := rulesetTest(t, "dragon-isle")
	if len(dragon.Continues) != 1 || dragon.Continues[0].ID != "forest-start" || !dragon.Continues[0].Completed ||
		!dragon.Continues[0].R.Contains(forest.V) || len(dragon.Imports) != 1 {
		t.Errorf("dragon-isle = %+v", dragon.Manifest)
	}
	demo := rulesetTest(t, "sky-tower")
	if demo.Ruleset.R.Contains(rules.V) || !demo.Ruleset.R.Contains(rules2.V) {
		t.Errorf("sky-tower range = %s", demo.Ruleset.R)
	}
}

func TestManifestErrors(t *testing.T) {
	const story = "id: t\nversion: 1.0.0\ntitle: T\nstart: r\n"
	const rules = "id: t\nversion: 3.0.0\ntitle: T\nkind: ruleset\n"
	for _, tc := range []struct {
		name  string
		files map[string]*string
		want  string
	}{
		{"unknown kind", map[string]*string{"pack.yaml": s(story + "kind: map\n")}, "must be story or ruleset"},
		{"bad ruleset id", map[string]*string{"pack.yaml": s(story + "ruleset: {id: Bad, version: '1'}\n")}, "ruleset id"},
		{"bad ruleset range", map[string]*string{"pack.yaml": s(story + "ruleset: {id: r, version: '^x'}\n")}, "ruleset version"},
		{"no ruleset range", map[string]*string{"pack.yaml": s(story + "ruleset: {id: r}\n")}, "ruleset version"},
		{"continues itself", map[string]*string{"pack.yaml": s(story + "ruleset: {id: r, version: '1'}\ncontinues: [{id: t}]\n")}, "cannot continue itself"},
		{"continues twice", map[string]*string{"pack.yaml": s(story + "ruleset: {id: r, version: '1'}\ncontinues: [{id: a}, {id: a}]\n")}, "listed twice"},
		{"continues bad range", map[string]*string{"pack.yaml": s(story + "ruleset: {id: r, version: '1'}\ncontinues: [{id: a, version: '>=x'}]\n")}, "continues a"},
		{"continues without ruleset", map[string]*string{"pack.yaml": s(story + "continues: [{id: a}]\n")}, "need a ruleset"},
		{"exports without ruleset", map[string]*string{"pack.yaml": s(story + "exports: [flags.a]\n")}, "need a ruleset"},
		{"imports without continues", map[string]*string{"pack.yaml": s(story + "ruleset: {id: r, version: '1'}\nimports: [flags.a]\n")}, "there are none"},
		{"bad export name", map[string]*string{"pack.yaml": s(story + "ruleset: {id: r, version: '1'}\nexports: [level]\n")}, "flags.<name> or items.<name>"},
		{"export twice", map[string]*string{"pack.yaml": s(story + "ruleset: {id: r, version: '1'}\nexports: [items.a, items.a]\n")}, "listed twice"},
		{"too many exports", map[string]*string{"pack.yaml": s(story + "ruleset: {id: r, version: '1'}\nexports: [" + manyNames(MaxCarried+1) + "]\n")}, "at most 50"},
		{"ruleset with start", map[string]*string{"pack.yaml": s(rules + "start: r\n")}, "no start room"},
		{"ruleset with rooms", map[string]*string{"pack.yaml": s(rules)}, "a ruleset pack has no rooms"},
		{"ruleset with exports", map[string]*string{"pack.yaml": s(rules + "exports: [flags.a]\n"), "rooms/r.yaml": nil}, "belong to story packs"},
		{"migration name", map[string]*string{"pack.yaml": s(rules), "rooms/r.yaml": nil, "migrations/two.lua": s("")}, "<major>.lua"},
		{"migration leading zero", map[string]*string{"pack.yaml": s(rules), "rooms/r.yaml": nil, "migrations/02.lua": s("")}, "<major>.lua"},
		{"migration into 1", map[string]*string{"pack.yaml": s(rules), "rooms/r.yaml": nil, "migrations/1.lua": s("")}, "from 2 to"},
		{"migration past the version", map[string]*string{"pack.yaml": s(rules), "rooms/r.yaml": nil, "migrations/4.lua": s("")}, "from 2 to"},
		{"migration offline", map[string]*string{"pack.yaml": s(rules), "rooms/r.yaml": nil, "migrations/2.lua": s("x = 'https://example.com'")}, "migrations/2.lua"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(fsWith(tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func manyNames(n int) string {
	var names []string
	for i := range n {
		names = append(names, "flags.f"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	return strings.Join(names, ", ")
}

func TestManifestDefaults(t *testing.T) {
	p, err := Load(fsWith(map[string]*string{"pack.yaml": s(goodPack + "ruleset: {id: r, version: '^1'}\ncontinues: [{id: a}]\n")}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != KindStory || p.Continues[0].Version != "*" || p.Continues[0].Completed {
		t.Errorf("manifest = %+v", p.Manifest)
	}
	plain, err := Load(fsWith(nil))
	if err != nil || plain.Kind != KindStory || plain.Ruleset != nil || plain.IsRuleset() {
		t.Errorf("plain pack = %+v, %v", plain, err)
	}
}

func TestMigrationDirectoryIsAnError(t *testing.T) {
	fsys := fstest.MapFS{
		"pack.yaml":          {Data: []byte("id: t\nversion: 2.0.0\ntitle: T\nkind: ruleset\n")},
		"migrations/2/x.lua": {Data: []byte("")},
	}
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "<major>.lua") {
		t.Errorf("err = %v", err)
	}
}

func TestCatalogRuleset(t *testing.T) {
	rules, rules2 := rulesetTest(t, "tiny-rules"), rulesetTest(t, "tiny-rules-2")
	forest, sky := rulesetTest(t, "forest-start"), rulesetTest(t, "sky-tower")
	c, err := NewCatalog(rules, rules2, forest, sky)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Check(); err != nil {
		t.Errorf("Check = %v", err)
	}
	if r, err := c.Ruleset(forest); err != nil || r != rules {
		t.Errorf("forest ruleset = %v, %v; want 1.0.0", r, err)
	}
	if r, err := c.Ruleset(sky); err != nil || r != rules2 {
		t.Errorf("sky ruleset = %v, %v; want 2.0.0", r, err)
	}
	if r, err := c.Ruleset(rules); r != nil || err != nil {
		t.Errorf("a pack without ruleset: %v, %v", r, err)
	}
	if c.Title("forest-start") != "Forest Start" || c.Title("gone") != "gone" {
		t.Errorf("titles: %q %q", c.Title("forest-start"), c.Title("gone"))
	}

	// Without 2.x, sky-tower has no ruleset.
	c, _ = NewCatalog(rules, forest, sky)
	if _, err := c.Ruleset(sky); !errors.Is(err, ErrNoRuleset) {
		t.Errorf("sky without 2.x: %v", err)
	}
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "sky-tower") {
		t.Errorf("Check = %v", err)
	}

	// A story named as a ruleset.
	odd, err := Load(fsWith(map[string]*string{"pack.yaml": s(goodPack + "ruleset: {id: forest-start, version: '*'}\n")}))
	if err != nil {
		t.Fatal(err)
	}
	c, _ = NewCatalog(forest, odd, rules)
	if _, err := c.Ruleset(odd); !errors.Is(err, ErrNoRuleset) || !strings.Contains(err.Error(), "story pack") {
		t.Errorf("story as ruleset: %v", err)
	}

	// One id, two kinds.
	fake, err := Load(fsWith(map[string]*string{"pack.yaml": s("id: tiny-rules\nversion: 3.0.0\ntitle: T\nstart: r\n")}))
	if err != nil {
		t.Fatal(err)
	}
	c, _ = NewCatalog(rules, fake)
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "ruleset in one version and a story") {
		t.Errorf("Check = %v", err)
	}
}
