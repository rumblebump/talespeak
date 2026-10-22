package hub

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// versionPack is pack v of id "v", whose one room's title names the version.
func versionPack(t *testing.T, version string) *pack.Pack {
	t.Helper()
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml":       {Data: []byte("id: v\nversion: " + version + "\ntitle: V\nstart: hall\n")},
		"rooms/hall.yaml": {Data: []byte("id: hall\ntitle: Hall " + version + "\ndescription: A hall.\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// versionHub is a hub of worlds of pack v with the versions installed.
func versionHub(t *testing.T, st *worlds, versions ...string) *Hub {
	t.Helper()
	var ps []*pack.Pack
	for _, v := range versions {
		ps = append(ps, versionPack(t, v))
	}
	h := New(Config{Store: st, Packs: catalog(t, ps...), Log: quiet,
		Load: func(w store.World, p *pack.Pack) (*game.World, error) {
			if w.PackVersion != p.Version {
				t.Errorf("world %d loads %s but records %q", w.ID, p.Name(), w.PackVersion)
			}
			return game.NewWorld(p, script.DefaultLimits, quiet)
		}})
	t.Cleanup(h.Close)
	return h
}

// enters joins world and returns the title of the room the player is in,
// or the refusal.
func enters(t *testing.T, h *Hub, world int64) string {
	t.Helper()
	s, ms := h.NewSession("", ws.Who{World: world})
	if len(ms) == 0 {
		t.Fatalf("no messages joining world %d", world)
	}
	if s == nil {
		return ms[0].Message
	}
	t.Cleanup(s.Close)
	return ms[0].Title
}

// Each world runs the newest version compatible with the one it recorded,
// and records the version it moves to; worlds of two major versions run
// side by side; a world no installed version can run is refused.
func TestWorldVersions(t *testing.T) {
	st := &worlds{m: map[int64]store.World{
		1: {ID: 1, PackID: "v", PackVersion: "1.0.0"},
		2: {ID: 2, PackID: "v", PackVersion: "2.0.0"},
		3: {ID: 3, PackID: "v"}, // from before packs had versions
		4: {ID: 4, PackID: "v", PackVersion: "3.0.0"},
		5: {ID: 5, PackID: "v", PackVersion: "1.5.0"}, // ran a version newer than any 1.x installed
		6: {ID: 6, PackID: "v", PackVersion: "2.1.0-alpha"},
	}}
	h := versionHub(t, st, "1.0.0", "1.2.0", "2.0.0", "2.1.0-beta")
	for _, tt := range []struct {
		world        int64
		want, record string
	}{
		{1, "Hall 1.2.0", "1.2.0"},
		{2, "Hall 2.0.0", "2.0.0"}, // a release before a pre-release
		{6, "Hall 2.1.0-beta", "2.1.0-beta"},
		{3, "Hall 2.0.0", "2.0.0"},
		{4, errNoVer.Error(), "3.0.0"},
		{5, errNoVer.Error(), "1.5.0"},
	} {
		if got := enters(t, h, tt.world); got != tt.want {
			t.Errorf("world %d: %q, want %q", tt.world, got, tt.want)
		}
		if w, _ := st.World(t.Context(), tt.world); w.PackVersion != tt.record {
			t.Errorf("world %d records %q, want %q", tt.world, w.PackVersion, tt.record)
		}
	}
	if h.Loaded() != 4 {
		t.Errorf("%d worlds loaded, want 4", h.Loaded())
	}
	if p, err := h.Pack(store.World{PackID: "nope"}); p != nil || err != errNoPack {
		t.Errorf("Pack of a pack not installed = %v, %v", p, err)
	}
	if p, err := h.Pack(store.World{PackID: "v", PackVersion: "1.1.0"}); err != nil || p.Version != "1.2.0" {
		t.Errorf("Pack(1.1.0) = %v, %v", p, err)
	}
}

// A world recorded at a version it moved to never moves back: after an
// update and a downgrade of the pack, it is refused instead of running
// state the newer version wrote on the older one.
func TestWorldVersionNeverBack(t *testing.T) {
	st := &worlds{m: map[int64]store.World{1: {ID: 1, PackID: "v", PackVersion: "1.0.0"}}}
	if got := enters(t, versionHub(t, st, "1.0.0", "1.1.0"), 1); got != "Hall 1.1.0" {
		t.Fatalf("after the update: %q", got)
	}
	if got := enters(t, versionHub(t, st, "1.0.0"), 1); got != errNoVer.Error() {
		t.Errorf("after the downgrade: %q, want %q", got, errNoVer)
	}
}

// A world whose version cannot be recorded does not load.
func TestWorldVersionNotRecorded(t *testing.T) {
	h := New(Config{Store: versionless{&worlds{m: map[int64]store.World{1: {ID: 1, PackID: "v", PackVersion: "1.0.0"}}}},
		Packs: catalog(t, versionPack(t, "1.1.0")), Log: quiet,
		Load: func(store.World, *pack.Pack) (*game.World, error) {
			t.Error("loaded without recording the version")
			return nil, errLoad
		}})
	defer h.Close()
	if got := enters(t, h, 1); got != errLoad.Error() {
		t.Errorf("join = %q, want %q", got, errLoad)
	}
	if h.Loaded() != 0 {
		t.Errorf("%d loaded", h.Loaded())
	}
}

// versionless is a store that cannot record versions.
type versionless struct{ *worlds }

func (versionless) SetPackVersion(ctx context.Context, _ int64, _ string) error {
	return errors.New("read-only")
}
