package pack

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// versioned is a loaded pack id at version, with a title that names it.
func versioned(t *testing.T, id, version string) *Pack {
	t.Helper()
	p, err := Load(fsWith(map[string]*string{"pack.yaml": s("id: " + id + "\nversion: " + version + "\ntitle: " + id + " " + version + "\nstart: r\n")}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func names(ps []*Pack) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name())
	}
	return strings.Join(out, ", ")
}

func TestCatalog(t *testing.T) {
	c, err := NewCatalog(versioned(t, "b", "1.0.0"), versioned(t, "a", "1.2.0"), versioned(t, "a", "2.0.0-beta"), versioned(t, "a", "1.10.0"),
		versioned(t, "a", "0.9.0"), versioned(t, "c", "1.0.0-rc.1"), versioned(t, "c", "1.0.0-rc.2"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.IDs(), " "); got != "a b c" {
		t.Errorf("IDs = %q", got)
	}
	// Releases first, newest first; then the pre-releases.
	if got := names(c.Versions("a")); got != "a 1.10.0, a 1.2.0, a 0.9.0, a 2.0.0-beta" {
		t.Errorf("Versions(a) = %s", got)
	}
	if got := names(c.All()); got != "a 1.10.0, a 1.2.0, a 0.9.0, a 2.0.0-beta, b 1.0.0, c 1.0.0-rc.2, c 1.0.0-rc.1" {
		t.Errorf("All = %s", got)
	}
	// New worlds start on the newest release, or a pre-release when there
	// is no release.
	if c.Newest("a").Version != "1.10.0" || c.Newest("c").Version != "1.0.0-rc.2" || c.Newest("nope") != nil {
		t.Errorf("Newest = %v %v %v", c.Newest("a"), c.Newest("c"), c.Newest("nope"))
	}

	for _, tt := range []struct{ id, recorded, want, err string }{
		{"a", "", "1.10.0", ""},                // a world from before versions
		{"a", "1.2.0", "1.10.0", ""},           // updates in a major version reach the world
		{"a", "1.10.0", "1.10.0", ""},          //
		{"a", "1.0.0", "1.10.0", ""},           // an older version that is gone
		{"a", "0.9.0", "0.9.0", ""},            // 0.9 stays on 0.9
		{"a", "2.0.0-alpha", "2.0.0-beta", ""}, // a pre-release moves on to a newer one
		{"a", "1.11.0", "", "pack a needs version ^1.11.0, and this server has 1.10.0, 1.2.0, 0.9.0, 2.0.0-beta"}, // never back
		{"a", "3.0.0", "", "needs version ^3.0.0"},
		{"a", "0.8.0", "", "needs version ^0.8.0"},
		{"a", "garbage", "", "needs version garbage"},
		{"c", "1.0.0-rc.1", "1.0.0-rc.2", ""},
		{"nope", "1.0.0", "", "pack nope: not installed"},
		{"nope", "", "", "not installed"},
	} {
		p, err := c.Pick(tt.id, tt.recorded)
		switch {
		case tt.err == "" && (err != nil || p.Version != tt.want):
			t.Errorf("Pick(%s, %q) = %v, %v; want %s", tt.id, tt.recorded, p, err, tt.want)
		case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
			t.Errorf("Pick(%s, %q) = %v, %v; want error %q", tt.id, tt.recorded, p, err, tt.err)
		}
	}
	var nv *NoVersionError
	if _, err := c.Pick("a", "5.0.0"); !errors.As(err, &nv) || nv.ID != "a" || len(nv.Installed) != 4 {
		t.Errorf("Pick past the versions: %#v", err)
	}
	if _, err := c.Pick("nope", "1.0.0"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Pick of no pack: %v", err)
	}

	// A range a story asks for: a release in it before a pre-release.
	if p, err := c.Match("a", MustRange(">=1.0")); err != nil || p.Version != "1.10.0" {
		t.Errorf("Match(>=1.0) = %v, %v", p, err)
	}
	if p, err := c.Match("a", MustRange("^2.0.0-0")); err != nil || p.Version != "2.0.0-beta" {
		t.Errorf("Match(^2.0.0-0) = %v, %v", p, err)
	}
}

// One version of a pack twice is an error; the same version of two packs
// is not.
func TestCatalogRefusesTwins(t *testing.T) {
	if _, err := NewCatalog(versioned(t, "a", "1.0.0"), versioned(t, "a", "1.0.0")); err == nil || !strings.Contains(err.Error(), "two packs are a 1.0.0") {
		t.Errorf("twins: %v", err)
	}
	if _, err := NewCatalog(versioned(t, "a", "1.0.0"), versioned(t, "b", "1.0.0")); err != nil {
		t.Errorf("two packs at 1.0.0: %v", err)
	}
	c, err := NewCatalog()
	if err != nil || len(c.All()) != 0 || len(c.IDs()) != 0 {
		t.Errorf("empty catalog = %v, %v", c, err)
	}
}

func TestLoadVersion(t *testing.T) {
	for _, tt := range []struct{ yaml, want string }{
		{"id: t\ntitle: T\nstart: r\n", "version is missing"},
		{"id: t\nversion: \"\"\ntitle: T\nstart: r\n", "version is missing"},
		{"id: t\nversion: 1.0\ntitle: T\nstart: r\n", `version "1.0" must be MAJOR.MINOR.PATCH`},
		{"id: t\nversion: v1.0.0\ntitle: T\nstart: r\n", "must be MAJOR.MINOR.PATCH"},
		{"id: t\nversion: 1.0.0+build.5\ntitle: T\nstart: r\n", "must be MAJOR.MINOR.PATCH"},
		{"id: t\nversion: [1]\ntitle: T\nstart: r\n", "pack.yaml"},
	} {
		if _, err := Load(fsWith(map[string]*string{"pack.yaml": &tt.yaml})); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: %v, want %q", tt.yaml, err, tt.want)
		}
	}
	p := versioned(t, "t", "2.1.0-rc.1")
	if p.Version != "2.1.0-rc.1" || p.V != (Version{Major: 2, Minor: 1, Pre: "rc.1"}) || p.Name() != "t 2.1.0-rc.1" {
		t.Errorf("pack = %s %+v", p.Version, p.V)
	}
}

