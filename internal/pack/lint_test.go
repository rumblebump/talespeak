package pack

import (
	"strconv"
	"strings"
	"testing"
)

func TestExternalRef(t *testing.T) {
	refs := []string{
		"https://example.com/x.png",
		"see HTTP://EXAMPLE.COM",
		"http://192.168.1.2:8080/",
		"ws://host/ws",
		"wss://host",
		"ftp://files.example",
		"git+ssh://host/repo",
		"//cdn.example.com/lib.js",
		"src=//cdn.example.com",
		"url(//evil.example/a.png)",
		"background: url(https://evil.example/a.png)",
		"//[::1]:80/",
		"visit www.example.com today",
		"at WWW.Example.org",
		"https:evil.example/a.png",
		"https:/evil.example",
		"javascript:alert(1)",
		"//localhost/x",
		"//intranet:8080/x",
	}
	for _, s := range refs {
		if !externalRef.MatchString(s) {
			t.Errorf("%q: not found", s)
		}
	}
	plain := []string{
		"",
		"A damp cell.",
		"7 // 2 is 3",
		"and/or",
		"a//b",
		"https",
		"https:",
		"mailto:someone",
		"data:image/png;base64,AAAA",
		"the www is a web",
		"cells/nails.png",
		"10:30 // later",
	}
	for _, s := range plain {
		if externalRef.MatchString(s) {
			t.Errorf("%q: found a reference", s)
		}
	}
}

func TestLintYAML(t *testing.T) {
	room := "id: r\ntitle: R\n# comment: https://ok.example\ndescription: |\n  A room.\n  Pictures at https://evil.example/map.png\nitems:\n  - id: map\n    label: //cdn.example\n"
	_, err := Load(fsWith(map[string]*string{"rooms/r.yaml": &room}))
	if err == nil {
		t.Fatal("pack with external references loaded")
	}
	msg := err.Error()
	for _, want := range []string{"rooms/r.yaml:6:", "evil.example", "rooms/r.yaml:9:", "cdn.example"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "ok.example") {
		t.Errorf("comment reported:\n%s", msg)
	}
}

func TestLintYAMLKeysAndPackFile(t *testing.T) {
	pk := "id: t\ntitle: Visit www.example.com\nstart: r\n"
	_, err := Load(fsWith(map[string]*string{"pack.yaml": &pk}))
	if err == nil || !strings.Contains(err.Error(), "pack.yaml:2:") {
		t.Fatalf("err = %v, want pack.yaml:2", err)
	}
	if got := lintYAML("x.yaml", []byte("? https://key.example\n: v\n")); len(got) != 1 {
		t.Errorf("key with a URL: %v", got)
	}
}

func TestLintLua(t *testing.T) {
	room := goodRoom + "script: r.lua\n"
	src := `-- Art by someone, https://ok.example (comments are not checked)
--[[ also https://ok.example ]]
function on_enter()
  say("Hello")
  say('see https://evil.example/one')
  say([[long string
  with //cdn.example]])
  say("escaped htt\112s://evil.example/two")
  ui.image("https:" .. "//evil.example/three")
end
`
	_, err := Load(fsWith(map[string]*string{"rooms/r.yaml": &room, "scripts/r.lua": &src}))
	if err == nil {
		t.Fatal("script with external references loaded")
	}
	msg := err.Error()
	for _, want := range []string{"scripts/r.lua:5:", "evil.example/one", "scripts/r.lua:7:", "cdn.example", "scripts/r.lua:8:", "evil.example/two", "scripts/r.lua:9:", "evil.example/three"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "ok.example") {
		t.Errorf("comment reported:\n%s", msg)
	}
}

// A script with a syntax error is left to the compiler; lint reports what
// it scanned before it.
func TestLintLuaStopsAtScanError(t *testing.T) {
	got := lintLua("x.lua", "say('https://a.example') say('unterminated")
	if len(got) != 1 {
		t.Errorf("errors = %v, want the one before the bad string", got)
	}
}

func TestLintErrorsAreBounded(t *testing.T) {
	src := strings.Repeat("say('https://a.example')\n", 100)
	if got := lintLua("x.lua", src); len(got) != maxLintErrors {
		t.Errorf("Lua: %d errors, want %d", len(got), maxLintErrors)
	}
	doc := strings.Repeat("- https://a.example\n", 100)
	if got := lintYAML("x.yaml", []byte(doc)); len(got) != maxLintErrors {
		t.Errorf("YAML: %d errors, want %d", len(got), maxLintErrors)
	}
}

