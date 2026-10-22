package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/account"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/store"
)

// writePack writes version 1.0.0 of a one-room pack with id into dir/name.
func writePack(t *testing.T, dir, name, id string) string {
	t.Helper()
	return writePackVersion(t, dir, name, id, "1.0.0")
}

// writePackVersion writes a one-room pack with id and version into
// dir/name; its title names the version.
func writePackVersion(t *testing.T, dir, name, id, version string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(p, "rooms"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"pack.yaml":       "id: " + id + "\nversion: " + version + "\ntitle: Pack " + id + suffix(version) + "\ndescription: About " + id + ".\nstart: hall\n",
		"rooms/hall.yaml": "id: hall\ntitle: Hall\ndescription: A hall.\n",
	}
	for f, body := range files {
		if err := os.WriteFile(filepath.Join(p, f), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// suffix is how a test pack's title names a version other than 1.0.0.
func suffix(version string) string {
	if version == "1.0.0" {
		return ""
	}
	return " " + version
}

func ids(ps packs) string {
	var out []string
	for _, p := range ps.all {
		out = append(out, p.ID)
	}
	return strings.Join(out, " ")
}

// The packs a server has: the main one first, then the others by id; the
// demo always, unless a folder replaces it.
func TestLoadPacks(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, "zeta", "zeta")
	writePack(t, dir, "alpha", "alpha")
	main := writePack(t, dir, "main", "main")
	if err := os.Mkdir(filepath.Join(dir, "not-a-pack"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("packs"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePack(t, dir, ".hidden", "hidden")

	for _, tt := range []struct {
		name, main, all string
		env             map[string]string
	}{
		{"demo only", "demo", "demo", nil},
		{"main pack", "main", "main demo", map[string]string{"TALESPEAK_PACK": main}},
		{"packs folder", "demo", "demo alpha main zeta", map[string]string{"TALESPEAK_PACKS": dir}},
		{"main pack in the folder too", "main", "main alpha demo zeta", map[string]string{"TALESPEAK_PACK": main, "TALESPEAK_PACKS": dir}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ps, err := loadPacks(env(tt.env), pack.DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if ps.mainID != tt.main || ps.main().ID != tt.main || ids(ps) != tt.all {
				t.Errorf("main %s, all %q; want %s, %q", ps.mainID, ids(ps), tt.main, tt.all)
			}
			if l := ps.lobby(); len(l) != len(ps.all) || l[0].ID != tt.main || l[0].Title == "" || l[0].Version == "" {
				t.Errorf("lobby packs = %+v", l)
			}
		})
	}

	// A folder with the demo's id replaces the built-in demo.
	own := t.TempDir()
	writePack(t, own, "mydemo", "demo")
	ps, err := loadPacks(env(map[string]string{"TALESPEAK_PACKS": own}), pack.DefaultLimits)
	if err != nil || ids(ps) != "demo" || ps.main().Title != "Pack demo" {
		t.Errorf("own demo: %q, %v", ids(ps), err)
	}
}

func TestLoadPacksRefuses(t *testing.T) {
	dup := t.TempDir()
	writePack(t, dup, "one", "same")
	writePack(t, dup, "two", "same")
	broken := t.TempDir()
	writePack(t, broken, "good", "good")
	if err := os.MkdirAll(filepath.Join(broken, "bad"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "bad", "pack.yaml"), []byte("id: BAD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"two packs, one id and version", map[string]string{"TALESPEAK_PACKS": dup}, `two packs are same 1.0.0`},
		{"a broken pack", map[string]string{"TALESPEAK_PACKS": broken}, "pack " + filepath.Join(broken, "bad")},
		{"no such folder", map[string]string{"TALESPEAK_PACKS": filepath.Join(dup, "nope")}, "TALESPEAK_PACKS:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := loadPacks(env(tt.env), pack.DefaultLimits); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// More edges of the pack catalog: too many folders, a broken main pack,
// TALESPEAK_PACKS naming a file, and a folder with the demo's id beside
// another main pack.
func TestLoadPacksEdges(t *testing.T) {
	many := t.TempDir()
	for i := range maxPacks + 1 {
		writePack(t, many, fmt.Sprintf("p%03d", i), fmt.Sprintf("p%03d", i))
	}
	if _, err := loadPacks(env(map[string]string{"TALESPEAK_PACKS": many}), pack.DefaultLimits); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("%d packs: %v", maxPacks+1, err)
	}
	if err := os.RemoveAll(filepath.Join(many, "p000")); err != nil {
		t.Fatal(err)
	}
	if ps, err := loadPacks(env(map[string]string{"TALESPEAK_PACKS": many}), pack.DefaultLimits); err != nil || len(ps.all) != maxPacks+1 {
		t.Errorf("exactly %d packs and the demo: %d, %v", maxPacks, len(ps.all), err)
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "pack.yaml"), []byte("id: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPacks(env(map[string]string{"TALESPEAK_PACK": broken}), pack.DefaultLimits); err == nil || !strings.Contains(err.Error(), "pack "+broken) {
		t.Errorf("broken main pack: %v", err)
	}
	if _, err := loadPacks(env(map[string]string{"TALESPEAK_PACK": filepath.Join(broken, "nope")}), pack.DefaultLimits); err == nil {
		t.Error("a main pack folder that does not exist loaded")
	}
	if _, err := loadPacks(env(map[string]string{"TALESPEAK_PACKS": filepath.Join(broken, "pack.yaml")}), pack.DefaultLimits); err == nil || !strings.Contains(err.Error(), "TALESPEAK_PACKS") {
		t.Errorf("TALESPEAK_PACKS naming a file: %v", err)
	}

	dir := t.TempDir()
	main := writePack(t, t.TempDir(), "main", "main")
	writePack(t, dir, "mydemo", "demo")
	ps, err := loadPacks(env(map[string]string{"TALESPEAK_PACK": main, "TALESPEAK_PACKS": dir}), pack.DefaultLimits)
	if err != nil || ids(ps) != "main demo" || ps.cat.Newest("demo").Title != "Pack demo" {
		t.Errorf("own demo beside another main pack: %q, %v", ids(ps), err)
	}
	// A folder with the main pack's id but elsewhere: the main one wins.
	writePack(t, dir, "main-copy", "main")
	ps, err = loadPacks(env(map[string]string{"TALESPEAK_PACK": main, "TALESPEAK_PACKS": dir}), pack.DefaultLimits)
	if err != nil || ids(ps) != "main demo" {
		t.Errorf("main id twice: %q, %v", ids(ps), err)
	}
}

// The main world's visibility follows TALESPEAK_WORLD_ACCESS.
func TestAccessVisibility(t *testing.T) {
	for _, tt := range []struct{ world, want string }{
		{account.AccessOpen, store.VisibilityPublic},
		{account.AccessInvite, store.VisibilityPrivate},
		{"", store.VisibilityPrivate},
	} {
		if got := (access{world: tt.world}).visibility(); got != tt.want {
			t.Errorf("visibility(%q) = %q, want %q", tt.world, got, tt.want)
		}
	}
}

// Several versions of one pack are all installed; new worlds start on the
// newest, and the main world moves to a new main world on a new major
// version, while the old one keeps running on its version.
func TestLoadPacksVersions(t *testing.T) {
	dir := t.TempDir()
	writePackVersion(t, dir, "story-1", "story", "1.0.0")
	writePackVersion(t, dir, "story-1.1", "story", "1.1.0")
	main1 := writePackVersion(t, t.TempDir(), "main", "main", "1.0.0")
	ps, err := loadPacks(env(map[string]string{"TALESPEAK_PACK": main1, "TALESPEAK_PACKS": dir}), pack.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if ids(ps) != "main demo story story" || ps.main().Version != "1.0.0" {
		t.Errorf("packs %q, main %s", ids(ps), ps.main().Name())
	}
	if l := ps.lobby(); len(l) != 3 || l[2].ID != "story" || l[2].Version != "1.1.0" || l[2].Title != "Pack story 1.1.0" {
		t.Errorf("lobby = %+v", l)
	}

	ctx := t.Context()
	db, err := openData(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	home, err := openHome(ctx, db, ps)
	if err != nil || home.PackVersion != "1.0.0" {
		t.Fatalf("main world = %+v, %v", home, err)
	}

	// 1.2.0 in the folder: the same main world runs it.
	writePackVersion(t, dir, "main-1.2", "main", "1.2.0")
	ps, err = loadPacks(env(map[string]string{"TALESPEAK_PACK": main1, "TALESPEAK_PACKS": dir}), pack.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := openHome(ctx, db, ps); err != nil || again.ID != home.ID || ps.main().Version != "1.2.0" {
		t.Errorf("after a minor update: %+v (main %s), %v", again, ps.main().Name(), err)
	}

	// 2.0.0 as TALESPEAK_PACK, 1.x still in the folder: a new main world,
	// and the old one runs on 1.2.0.
	main2 := writePackVersion(t, t.TempDir(), "main", "main", "2.0.0")
	ps, err = loadPacks(env(map[string]string{"TALESPEAK_PACK": main2, "TALESPEAK_PACKS": dir}), pack.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	next, err := openHome(ctx, db, ps)
	if err != nil || next.ID == home.ID || next.PackVersion != "2.0.0" {
		t.Errorf("after a major update: %+v, %v", next, err)
	}
	if p, err := ps.cat.Pick("main", home.PackVersion); err != nil || p.Version != "1.2.0" {
		t.Errorf("the old main world runs %v, %v", p, err)
	}
	if ps.keepHome("1.2.0") || !ps.keepHome("2.0.0") || !ps.keepHome("") || ps.keepHome("3.0.0") {
		t.Error("keepHome keeps the wrong main worlds")
	}

	// The main pack in the folder too, same version: one copy.
	writePackVersion(t, dir, "main-2", "main", "2.0.0")
	if ps, err = loadPacks(env(map[string]string{"TALESPEAK_PACK": main2, "TALESPEAK_PACKS": dir}), pack.DefaultLimits); err != nil || ids(ps) != "main main demo story story" {
		t.Errorf("main pack twice: %q, %v", ids(ps), err)
	}
	if got := len(ps.installed()); got != 5 || ps.installed()[0].Hash == "" {
		t.Errorf("installed = %+v", ps.installed())
	}
}
