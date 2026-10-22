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

// writePack writes a one-room pack with id into dir/name.
func writePack(t *testing.T, dir, name, id string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(p, "rooms"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"pack.yaml":       "id: " + id + "\ntitle: Pack " + id + "\ndescription: About " + id + ".\nstart: hall\n",
		"rooms/hall.yaml": "id: hall\ntitle: Hall\ndescription: A hall.\n",
	}
	for f, body := range files {
		if err := os.WriteFile(filepath.Join(p, f), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
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
			if ps.main.ID != tt.main || ids(ps) != tt.all {
				t.Errorf("main %s, all %q; want %s, %q", ps.main.ID, ids(ps), tt.main, tt.all)
			}
			if l := ps.lobby(); len(l) != len(ps.all) || l[0].ID != tt.main || l[0].Title == "" {
				t.Errorf("lobby packs = %+v", l)
			}
			if m := ps.byID(); len(m) != len(ps.all) {
				t.Errorf("byID = %d packs", len(m))
			}
		})
	}

	// A folder with the demo's id replaces the built-in demo.
	own := t.TempDir()
	writePack(t, own, "mydemo", "demo")
	ps, err := loadPacks(env(map[string]string{"TALESPEAK_PACKS": own}), pack.DefaultLimits)
	if err != nil || ids(ps) != "demo" || ps.main.Title != "Pack demo" {
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
		{"two packs, one id", map[string]string{"TALESPEAK_PACKS": dup}, `two packs have the id "same"`},
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
	if err != nil || ids(ps) != "main demo" || ps.byID()["demo"].Title != "Pack demo" {
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
