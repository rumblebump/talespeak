package pack

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/content"
)

// errLines returns the line of each "file:line: external reference" error,
// in order, and fails if one names another file.
func errLines(t *testing.T, file string, errs []error) []int {
	t.Helper()
	var lines []int
	for _, err := range errs {
		rest, ok := strings.CutPrefix(err.Error(), file+":")
		n, _, ok2 := strings.Cut(rest, ":")
		line, err2 := strconv.Atoi(n)
		if !ok || !ok2 || err2 != nil || (!strings.Contains(err.Error(), "external reference") && !strings.Contains(err.Error(), "YAML tag")) {
			t.Fatalf("error %q is not %s:<line>: external reference or YAML tag", err, file)
		}
		lines = append(lines, line)
	}
	return lines
}

// Text a story author writes every day must not trip the lint.
func TestExternalRefStoryText(t *testing.T) {
	plain := []string{
		"The guard snores. A key glints on his belt.",
		"Half past ten: 10:30 // the bell rings",
		"7 // 2 is 3, and 7 / 2 is 3.5",
		"and/or, either/or, his/her",
		"path/to//file",
		"A//B, x//y, 3//4",
		"I/O and TCP/IP are words here, not links",
		"C:\\Games\\talespeak",
		"HTTP is how browsers talk; https is the safe one",
		"The www was young then.",
		"www. is not a host",
		"WWW.TXT",
		"mailto:someone",
		"tel:+49 30 1234",
		"data:image/png;base64,AAAA",
		"javascript is a language",
		"Ratio 16:9, score 3:1",
		"e.g. this, i.e. that",
		"a :// b",
		"//",
		"// ",
		"--//--",
		"<//>",
		"Hmm... / ...",
		"Roses are red, / violets are blue, // sugar is sweet",
		"über//alles",
		"cells/nails.png",
		"",
	}
	for _, s := range plain {
		if loc := externalRef.FindStringIndex(s); loc != nil {
			t.Errorf("%q: found %q", s, s[loc[0]:loc[1]])
		}
	}
}

// Every way a reference can be written in YAML, and the line reported.
func TestLintYAMLForms(t *testing.T) {
	tests := []struct {
		name, doc string
		lines     []int
	}{
		{"plain", "a: https://x.example\n", []int{1}},
		{"single quoted", "a: 'see https://x.example'\n", []int{1}},
		{"double quoted escapes", "a: \"\\x68ttps:\\x2F\\x2Fx.example\"\n", []int{1}},
		{"unicode escape", "a: \"https:\\u002F\\u002Fx.example\"\n", []int{1}},
		{"escaped line break joins", "a: \"https:\\\n  //x.example\"\n", []int{2}},
		{"plain scalar folds to //host", "a: see\n  //x.example\n", []int{2}},
		{"flow mapping", "{a: b, c: //cdn.example}\n", []int{1}},
		{"flow sequence", "a: [ok, \"wss://x.example\"]\n", []int{1}},
		{"key", "https://k.example: v\n", []int{1}},
		{"nested key", "a:\n  b:\n    www.k.example: v\n", []int{3}},
		{"complex key", "? [ftp://k.example]\n: v\n", []int{1}},
		{"anchor counted once", "a: &u https://x.example\nb: *u\nc: *u\n", []int{1}},
		{"merge key", "base: &b {d: https://x.example}\nr:\n  <<: *b\n", []int{1}},
		{"one per scalar", "a: https://x.example https://y.example\n", []int{1}},
		{"several", "a: ok\nb: https://x.example\nc: ok\nd: //y.example\n", []int{2, 4}},
		{"comment", "a: ok # https://x.example\n# www.y.example\n", nil},
		{"document marker", "---\na: ok\n...\n", nil},
		{"empty", "", nil},
		{"only a comment", "# https://x.example\n", nil},
		{"invalid YAML is left to decode", "a: [https://x.example\n", nil},
		{"tab indentation is left to decode", "a:\n\tb: https://x.example\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errLines(t, "f.yaml", lintYAML("f.yaml", []byte(tt.doc)))
			if !slices.Equal(got, tt.lines) {
				t.Errorf("lines = %v, want %v", got, tt.lines)
			}
		})
	}
}

