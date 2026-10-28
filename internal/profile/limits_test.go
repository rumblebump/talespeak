package profile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
)

// rules2 is a Tiny Rules 2.0.0 whose migration is src.
func rules2(t *testing.T, src string) *pack.Pack {
	t.Helper()
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml":        {Data: []byte("id: tiny-rules\nversion: 2.0.0\nkind: ruleset\ntitle: Tiny Rules\n")},
		"migrations/2.lua": {Data: []byte(src)},
		"lib/sheet.lua":    {Data: []byte("return {}")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// withLimits makes f's Service run migrations with the state limits st,
// as TALESPEAK_STATE_* set them.
func (f *fixture) withLimits(st script.StateLimits) {
	l := script.DefaultLimits
	l.State = st
	f.s = New(Config{Store: f.s.cfg.Store, Packs: f.cat, Limits: l, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Runs: f.s.cfg.Runs})
}

// The server's state limits hold for what a migration returns: lower
// ones refuse a sheet the defaults keep, higher ones keep one the
// defaults refuse. A refused migration leaves the profile as it was.
func TestMigrationConfiguredLimits(t *testing.T) {
	const manyKeys = `function migrate(s) for i = 1, 11 do s["k" .. i] = i end end`
	const bigValue = `function migrate(s) s.notes = string.rep("x", 30000) end`
	for _, tc := range []struct {
		name   string
		src    string
		limits script.StateLimits
		ok     bool
	}{
		{"defaults keep 11 keys", manyKeys, script.StateLimits{}, true},
		{"10 keys at most", manyKeys, script.StateLimits{PlayerKeys: 10}, false},
		{"defaults refuse a large value", bigValue, script.StateLimits{}, false},
		{"higher value limit", bigValue, script.StateLimits{ValueBytes: 64 << 10}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, loadPack(t, "tiny-rules"), rules2(t, tc.src), loadPack(t, "forest-start"), loadPack(t, "sky-tower"))
			f.withLimits(tc.limits)
			hero, err := f.pick("forest-start", 0, "Hero")
			if err != nil {
				t.Fatal(err)
			}
			o, err := f.pick("sky-tower", hero.ID, "")
			ctx := context.Background()
			p, _ := f.st.Profile(ctx, hero.ID)
			sheet, _ := f.st.ProfileState(ctx, hero.ID)
			if tc.ok {
				if err != nil || o.Version != "2.0.0" || p.Version != "2.0.0" || len(sheet) == 0 {
					t.Errorf("pick = %+v, %v; profile %+v, %d keys", o, err, p, len(sheet))
				}
				return
			}
			var refused *Refusal
			if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "migration failed") {
				t.Errorf("pick = %v", err)
			}
			if p.Version != "1.0.0" || p.Generation != 0 || len(sheet) != 0 {
				t.Errorf("refused migration changed the profile: %+v, %d keys", p, len(sheet))
			}
		})
	}
}

// changingStore is a store whose profile changes (another tab, a
// migration elsewhere) between Pick's checks and its write.
type changingStore struct {
	store.Profiles
	version, migrate bool // which write finds the profile changed
}

func (c *changingStore) SetProfileVersion(ctx context.Context, profile, gen int64, version string) error {
	if c.version {
		return store.ErrProfileChanged
	}
	return c.Profiles.SetProfileVersion(ctx, profile, gen, version)
}

func (c *changingStore) MigrateProfile(ctx context.Context, profile, gen int64, from, to string, state map[string][]byte) error {
	if c.migrate {
		return store.ErrProfileChanged
	}
	return c.Profiles.MigrateProfile(ctx, profile, gen, from, to, state)
}

// A profile that changed while Pick moved it up is refused with a
// sentence to try again, and keeps its version.
func TestPickProfileChangedMeanwhile(t *testing.T) {
	newer := *loadPack(t, "tiny-rules")
	newer.Version, newer.V = "1.1.0", pack.Version{Major: 1, Minor: 1}
	for _, tc := range []struct {
		name  string
		packs func() []*pack.Pack
		cs    changingStore
		world string
	}{
		{"same line", func() []*pack.Pack {
			return []*pack.Pack{loadPack(t, "tiny-rules"), &newer, loadPack(t, "forest-start")}
		}, changingStore{version: true}, "forest-start"},
		{"migration", func() []*pack.Pack {
			return []*pack.Pack{loadPack(t, "tiny-rules"), loadPack(t, "tiny-rules-2"), loadPack(t, "forest-start"), loadPack(t, "sky-tower")}
		}, changingStore{migrate: true}, "sky-tower"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.packs()...)
			ctx := context.Background()
			// A profile on 1.0.0, made before the newer ruleset version.
			hero, err := f.st.CreateProfile(ctx, f.ann.ID, "Hero", "tiny-rules", "1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			cs := tc.cs
			cs.Profiles = f.st
			f.s = New(Config{Store: &cs, Packs: f.cat, Limits: script.DefaultLimits, Log: f.s.cfg.Log, Runs: f.s.cfg.Runs})
			var refused *Refusal
			if _, err := f.pick(tc.world, hero.ID, ""); !errors.As(err, &refused) || !strings.Contains(refused.Reason, "changed meanwhile") {
				t.Fatalf("pick = %v", err)
			}
			if p, _ := f.st.Profile(ctx, hero.ID); p.Version != "1.0.0" {
				t.Errorf("version = %q", p.Version)
			}
			if c := f.card(tc.world); c.Bound != nil {
				t.Errorf("bound after a refused pick: %+v", c.Bound)
			}
		})
	}
}