// The hash covers every file the engine reads, and only those.
func TestPackHash(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pack.yaml", goodPack)
	write("rooms/r.yaml", goodRoom+"script: r.lua\n")
	write("scripts/r.lua", "function on_enter() end\n")
	write("lib/m.lua", "return {}\n")
	write("assets/a.png", string(image(".png", 100)))
	hash := func() string {
		t.Helper()
		p, err := Load(os.DirFS(dir))
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Hash) != 64 {
			t.Fatalf("hash %q", p.Hash)
		}
		return p.Hash
	}
	first := hash()
	if hash() != first {
		t.Error("the same files hash differently")
	}
	write("README.md", "notes for authors")
	write("scripts/unused.lua", "x = 1\n")
	if hash() != first {
		t.Error("files the engine does not read changed the hash")
	}
	seen := map[string]string{first: "first"}
	for _, f := range []struct{ name, body string }{
		{"pack.yaml", goodPack + "# a comment\n"},
		{"rooms/r.yaml", goodRoom + "script: r.lua\n# changed\n"},
		{"scripts/r.lua", "function on_enter() say('hi') end\n"},
		{"lib/m.lua", "return {x = 1}\n"},
		{"assets/a.png", string(image(".png", 101))},
	} {
		write(f.name, f.body)
		h := hash()
		if other, ok := seen[h]; ok {
			t.Errorf("after changing %s the hash is the one of %s", f.name, other)
		}
		seen[h] = f.name
	}
	// A file renamed is another pack too.
	if err := os.Rename(filepath.Join(dir, "rooms/r.yaml"), filepath.Join(dir, "rooms/s.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[hash()]; ok {
		t.Error("renaming a room file kept a hash")
	}
}

// The hashing file system still refuses links the way the plain one does.
func TestPackHashKeepsLinksOut(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.lua")
	if err := os.WriteFile(outside, []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"pack.yaml": goodPack, "rooms/r.yaml": goodRoom + "script: r.lua\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "scripts", "r.lua")); err != nil {
		t.Skip(err)
	}
	if _, err := Load(os.DirFS(dir)); err == nil || !strings.Contains(err.Error(), "not a link") {
		t.Errorf("a linked script loaded: %v", err)
	}
}

// Each version of a pack serves its own images.
func TestAssetsPerVersion(t *testing.T) {
	load := func(version string, size int) *Pack {
		p, err := Load(packFS(map[string]*fstest.MapFile{
			"pack.yaml":    file([]byte("id: t\nversion: " + version + "\ntitle: T\nstart: r\n")),
			"assets/a.png": file(image(".png", size)),
		}))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	old, cur := load("1.0.0", 100), load("2.0.0", 200)
	if u, _ := old.Image("a.png"); u != "/packs/t/1.0.0/assets/a.png" {
		t.Errorf("url = %s", u)
	}
	h := AssetHandler(old, cur)
	for _, tt := range []struct {
		path string
		code int
		size int
	}{
		{"/packs/t/1.0.0/assets/a.png", http.StatusOK, 100},
		{"/packs/t/2.0.0/assets/a.png", http.StatusOK, 200},
		{"/packs/t/3.0.0/assets/a.png", http.StatusNotFound, 0},
		{"/packs/t/1.0/assets/a.png", http.StatusNotFound, 0},
		{"/packs/t/assets/a.png", http.StatusNotFound, 0},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
		if rec.Code != tt.code || (tt.code == http.StatusOK && rec.Body.Len() != tt.size) {
			t.Errorf("%s = %d, %d bytes", tt.path, rec.Code, rec.Body.Len())
		}
	}
}