// Every way a string literal can be written in Lua, and the line reported.
func TestLintLuaForms(t *testing.T) {
	tests := []struct {
		name, src string
		lines     []int
	}{
		{"double quoted", `say("https://x.example")`, []int{1}},
		{"single quoted", `say('//cdn.example/a')`, []int{1}},
		{"long string", "say([[www.x.example]])", []int{1}},
		{"long string level 2", "say([==[ a ]] https://x.example ]==])", []int{1}},
		{"long string first newline", "say([[\nhttps://x.example]])", []int{2}},
		{"decimal escapes", `say("\104\116\116\112\115://x.example")`, []int{1}},
		{"escaped slashes", `say("https:\47\47x.example")`, []int{1}},
		{"method on a literal", `say(("https://x.example"):upper())`, []int{1}},
		{"table key", `t = {["https://x.example"] = 1}`, []int{1}},
		{"table value", "t = {\n  a = 1,\n  b = 'wss://x.example',\n}", []int{3}},
		{"call without parentheses", `say "https://x.example"`, []int{1}},
		{"string that looks like a comment", `say("-- https://x.example")`, []int{1}},
		{"line comment", "-- https://x.example\nsay('ok')", nil},
		{"long comment", "--[[ https://x.example\n www.y.example ]]\nsay('ok')", nil},
		{"long comment level 1", "--[=[ ]] https://x.example ]=]", nil},
		{"code after a long comment", "--[[ c ]] say('https://x.example')", []int{1}},
		{"quote inside a comment", "-- it's https://x.example\nsay('ok')", nil},
		{"string after comment line", "-- note\n\nsay('https://x.example')", []int{3}},
		{"LF lines", "a = 1\nb = 2\nsay('https://x.example')", []int{3}},
		{"CRLF lines", "a = 1\r\nb = 2\r\nsay('https://x.example')", []int{3}},
		{"CR lines", "a = 1\rb = 2\rsay('https://x.example')", []int{3}},
		{"blank CRLF lines", "\r\n\r\n\r\nsay('https://x.example')", []int{4}},
		{"after an escaped newline", "say('a\\\nb')\nsay('https://x.example')", []int{3}},
		{"after a multi-line long string", "say([[a\nb\nc]])\nsay('https://x.example')", []int{4}},
		{"several", "say('https://a.example')\nsay('ok')\nsay('https://b.example')", []int{1, 3}},
		{"identifiers are not strings", "https = 1 www = {} local x = https", nil},
		{"concat of halves", `say("https:" .. "//x.example")`, []int{1}},
		{"empty", "", nil},
		{"invalid token stops", "say('https://a.example') ~ say('https://b.example')", []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errLines(t, "s.lua", lintLua("s.lua", tt.src))
			if !slices.Equal(got, tt.lines) {
				t.Errorf("lines = %v, want %v", got, tt.lines)
			}
		})
	}
}

