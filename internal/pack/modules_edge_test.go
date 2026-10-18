package pack

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"
)

// Edge files in lib/ (T-0026): hidden files and folders, names at and past
// the limits, folders, links, and the module count.

func TestLoadModulesEdgeFiles(t *testing.T) {
	ok := func(files map[string]*fstest.MapFile) fstest.MapFS {
		fsys := fstest.MapFS{
			"pack.yaml":    {Data: []byte(goodPack)},
			"rooms/r.yaml": {Data: []byte(goodRoom)},
		}
		for k, v := range files {
			fsys[k] = v
		}
		return fsys
	}
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	long := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		files   map[string]*fstest.MapFile
		modules []string // the modules loaded; nil with want
		want    []string // what the error holds; nil when it loads
	}{
		{"empty lib", map[string]*fstest.MapFile{"lib": {Mode: fs.ModeDir}}, []string{}, nil},
		{"hidden folder", map[string]*fstest.MapFile{"lib/.git/HEAD": file("ref"), "lib/.git/x.lua": file("x")}, []string{}, nil},
		{"hidden file", map[string]*fstest.MapFile{"lib/.m.lua.swp": file("x"), "lib/.lua": file("x")}, []string{}, nil},
		{"name of 64", map[string]*fstest.MapFile{"lib/" + long + ".lua": file("return 1")}, []string{long}, nil},
		{"digits", map[string]*fstest.MapFile{"lib/7.lua": file("")}, []string{"7"}, nil},
		{"empty module", map[string]*fstest.MapFile{"lib/e.lua": file("")}, []string{"e"}, nil},
		{"name of 65", map[string]*fstest.MapFile{"lib/" + long + "a.lua": file("")}, nil, []string{"lib/" + long + "a.lua", "<name>.lua"}},
		{"leading dash", map[string]*fstest.MapFile{"lib/-m.lua": file("")}, nil, []string{"lib/-m.lua"}},
		{"leading underscore", map[string]*fstest.MapFile{"lib/_m.lua": file("")}, nil, []string{"lib/_m.lua"}},
		{"double extension", map[string]*fstest.MapFile{"lib/m.lua.lua": file("")}, nil, []string{"lib/m.lua.lua"}},
		{"upper extension", map[string]*fstest.MapFile{"lib/m.LUA": file("")}, nil, []string{"lib/m.LUA"}},
		{"space", map[string]*fstest.MapFile{"lib/a b.lua": file("")}, nil, []string{"lib/a b.lua"}},
		{"non-ASCII", map[string]*fstest.MapFile{"lib/é.lua": file("")}, nil, []string{"lib/é.lua"}},
		{"no extension", map[string]*fstest.MapFile{"lib/m": file("")}, nil, []string{"lib/m:"}},
		{"folder named like a module", map[string]*fstest.MapFile{"lib/m.lua/x.lua": file("")}, nil, []string{"lib/m.lua:"}},
		{"lib is a file", map[string]*fstest.MapFile{"lib": file("x = 1")}, nil, []string{"lib:"}},
		{"web file", map[string]*fstest.MapFile{"lib/m.js": file("")}, nil, []string{"lib/m.js: lib holds only", "lib/m.js: .js files"}},
		{"lint line", map[string]*fstest.MapFile{"lib/m.lua": file("-- https://example.com in a comment\nlocal ok = 1\nx = \"see //www.example.com\"")}, nil, []string{"lib/m.lua:3"}},
		{"too large and counted out", map[string]*fstest.MapFile{"lib/big.lua": file(strings.Repeat("-", MaxScriptBytes+1)), "lib/m.lua": file("")}, nil, []string{"lib/big.lua", "larger than"}},
		// BUG: followed, so lib/m.lua holds pack.yaml; see TestLoadModuleSymlinkOnDisk.
		{"symlink", map[string]*fstest.MapFile{"lib/m.lua": {Data: []byte("../pack.yaml"), Mode: fs.ModeSymlink}}, nil, []string{"lib/m.lua"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Load(ok(tt.files))
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Load = %v", err)
				}
				if len(p.Modules) != len(tt.modules) {
					t.Errorf("Modules = %q, want %q", keys(p.Modules), tt.modules)
				}
				for _, m := range tt.modules {
					if _, ok := p.Modules[m]; !ok {
						t.Errorf("Modules = %q, want %q", keys(p.Modules), tt.modules)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("Load = nil error, Modules = %q", keys(p.Modules))
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Hidden files and misnamed files do not count toward MaxModules: a pack
// with exactly MaxModules modules loads with a .git folder beside them,
// and a misnamed file is reported by its own error, not as one too many.
func TestLoadModulesCount(t *testing.T) {
	files := map[string]*string{"lib/.hidden.lua": s(""), "lib/.git/a.lua": s("")}
	for i := range MaxModules {
		files["lib/m"+strconv.Itoa(i)+".lua"] = s("return " + strconv.Itoa(i))
	}
	p, err := Load(fsWith(files))
	if err != nil || len(p.Modules) != MaxModules || p.Modules["m42"] != "return 42" {
		t.Fatalf("MaxModules modules: err = %v", err)
	}
	files["lib/Bad.lua"] = s("")
	_, err = Load(fsWith(files))
	if err == nil || !strings.Contains(err.Error(), "lib/Bad.lua") || strings.Contains(err.Error(), "more than") {
		t.Errorf("misnamed file beside MaxModules modules: %v", err)
	}
}

// A real symlink on disk in lib/ is refused, as in assets/: a module link
// to a file outside the pack would load code from anywhere the server can
// read.
//
// BUG: loadModules (pack.go:227) only refuses folders, so a link to a
// file outside the pack loads its content as a module.
func TestLoadModuleSymlinkOnDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	for name, data := range map[string]string{"pack.yaml": goodPack, "rooms/r.yaml": goodRoom, "lib/real.lua": "return 1"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.lua")
	if err := os.WriteFile(outside, []byte(`return "from outside the pack"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "lib", "out.lua")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if err := os.Symlink("real.lua", filepath.Join(dir, "lib", "inside.lua")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "lib", "folder.lua")); err != nil {
		t.Fatal(err)
	}
	p, err := Load(os.DirFS(dir))
	if err == nil {
		t.Fatalf("Load with symlinks in lib/ = nil error, Modules = %q", p.Modules)
	}
	for _, want := range []string{"lib/out.lua", "lib/inside.lua", "lib/folder.lua"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "lib/real.lua") {
		t.Errorf("the regular module was refused: %v", err)
	}
}

// FuzzLoadModule loads a pack with one fuzzed file in lib/: Load never
// panics, and a pack that loads holds only modules with valid names and
// sizes, with their source unchanged.
func FuzzLoadModule(f *testing.F) {
	for _, seed := range []struct{ name, src string }{
		{"m.lua", "return {}"},
		{".hidden.lua", "x"},
		{"M.lua", ""},
		{"a.lua.lua", ""},
		{"m.js", ""},
		{"m.lua", `x = "https://example.com"`},
		{"sub/m.lua", "return 1"},
		{"m.lua", "--[[ unterminated"},
	} {
		f.Add(seed.name, seed.src)
	}
	f.Fuzz(func(t *testing.T, name, src string) {
		if !fs.ValidPath("lib/"+name) || len(src) > 2*MaxScriptBytes {
			return
		}
		fsys := fsWith(map[string]*string{"lib/" + name: &src})
		p, err := Load(fsys)
		if err != nil {
			if p != nil {
				t.Fatalf("Load returned a pack with %v", err)
			}
			return
		}
		if len(p.Modules) > 1 {
			t.Fatalf("Modules = %q from one file", keys(p.Modules))
		}
		for m, got := range p.Modules {
			if !idPattern.MatchString(m) || "lib/"+m+".lua" != "lib/"+name || got != src || len(got) > MaxScriptBytes || !utf8.ValidString(m) {
				t.Fatalf("module %q from lib/%s = %d bytes", m, name, len(got))
			}
		}
	})
}
