package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The clean client the cases below change one file of. index.html holds
// the one allowed link, the AGPL source link.
var cleanWeb = map[string]string{
	"web/static/index.html": `<!doctype html>
<html>
<head>
<link rel="stylesheet" href="style.css">
<script type="module" src="app.js"></script>
</head>
<body>
<img :src="picture.url" alt="">
<p x-text="line"></p>
<a href="https://github.com/rumblebump/talespeak">Source</a>
</body>
</html>
`,
	"web/static/app.js":    "// Talespeak client; see https://example.com/docs for the protocol.\nexport const x = 1;\n",
	"web/static/style.css": "body { color: #222; background: url(img/bg.png); }\n",
	"web/web.go":           "package web\n\n//go:embed static\nvar Static embed.FS\n",
}

// runCheckWeb runs a copy of scripts/check-web.sh on a temporary tree made
// of cleanWeb with the given files replaced, and returns its output and
// whether it passed. scripts/vendor.sh is a stub: the checksums have their
// own check.
func runCheckWeb(t *testing.T, files map[string]string) (string, bool) {
	t.Helper()
	root := t.TempDir()
	script, err := os.ReadFile(filepath.Join("check-web.sh"))
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{
		"scripts/check-web.sh": string(script),
		"scripts/vendor.sh":    "#!/bin/sh\nexit 0\n",
	}
	for name, data := range cleanWeb {
		all[name] = data
	}
	for name, data := range files {
		all[name] = data
	}
	for name, data := range all {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", filepath.Join(root, "scripts", "check-web.sh"))
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	return string(out), err == nil
}

func checkWebTools(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: runs scripts/check-web.sh")
	}
	for _, tool := range []string{"sh", "node", "sed", "grep", "find"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
}

func TestCheckWeb(t *testing.T) {
	checkWebTools(t)
	const (
		external = "external URLs in web/"
		sinks    = "HTML sinks in web/"
		bindings = "URL bindings in web/"
		assigns  = "URL assignments in web/"
	)
	html := func(body string) map[string]string {
		return map[string]string{"web/static/index.html": strings.Replace(cleanWeb["web/static/index.html"], "<body>\n", "<body>\n"+body+"\n", 1)}
	}
	js := func(src string) map[string]string { return map[string]string{"web/static/app.js": src} }
	css := func(src string) map[string]string { return map[string]string{"web/static/style.css": src} }
	tests := []struct {
		name  string
		files map[string]string
		fail  string // the message expected, "" when the check passes
	}{
		{"clean", nil, ""},
		{"external image", html(`<img src="https://cdn.example/a.png">`), external},
		{"external script upper case", html(`<script src="HTTPS://CDN.EXAMPLE/x.js"></script>`), external},
		{"protocol-relative", html(`<img src="//cdn.example/a.png">`), external},
		{"loopback address", html(`<a href="http://127.0.0.1:8080/">x</a>`), external},
		{"localhost", html(`<a href="//localhost/">x</a>`), external},
		{"IPv6", html(`<a href="http://[::1]/">x</a>`), external},
		{"source link look-alike", html(`<a href="https://github.com/rumblebump/talespeak.evil.example">x</a>`), external},
		{"source link outside index.html", js("export const u = \"https://github.com/rumblebump/talespeak\";\n"), external},
		{"css url protocol-relative", css("a { background: url(//cdn.example/a.png) }\n"), external},
		{"css import", css("@import url(\"https://fonts.example/f.css\");\n"), external},
		{"css comment is not exempt", css("/* https://example.com */\n"), external},
		{"websocket URL in code", js("export const u = \"wss://evil.example/ws\";\n"), external},
		{"URL after code on a line", js("export const x = 1; // https://example.com\n"), external},
		{"URL in a comment line", js("  // https://example.com/why\nexport const x = 1;\n"), ""},
		{"vendored files are skipped", map[string]string{"web/static/vendor/lib.js": "// lib\nfetch(\"https://x.example\"); el.innerHTML = 1;\n"}, ""},
		{"innerHTML", js("export function f(el, s) { el.innerHTML = s; }\n"), sinks},
		{"x-html", html(`<p x-html="line"></p>`), sinks},
		{"eval", js("export const f = (s) => eval(s);\n"), sinks},
		{"href binding", html(`<a :href="link">x</a>`), bindings},
		{"style binding", html(`<p x-bind:style="s"></p>`), bindings},
		{"source link with more on its line", html(`<a href="https://github.com/rumblebump/talespeak">a</a><img src="//cdn.example/a.png">`), external},
		{"picture src allowed", html(`<img :src="picture.url">`), ""},
		{"src assignment", js("export function f(img, u) { img.src = u; }\n"), assigns},
		{"fetch", js("export const f = () => fetch(\"/x\");\n"), assigns},
		{"location", js("export function f(u) { location = u; }\n"), assigns},
		{"syntax error", js("export const = ;\n"), "does not parse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, ok := runCheckWeb(t, tt.files)
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

// The client as it is in the repository passes, vendored checksums too.
func TestCheckWebRepository(t *testing.T) {
	checkWebTools(t)
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not found")
	}
	cmd := exec.Command("sh", filepath.Join("check-web.sh"))
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("scripts/check-web.sh: %v\n%s", err, out)
	}
}

// The allowed :src="picture.url" does not excuse another binding on the
// same line.
func TestCheckWebBindingBesidePictureSrc(t *testing.T) {
	checkWebTools(t)
	page := strings.Replace(cleanWeb["web/static/index.html"], `<img :src="picture.url" alt="">`,
		`<img :src="picture.url" alt=""><a :href="link">x</a>`, 1)
	out, ok := runCheckWeb(t, map[string]string{"web/static/index.html": page})
	if ok || !strings.Contains(out, "URL bindings in web/") {
		t.Errorf("check passed a :href binding beside the picture's src:\n%s", out)
	}
}