func TestRefErrorSnippetEdges(t *testing.T) {
	tests := []struct {
		name, s, want string
	}{
		{"whole string", "https://x.example", `"https://x.example"`},
		{"at the start", "https://x.example/" + strings.Repeat("a", 100), `"https://x.example/aaaaaaaaaaaaaaaaaaaa"`},
		{"at the end", strings.Repeat("a", 100) + " //x.example", `"aaaaaaaaaa //x.example"`},
		{"cut inside a rune", "ééééééééé https://x.example", `"?éééé https://x.example"`},
		{"control characters quoted", "a\x00\nhttps://x.example", `"a\x00 https://x.example"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := refError("f", 1, tt.s).Error()
			if !strings.Contains(msg, "external reference "+tt.want+":") {
				t.Errorf("message = %q, want snippet %s", msg, tt.want)
			}
		})
	}
}

func TestLintFiles(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string // "" when allowed
	}{
		{"css", "a.css", ".css"},
		{"svg in assets", "assets/map.svg", ".svg"},
		{"html", "rooms/r.html", ".html"},
		{"htm", "x.htm", ".htm"},
		{"xhtml", "x.xhtml", ".xhtml"},
		{"js", "scripts/app.js", ".js"},
		{"mjs", "deep/a/b/c/d/x.mjs", ".mjs"},
		{"wasm", "bin/x.wasm", ".wasm"},
		{"upper case", "X.SVG", ".svg"},
		{"mixed case", "x.Js", ".js"},
		{"name is only the extension", ".css", ".css"},
		{"hidden folder, never read", ".cache/x.js", ""},
		{"backup", "style.css.bak", ""},
		{"no extension", "css", ""},
		{"readme", "README.md", ""},
		{"license", "LICENSE", ""},
		{"lua", "scripts/x.lua", ""},
		{"png", "assets/a.png", ""},
		{"json", "notes.json", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := lintFiles(fstest.MapFS{tt.file: {Data: []byte("x")}})
			if tt.want == "" {
				if len(errs) != 0 {
					t.Errorf("errors = %v, want none", errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Error() != tt.file+": "+tt.want+" files are not supported in a pack" {
				t.Errorf("errors = %v, want one for %s", errs, tt.file)
			}
		})
	}
}

// A folder named like a web file holds files, it is not one.
func TestLintFilesFolderNamedLikeWebFile(t *testing.T) {
	if errs := lintFiles(fstest.MapFS{"x.css/notes.txt": {Data: []byte("x")}}); len(errs) != 0 {
		t.Errorf("errors = %v", errs)
	}
}

func TestLintFilesErrorsAreBounded(t *testing.T) {
	fsys := fstest.MapFS{}
	for i := range 3 * maxLintErrors {
		fsys["web/"+strconv.Itoa(i)+".js"] = &fstest.MapFile{Data: []byte("x")}
	}
	if got := lintFiles(fsys); len(got) != maxLintErrors {
		t.Errorf("%d errors, want %d", len(got), maxLintErrors)
	}
}

// The walk counts every entry, folders and the root included, and stops
// only past maxPackFiles.
func TestLintFilesBoundIsInclusive(t *testing.T) {
	build := func(entries int) fstest.MapFS {
		fsys := fstest.MapFS{}
		for i := range entries - 2 { // the root "." and "notes"
			fsys["notes/"+strconv.Itoa(i)] = &fstest.MapFile{Data: []byte("x")}
		}
		return fsys
	}
	if errs := lintFiles(build(maxPackFiles)); len(errs) != 0 {
		t.Errorf("%d entries: %v", maxPackFiles, errs)
	}
	errs := lintFiles(build(maxPackFiles + 1))
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "pack files: more than 5000 files") {
		t.Errorf("%d entries: %v", maxPackFiles+1, errs)
	}
}

// On disk a symlink is reported by its own name and never followed, so a
// link to a folder cannot loop or leave the pack.
func TestLintFilesSymlinksOnDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "evil.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		"style.css": filepath.Join(outside, "evil.js"),
		"loop":      dir,
		"out":       outside,
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	errs := lintFiles(os.DirFS(dir))
	if len(errs) != 1 || !strings.HasPrefix(errs[0].Error(), "style.css: .css") {
		t.Errorf("errors = %v, want only style.css", errs)
	}
}

func TestLintFilesUnreadableFolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions and a non-root user")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	errs := lintFiles(os.DirFS(dir))
	if len(errs) != 1 || !strings.HasPrefix(errs[0].Error(), "pack files: ") {
		t.Errorf("errors = %v, want the walk error", errs)
	}
}

// Lint problems are joined with every other problem of the pack, each
// with its file, and they do not hide each other.
func TestLintJoinsOtherErrors(t *testing.T) {
	_, err := Load(fsWith(map[string]*string{
		"pack.yaml":     s("id: t\ntitle: \"\"\nstart: r\n"),
		"rooms/r.yaml":  s("id: r\ntitle: R\ndescription: https://a.example\nscript: r.lua\n"),
		"rooms/z.yaml":  s("id: r\ntitle: Z\ndescription: //b.example\n"),
		"scripts/r.lua": s("say('www.c.example')"),
		"assets/x.css":  s("a{}"),
		"assets/a.txt":  s("x"),
	}))
	if err == nil {
		t.Fatal("Load = nil error")
	}
	msg := err.Error()
	for _, want := range []string{
		"pack.yaml: title",
		"rooms/r.yaml:3: external reference",
		"rooms/z.yaml:3: external reference", // linted even though its id is taken
		"rooms/z.yaml: id \"r\" is already used",
		"scripts/r.lua:1: external reference",
		"assets/x.css: .css files",
		"assets/a.txt",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
}

// A pack.yaml that does not decode stops Load before anything else is
// checked, lint included.
func TestLintNotReachedWhenPackFileBroken(t *testing.T) {
	_, err := Load(fsWith(map[string]*string{"pack.yaml": s("id: [\n"), "x.css": s("a{}")}))
	if err == nil || strings.Contains(err.Error(), "x.css") || !strings.HasPrefix(err.Error(), "pack.yaml: ") {
		t.Errorf("err = %v, want only the pack.yaml error", err)
	}
}

// Only scripts a room names are read, so only they are linted; the engine
// never loads the others.
func TestLintSkipsUnusedScripts(t *testing.T) {
	_, err := Load(fsWith(map[string]*string{"scripts/unused.lua": s("say('https://x.example')")}))
	if err != nil {
		t.Errorf("Load = %v", err)
	}
}

// A room whose YAML does not decode gets one error, not lint noise too.
func TestLintNotRunOnBrokenRoom(t *testing.T) {
	_, err := Load(fsWith(map[string]*string{"rooms/r.yaml": s("id: r\ntitle: https://x.example\nbogus: 1\n")}))
	if err == nil || strings.Contains(err.Error(), "external reference") {
		t.Errorf("err = %v, want only the decode error", err)
	}
}

// The packs that ship with the engine are clean: every YAML file and
// every script, used or not, and no web files.
func TestShippedPacksPassLint(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"demo":      content.Demo(),
		"test-pack": os.DirFS(filepath.Join("..", "..", "content", "test-pack")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(fsys); err != nil {
				t.Fatal(err)
			}
			for _, e := range lintFiles(fsys) {
				t.Error(e)
			}
			n := 0
			err := fs.WalkDir(fsys, ".", func(file string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				data, err := fs.ReadFile(fsys, file)
				if err != nil {
					return err
				}
				switch path := file; {
				case strings.HasSuffix(path, ".yaml"):
					n++
					for _, e := range lintYAML(file, data) {
						t.Error(e)
					}
				case strings.HasSuffix(path, ".lua"):
					n++
					for _, e := range lintLua(file, string(data)) {
						t.Error(e)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if n < 3 {
				t.Errorf("linted %d files, want pack.yaml, a room and a script at least", n)
			}
		})
	}
}

// FuzzLint feeds outside text to both linters directly: no panic, at most
// maxLintErrors errors, each naming the file and a line inside it.
func FuzzLint(f *testing.F) {
	for _, s := range []string{
		"", "a: https://x.example\n", "say('https://x.example')", "say([==[\n//x.example]==])",
		"--[[ https://x ]] say(\"\\104ttps://x\")", "a: !!binary aGk=\n", "? [ftp://k]\n: v\n",
		"x=1\r\n\r\nsay('www.x.example')", "say('\xff\xfe https://x')", "a: \"\\xff https://x\"\n",
		strings.Repeat("say('//a.example')\n", 30), "[[", "'", "\"\\", "--[[",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		lines := strings.Count(src, "\n") + strings.Count(src, "\r") + 1
		for file, errs := range map[string][]error{
			"f.yaml": lintYAML("f.yaml", []byte(src)),
			"s.lua":  lintLua("s.lua", src),
		} {
			if len(errs) > maxLintErrors {
				t.Fatalf("%s: %d errors", file, len(errs))
			}
			for _, line := range errLines(t, file, errs) {
				if line < 1 || line > lines {
					t.Fatalf("%s: line %d of %d", file, line, lines)
				}
			}
		}
	})
}