func TestRefErrorSnippet(t *testing.T) {
	long := strings.Repeat("x", 500) + " https://evil.example/" + strings.Repeat("y", 500) + "\n\x00"
	err := refError("f", 3, long)
	msg := err.Error()
	if len(msg) > 250 || !strings.Contains(msg, "f:3:") || !strings.Contains(msg, "https://evil.example") {
		t.Errorf("message = %q", msg)
	}
}

func TestWebFilesRefused(t *testing.T) {
	css, page, readme := "body { color: red }", "<p>hi</p>", "See https://example.com for the artist."
	_, err := Load(fsWith(map[string]*string{"assets/style.CSS": &css, "extra/page.html": &page, "README.md": &readme}))
	if err == nil {
		t.Fatal("pack with web files loaded")
	}
	msg := err.Error()
	for _, want := range []string{"assets/style.CSS: .css files", "extra/page.html: .html files"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "README") {
		t.Errorf("README reported:\n%s", msg)
	}
}

func TestPackFileWalkIsBounded(t *testing.T) {
	files := map[string]*string{}
	x := "x"
	for i := range maxPackFiles + 1 {
		files["notes/"+strconv.Itoa(i)+".txt"] = &x
	}
	_, err := Load(fsWith(files))
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("err = %v, want the file limit", err)
	}
}

// Decode turns a !!binary scalar into its base64-decoded bytes, so the
// text a player sees could hold any reference while lint sees base64:
// tags are refused.
func TestLintRefusesTags(t *testing.T) {
	// aHR0cHM6Ly9ldmlsLmV4YW1wbGUvYS5wbmc= is "https://evil.example/a.png".
	room := "id: r\ntitle: R\ndescription: !!binary aHR0cHM6Ly9ldmlsLmV4YW1wbGUvYS5wbmc=\n" +
		"items:\n  - id: a\n    label: !!binary aHR0cHM6Ly9ldmlsLmV4YW1wbGU=\n"
	p, err := Load(fsWith(map[string]*string{"rooms/r.yaml": &room}))
	if err == nil {
		t.Fatalf("pack loaded with description %q and label %q", p.Rooms["r"].Description, p.Rooms["r"].Items[0].Label)
	}
	if !strings.Contains(err.Error(), "rooms/r.yaml:3:") || !strings.Contains(err.Error(), "rooms/r.yaml:6:") {
		t.Errorf("err = %v, want both references with their lines", err)
	}
}

// A reference inside a multi-line YAML block scalar or Lua long string is
// reported on the line that holds it, not where the text starts.
func TestLintLineInsideMultiLineText(t *testing.T) {
	yamlDoc := "id: r\ntitle: R\ndescription: |\n  One.\n  Two.\n  Three https://evil.example\n"
	if got := errLines(t, "f.yaml", lintYAML("f.yaml", []byte(yamlDoc))); len(got) != 1 || got[0] != 6 {
		t.Errorf("YAML block scalar: lines = %v, want [6]", got)
	}
	lua := "say([[\nOne.\nTwo.\nThree https://evil.example]])"
	if got := errLines(t, "s.lua", lintLua("s.lua", lua)); len(got) != 1 || got[0] != 4 {
		t.Errorf("Lua long string: lines = %v, want [4]", got)
	}
}

// Browsers parse these as references to another host: the WHATWG URL
// parser treats \ as / for http(s), and drops tabs and newlines.
func TestLintBrowserURLForms(t *testing.T) {
	for _, s := range []string{
		`\\evil.example\a.png`,       // protocol-relative with backslashes
		`/\evil.example/a.png`,       // mixed
		`https:\\evil.example/a.png`, // scheme with backslashes
		"/\t/evil.example/a.png",     // tab dropped by the URL parser
		"https:/\n/evil.example",     // newline dropped by the URL parser
	} {
		if _, ok := findRef(s); !ok {
			t.Errorf("%q: not found", s)
		}
	}
}

// Stories use "//" before a word for transmissions and asides. None of
// these has a host with a dot, a port or an IPv6 address.
func TestLintStoryTextWithSlashes(t *testing.T) {
	for _, s := range []string{
		"//INCOMING TRANSMISSION//",
		"The radio crackles. ...//static//...",
		"Left or right? left //right",
	} {
		if ref, ok := findRef(s); ok {
			t.Errorf("%q: found %q", s, ref)
		}
	}
}
