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
	out, err := exec.Command("sh", filepath.Join(root, "scripts", "check-codemap.sh")).CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	return string(out), err == nil
}

func TestCheckCodemap(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: runs scripts/check-codemap.sh")
	}
	for _, tool := range []string{"sh", "find", "grep", "awk", "sed", "wc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, ok := runCheckCodemap(t, tt.files)
			switch {
			case tt.fail == "" && !ok:
				t.Errorf("check failed:\n%s", out)
			case tt.fail != "" && ok:
				t.Errorf("check passed, want %q:\n%s", tt.fail, out)
			case tt.fail != "" && !strings.Contains(out, tt.fail):
				t.Errorf("output lacks %q:\n%s", tt.fail, out)
			}
		})
	}
}
