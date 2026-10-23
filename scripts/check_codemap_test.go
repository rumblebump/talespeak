package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The clean tree the cases below change one file of: every source file
// is in the map, and the one long file is allowed.
var cleanTree = map[string]string{
	"CODING.md": "# Map\n\n| `cmd/x/main.go` | main |\n| `internal/a/a.go` | a |\n| `web/static/app.js` | app |\n" +
		"| `content/p/scripts/` | rooms |\n| `content/p/lib/lib.lua` | lib |\n| `scripts/check-codemap.sh` | this |\n" +
		"| `internal/a/long.go` | long |\n\nImports such as `log/slog` are no paths.\n",
	"cmd/x/main.go":                "package main\n",
	"internal/a/a.go":              "package a\n",
	"internal/a/a_test.go":         "package a\n",
	"internal/a/long.go":           lines(700),
	"internal/a/testdata/x/s.lua":  "-- test data\n",
	"web/static/app.js":            "export const x = 1;\n",
	"web/static/vendor/alpine.js":  "// vendored\n",
	"content/p/scripts/hall.lua":   "-- a room\n",
	"content/p/scripts/cellar.lua": "-- a room\n",
	"content/p/lib/lib.lua":        "return {}\n",
	"e2e/tests/a.spec.js":          "// test\n",
	"e2e/node_modules/x/index.js":  "// dependency\n",
	"scripts/size-allow.txt":       "# allowed\ninternal/a/long.go 700\n",
}

// lines is a Go file of n lines.
func lines(n int) string {
	return "package a\n" + strings.Repeat("\n", n-1)
}

// runCheckCodemap runs a copy of scripts/check-codemap.sh on a temporary
// tree made of cleanTree with the given files replaced; a nil value
// removes a file. It returns the output and whether the check passed.
func runCheckCodemap(t *testing.T, files map[string]*string) (string, bool) {
	t.Helper()
	root := writeTree(t, files)
	return runScript(t, "", "sh", filepath.Join(root, "scripts", "check-codemap.sh"))
}

// writeTree writes cleanTree with the given files replaced (nil removes
// one) and a copy of the script into a new temporary directory, its root.
func writeTree(t *testing.T, files map[string]*string) string {
	t.Helper()
	root := t.TempDir()
	script, err := os.ReadFile("check-codemap.sh")
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]*string{}
	for name, data := range cleanTree {
		all[name] = &data
	}
	s := string(script)
	all["scripts/check-codemap.sh"] = &s
	for name, data := range files {
		all[name] = data
	}
	for name, data := range all {
		if data == nil {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(*data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runScript runs name with args in dir ("" for the test's own directory)
// and returns the output and whether it exited 0.
func runScript(t *testing.T, dir, name string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	return string(out), err == nil
}

// needTools skips the test when a tool the script uses is missing.
func needTools(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: runs scripts/check-codemap.sh")
	}
	for _, tool := range []string{"sh", "find", "grep", "awk", "sed", "wc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
}

// checkResult reports whether out and ok match fail, the message expected
// ("" when the check should pass).
func checkResult(t *testing.T, out string, ok bool, fail string) {
	t.Helper()
	switch {
	case fail == "" && !ok:
		t.Errorf("check failed:\n%s", out)
	case fail != "" && ok:
		t.Errorf("check passed, want %q:\n%s", fail, out)
	case fail != "" && !strings.Contains(out, fail):
		t.Errorf("output lacks %q:\n%s", fail, out)
	}
}

func TestCheckCodemap(t *testing.T) {
	needTools(t)
	str := func(s string) *string { return &s }
	mapWith := func(extra string) map[string]*string {
		return map[string]*string{"CODING.md": str(cleanTree["CODING.md"] + extra)}
	}
	tests := []struct {
		name  string
		files map[string]*string
		fail  string // the message expected, "" when the check passes
	}{
		{"clean", nil, ""},
		{"new source file", map[string]*string{"internal/a/b.go": str("package a\n")}, "internal/a/b.go: not in CODING.md"},
		{"new Lua lib", map[string]*string{"content/p/lib/more.lua": str("return 1\n")}, "content/p/lib/more.lua: not in CODING.md"},
		{"new pack's rooms", map[string]*string{"content/q/scripts/a.lua": str("-- a\n")}, "content/q/scripts/: not in CODING.md"},
		{"new shell script", map[string]*string{"scripts/x.sh": str("#!/bin/sh\n")}, "scripts/x.sh: not in CODING.md"},
		{"room script in a mapped folder", map[string]*string{"content/p/scripts/attic.lua": str("-- a\n")}, ""},
		{"new test file", map[string]*string{"internal/a/b_test.go": str("package a\n")}, ""},
		{"a name is not a path", mapWith("Mentions `a.go` alone.\n"), ""},
		{"dead path", mapWith("| `internal/gone/gone.go` | gone |\n"), "internal/gone/gone.go: named in CODING.md but does not exist"},
		{"dead docs link", mapWith("See `docs/nothing.md`.\n"), "docs/nothing.md: named in CODING.md but does not exist"},
		{"map missing", map[string]*string{"CODING.md": nil}, "CODING.md is missing"},
		{"source at the limit", map[string]*string{"internal/a/a.go": str(lines(600))}, ""},
		{"source too long", map[string]*string{"internal/a/a.go": str(lines(601))}, "internal/a/a.go: 601 lines, at most 600"},
		{"Lua too long", map[string]*string{"content/p/lib/lib.lua": str(strings.Repeat("--\n", 601))}, "content/p/lib/lib.lua: 601 lines"},
		{"test at its limit", map[string]*string{"internal/a/a_test.go": str(lines(800))}, ""},
		{"test too long", map[string]*string{"internal/a/a_test.go": str(lines(801))}, "internal/a/a_test.go: 801 lines, at most 800"},
		{"e2e test too long", map[string]*string{"e2e/tests/a.spec.js": str(strings.Repeat("//\n", 801))}, "e2e/tests/a.spec.js: 801 lines, at most 800"},
		{"allowed file grew", map[string]*string{"internal/a/long.go": str(lines(701))}, "allows 700"},
		{"allowed file split", map[string]*string{"internal/a/long.go": str(lines(400))}, "remove its line from scripts/size-allow.txt"},
		{"allowed file gone", map[string]*string{"internal/a/long.go": nil, "CODING.md": str(strings.Replace(cleanTree["CODING.md"], "| `internal/a/long.go` | long |\n", "", 1))}, "internal/a/long.go: in scripts/size-allow.txt but does not exist"},
		{"vendored and node_modules skipped", map[string]*string{"web/static/vendor/big.js": str(strings.Repeat("//\n", 900)), "e2e/node_modules/y/a.js": str(strings.Repeat("//\n", 900))}, ""},

		// Names in CODING.md.
		{"date pattern is no dead path", mapWith("Log it as `protocol/YYYY-MM-DD-full-review.md`.\n"), ""},
		{"task id pattern is no dead path", mapWith("A task is `tasks/open/T-NNNN-slug.md`.\n"), ""},
		{"pattern next to a dead path", mapWith("`protocol/YYYY-MM-DD-x.md` and `protocol/gone.md`\n"), "protocol/gone.md: named in CODING.md but does not exist"},
		{"punctuation after the backticks", map[string]*string{"internal/a/b.go": str("package a\n"), "CODING.md": str(cleanTree["CODING.md"] + "See (`internal/a/b.go`), then `internal/a/a.go`.\n")}, ""},
		{"punctuation inside the backticks", mapWith("See `docs/testing.md.`\n"), "docs/testing.md.: named in CODING.md but does not exist"},
		{"line number is no mapping", map[string]*string{"internal/a/b.go": str("package a\n"), "CODING.md": str(cleanTree["CODING.md"] + "See `internal/a/b.go:12`.\n")}, "internal/a/b.go: not in CODING.md"},
		{"name without backticks is no mapping", map[string]*string{"internal/a/b.go": str("package a\n"), "CODING.md": str(cleanTree["CODING.md"] + "See internal/a/b.go.\n")}, "internal/a/b.go: not in CODING.md"},
		{"./ prefix is no mapping", map[string]*string{"internal/a/b.go": str("package a\n"), "CODING.md": str(cleanTree["CODING.md"] + "See `./internal/a/b.go`.\n")}, "internal/a/b.go: not in CODING.md"},
		{"existing directory named", mapWith("The package `internal/a/` and `content/p/`.\n"), ""},
		{"dead directory named", mapWith("The package `internal/gone/`.\n"), "internal/gone/: named in CODING.md but does not exist"},
		{"rooms folder without its slash", map[string]*string{"CODING.md": str(strings.Replace(cleanTree["CODING.md"], "`content/p/scripts/`", "`content/p/scripts`", 1))}, "content/p/scripts/: not in CODING.md"},
		{"nested room scripts", map[string]*string{"content/p/scripts/wing/a.lua": str("-- a\n")}, "content/p/scripts/wing/: not in CODING.md"},
		{"CRLF map", map[string]*string{"CODING.md": str(strings.ReplaceAll(cleanTree["CODING.md"], "\n", "\r\n"))}, ""},
		{"Python script", map[string]*string{"scripts/x.py": str("print(1)\n")}, "scripts/x.py: not in CODING.md"},
		{"hidden folder", map[string]*string{".claude/hooks/x.sh": str("#!/bin/sh\n")}, ".claude/hooks/x.sh: not in CODING.md"},

		// Files of the tree.
		{"e2e shell script needs no line", map[string]*string{"e2e/run.sh": str("#!/bin/sh\n")}, ""},
		{"e2e helper too long", map[string]*string{"e2e/tests/fixtures.js": str(strings.Repeat("//\n", 801))}, "e2e/tests/fixtures.js: 801 lines"},
		{"testdata anywhere skipped", map[string]*string{"testdata/big.go": str(lines(900)), "internal/a/testdata/big.go": str(lines(900))}, ""},
		{"outside git every file is checked", map[string]*string{"bin/gen.go": str("package main\n")}, "bin/gen.go: not in CODING.md"},
		{"file name with a space", map[string]*string{"internal/a/my file.go": str("package a\n")}, "internal/a/my file.go: use only letters, digits and _ . / - in file names"},
		{"space in a non-source name", map[string]*string{"docs/my notes.md": str("x\n")}, "docs/my notes.md: use only letters"},
		{"glob characters in a file name", map[string]*string{"internal/a/[ab].go": str(lines(900))}, "internal/a/[ab].go: use only letters"},
		{"star in a file name", map[string]*string{"internal/a/*.go": str("package a\n")}, "internal/a/*.go: use only letters"},
		{"non-ASCII file name", map[string]*string{"internal/a/caf\u00e9.go": str("package a\n")}, "use only letters"},
		{"newline in a file name", map[string]*string{"internal/a/x\ny.go": str("package a\n")}, "not in CODING.md"},
		{"odd name in node_modules skipped", map[string]*string{"e2e/node_modules/x/a b.js": str("//\n")}, ""},
		{"vendor folder outside web/static is checked", map[string]*string{"internal/vendor/x.go": str("package vendor\n")}, "internal/vendor/x.go: not in CODING.md"},
		{"non-source extensions skipped", map[string]*string{"internal/a/notes.txt": str(strings.Repeat("x\n", 900)), "web/static/app.css": str(strings.Repeat("x\n", 900))}, ""},
		{"JS too long", map[string]*string{"web/static/app.js": str(strings.Repeat("//\n", 601))}, "web/static/app.js: 601 lines, at most 600"},

		// scripts/size-allow.txt.
		{"allow line with a trailing comment", map[string]*string{"scripts/size-allow.txt": str("internal/a/long.go 700 # until the split\n")}, `scripts/size-allow.txt:1: internal/a/long.go 700 # until the split: want "path lines"`},
		{"allow count not a number", map[string]*string{"scripts/size-allow.txt": str("internal/a/long.go 7OO\n")}, `scripts/size-allow.txt:1: internal/a/long.go 7OO: want "path lines"`},
		{"allow count not a number, file grew", map[string]*string{"internal/a/long.go": str(lines(5000)), "scripts/size-allow.txt": str("internal/a/long.go abc\n")}, "internal/a/long.go: 5000 lines, at most 600"},
		{"allow file with CRLF", map[string]*string{"scripts/size-allow.txt": str("internal/a/long.go 650\r\n")}, "internal/a/long.go: 700 lines, at most 600"},
		{"allow line with three fields", map[string]*string{"scripts/size-allow.txt": str("internal/a/long.go 700 800\n")}, `want "path lines"`},
		{"allow line without a count", map[string]*string{"scripts/size-allow.txt": str("internal/a/long.go\n")}, "internal/a/long.go: 700 lines, at most 600"},
		{"allow line indented comment", map[string]*string{"scripts/size-allow.txt": str("  # note\ninternal/a/long.go 700\n")}, "#: in scripts/size-allow.txt but does not exist"},
		{"allow file missing", map[string]*string{"scripts/size-allow.txt": nil}, "internal/a/long.go: 700 lines, at most 600: split it"},
		{"allow file empty", map[string]*string{"scripts/size-allow.txt": str("")}, "internal/a/long.go: 700 lines, at most 600: split it"},
		{"allow path with ./", map[string]*string{"scripts/size-allow.txt": str("./internal/a/long.go 700\n")}, "internal/a/long.go: 700 lines, at most 600: split it"},
		{"allow count under the limit", map[string]*string{"scripts/size-allow.txt": str("internal/a/long.go 700\ninternal/a/a.go 100\n")}, "internal/a/a.go: 1 lines, at most 600: remove its line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, ok := runCheckCodemap(t, tt.files)
			checkResult(t, out, ok, tt.fail)
		})
	}
}

// TestCheckCodemapReviewFixes holds the cases the test engineer found
// broken in the first version of the script.
func TestCheckCodemapReviewFixes(t *testing.T) {
	needTools(t)
	str := func(s string) *string { return &s }
	tests := []struct {
		name  string
		files map[string]*string
		fail  string
	}{
		// Shell and Python scripts are source files, so they have the
		// size limit too.
		{"shell script too long", map[string]*string{
			"scripts/long.sh": str("#!/bin/sh\n" + strings.Repeat("#\n", 700)),
			"CODING.md":       str(cleanTree["CODING.md"] + "| `scripts/long.sh` | long |\n"),
		}, "scripts/long.sh: 701 lines, at most 600"},
		{"last line without a newline counts", map[string]*string{"internal/a/a.go": str(lines(600) + "var x = 1")}, "internal/a/a.go: 601 lines, at most 600"},
		{"stale allow line for a shell script", map[string]*string{"scripts/size-allow.txt": str("internal/a/long.go 700\nscripts/check-codemap.sh 900\n")}, "scripts/check-codemap.sh"},
		{"allow line twice", map[string]*string{"scripts/size-allow.txt": str("internal/a/long.go 650\ninternal/a/long.go 650\n")}, "internal/a/long.go"},
		// Lua pack tests are tests (docs/testing.md): no line in the map,
		// and the test limit.
		{"Lua pack test needs no line", map[string]*string{"content/p/tests/hall_test.lua": str("-- test\n")}, ""},
		{"Lua pack test has the test limit", map[string]*string{"content/p/tests/hall_test.lua": str(strings.Repeat("--\n", 700))}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, ok := runCheckCodemap(t, tt.files)
			checkResult(t, out, ok, tt.fail)
		})
	}
}

func TestCheckCodemapWorkingDir(t *testing.T) {
	needTools(t)
	root := writeTree(t, map[string]*string{"internal/a/b.go": func(s string) *string { return &s }("package a\n")})
	elsewhere := t.TempDir()
	tests := []struct {
		name, dir string
		args      []string
	}{
		{"absolute path from elsewhere", elsewhere, []string{filepath.Join(root, "scripts", "check-codemap.sh")}},
		{"from the root, as CI runs it", root, []string{"./scripts/check-codemap.sh"}},
		{"from scripts/", filepath.Join(root, "scripts"), []string{"check-codemap.sh"}},
		{"from a subfolder", filepath.Join(root, "internal"), []string{"../scripts/check-codemap.sh"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, ok := runScript(t, tt.dir, "sh", tt.args...)
			checkResult(t, out, ok, "internal/a/b.go: not in CODING.md")
		})
	}
	// Executed directly, through its shebang, as the CI job does.
	t.Run("executed directly", func(t *testing.T) {
		out, ok := runScript(t, root, "./scripts/check-codemap.sh")
		checkResult(t, out, ok, "internal/a/b.go: not in CODING.md")
	})
}

func TestCheckCodemapSymlinks(t *testing.T) {
	needTools(t)
	tests := []struct {
		name   string
		link   string // made in the tree, pointing at target
		target string
		extra  string // added to CODING.md
		fail   string
	}{
		// find -type f does not follow links: a link is neither mapped
		// nor measured, and a linked folder is not entered.
		{"link to a source file", "internal/a/link.go", "a.go", "", ""},
		{"link to a long file", "internal/a/biglink.go", "long.go", "", ""},
		{"link to a folder", "internal/b", "a", "", ""},
		{"named link to a file", "internal/a/link.go", "a.go", "`internal/a/link.go`\n", ""},
		{"named dangling link", "internal/a/dead.go", "gone.go", "`internal/a/dead.go`\n", "internal/a/dead.go: named in CODING.md but does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := cleanTree["CODING.md"] + tt.extra
			root := writeTree(t, map[string]*string{"CODING.md": &m})
			if err := os.Symlink(tt.target, filepath.Join(root, filepath.FromSlash(tt.link))); err != nil {
				t.Skipf("no symlinks: %v", err)
			}
			out, ok := runScript(t, "", "sh", filepath.Join(root, "scripts", "check-codemap.sh"))
			checkResult(t, out, ok, tt.fail)
		})
	}
}

// TestCheckCodemapGit runs the check in a git checkout, where it reads
// the files git tracks or would track: ignored build output is left out,
// a new file not yet added is not.
func TestCheckCodemapGit(t *testing.T) {
	needTools(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	str := func(s string) *string { return &s }
	tests := []struct {
		name  string
		files map[string]*string
		fail  string
	}{
		{"clean", nil, ""},
		{"ignored build output", map[string]*string{"bin/gen.go": str("package main\n")}, ""},
		{"new file not added yet", map[string]*string{"internal/a/new.go": str("package a\n")}, "internal/a/new.go: not in CODING.md"},
		{"vendored file in git", map[string]*string{"web/static/vendor/big.js": str(strings.Repeat("//\n", 900))}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]*string{".gitignore": str("/bin/\n")}
			for name, data := range tt.files {
				files[name] = data
			}
			root := writeTree(t, files)
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			git("init", "-q")
			for name := range cleanTree {
				git("add", name)
			}
			git("add", ".gitignore", "scripts/check-codemap.sh")
			if _, ok := tt.files["web/static/vendor/big.js"]; ok {
				git("add", "web/static/vendor/big.js")
			}
			out, ok := runScript(t, "", "sh", filepath.Join(root, "scripts", "check-codemap.sh"))
			checkResult(t, out, ok, tt.fail)
		})
	}
}
