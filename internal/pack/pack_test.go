package pack

import (
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rumblebump/talespeak/content"
)

const (
	goodPack = "id: t\ntitle: T\nstart: r\n"
	goodRoom = "id: r\ntitle: R\ndescription: A room.\n"
)

// fsWith returns a minimal valid pack with the given files replaced or
// added. A nil value removes the file.
func fsWith(files map[string]*string) fstest.MapFS {
	fsys := fstest.MapFS{
		"pack.yaml":    {Data: []byte(goodPack)},
		"rooms/r.yaml": {Data: []byte(goodRoom)},
	}
	for name, data := range files {
		if data == nil {
			delete(fsys, name)
			continue
		}
		fsys[name] = &fstest.MapFile{Data: []byte(*data)}
	}
	return fsys
}

func s(v string) *string { return &v }

func TestLoadDemo(t *testing.T) {
	p, err := Load(content.Demo())
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "demo" || p.Start != "cell" || p.Title != "The damp cell" {
		t.Errorf("pack = %+v", p)
	}
	cell := p.Rooms["cell"]
	if cell == nil {
		t.Fatalf("rooms = %v, want cell", p.Rooms)
	}
	if cell.Title != "A damp cell" || cell.Script != "cell.lua" || cell.File != "rooms/cell.yaml" {
		t.Errorf("cell = %+v", cell)
	}
	// ">-" folds the description into one line without a trailing newline.
	if !strings.HasPrefix(cell.Description, "Stone walls, ") || strings.ContainsAny(cell.Description, "\n") {
		t.Errorf("description = %q", cell.Description)
	}
	if !strings.Contains(cell.Source, "function on_enter(player)") {
		t.Errorf("source not read: %q", cell.Source)
	}
}

func TestLoadMinimal(t *testing.T) {
	p, err := Load(fsWith(map[string]*string{
		"rooms/r.yaml":  s(goodRoom + "script: r.lua\n"),
		"scripts/r.lua": s("x = 1"),
		// Only rooms/*.yaml are rooms: other files and folders are ignored.
		"rooms/notes.md":     s("not yaml: ["),
		"rooms/sub/x.yaml":   s("garbage: ["),
		"scripts/unused.lua": s("syntax error ("),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rooms) != 1 || p.Rooms["r"].Source != "x = 1" || p.Rooms["r"].File != "rooms/r.yaml" {
		t.Errorf("rooms = %+v", p.Rooms["r"])
	}
}

func TestLoadErrors(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	tests := []struct {
		name  string
		files map[string]*string
		want  []string // substrings the error must contain
	}{
		{"no pack.yaml", map[string]*string{"pack.yaml": nil}, []string{"pack.yaml"}},
		{"empty pack.yaml", map[string]*string{"pack.yaml": s("")}, []string{"pack.yaml"}},
		{"pack unknown field", map[string]*string{"pack.yaml": s(goodPack + "author: x\n")}, []string{"pack.yaml", "author"}},
		{"pack two documents", map[string]*string{"pack.yaml": s(goodPack + "---\nid: u\n")}, []string{"pack.yaml", "one YAML document"}},
		{"pack wrong type", map[string]*string{"pack.yaml": s("id: [a]\ntitle: T\nstart: r\n")}, []string{"pack.yaml"}},
		{"pack too large", map[string]*string{"pack.yaml": s(goodPack + "# " + long(MaxYAMLBytes) + "\n")}, []string{"pack.yaml", "larger than"}},
		{"pack id empty", map[string]*string{"pack.yaml": s("id: \"\"\ntitle: T\nstart: r\n")}, []string{"pack.yaml", "id"}},
		{"pack id upper case", map[string]*string{"pack.yaml": s("id: Demo\ntitle: T\nstart: r\n")}, []string{"pack.yaml", `"Demo"`}},
		{"pack id too long", map[string]*string{"pack.yaml": s("id: " + long(65) + "\ntitle: T\nstart: r\n")}, []string{"pack.yaml", "id"}},
		{"pack id with dot", map[string]*string{"pack.yaml": s("id: a.b\ntitle: T\nstart: r\n")}, []string{"pack.yaml", "id"}},
		{"pack id with slash", map[string]*string{"pack.yaml": s("id: a/b\ntitle: T\nstart: r\n")}, []string{"pack.yaml", "id"}},
		{"pack title missing", map[string]*string{"pack.yaml": s("id: t\nstart: r\n")}, []string{"pack.yaml", "title"}},
		{"pack title blank", map[string]*string{"pack.yaml": s("id: t\ntitle: \"  \"\nstart: r\n")}, []string{"pack.yaml", "title"}},
		{"pack title two lines", map[string]*string{"pack.yaml": s("id: t\ntitle: \"a\\nb\"\nstart: r\n")}, []string{"pack.yaml", "title"}},
		{"pack title carriage return", map[string]*string{"pack.yaml": s("id: t\ntitle: \"a\\rb\"\nstart: r\n")}, []string{"pack.yaml", "title"}},
		{"pack title 121 characters", map[string]*string{"pack.yaml": s("id: t\ntitle: " + strings.Repeat("é", 121) + "\nstart: r\n")}, []string{"pack.yaml", "title"}},
		{"start missing", map[string]*string{"pack.yaml": s("id: t\ntitle: T\n")}, []string{"pack.yaml", "start is missing"}},
		{"start unknown", map[string]*string{"pack.yaml": s("id: t\ntitle: T\nstart: nowhere\n")}, []string{"pack.yaml", `"nowhere"`}},
		{"no rooms", map[string]*string{"rooms/r.yaml": nil}, []string{"pack.yaml", `start room "r"`}},
		{"room unknown field", map[string]*string{"rooms/r.yaml": s(goodRoom + "exits: [north]\n")}, []string{"rooms/r.yaml", "exits"}},
		{"room two documents", map[string]*string{"rooms/r.yaml": s(goodRoom + "---\n" + goodRoom)}, []string{"rooms/r.yaml", "one YAML document"}},
		{"room invalid yaml", map[string]*string{"rooms/r.yaml": s("id: [r\n")}, []string{"rooms/r.yaml"}},
		{"room empty file", map[string]*string{"rooms/r.yaml": s("")}, []string{"rooms/r.yaml"}},
		{"room too large", map[string]*string{"rooms/r.yaml": s(goodRoom + "# " + long(MaxYAMLBytes) + "\n")}, []string{"rooms/r.yaml", "larger than"}},
		{"room id bad", map[string]*string{"rooms/r.yaml": s("id: R!\ntitle: R\ndescription: d\n")}, []string{"rooms/r.yaml", `"R!"`}},
		{"room id starts with dash", map[string]*string{"rooms/r.yaml": s("id: -r\ntitle: R\ndescription: d\n")}, []string{"rooms/r.yaml", "id"}},
		{"room duplicate id", map[string]*string{"rooms/z.yaml": s(goodRoom)}, []string{"rooms/z.yaml", "already used by rooms/r.yaml"}},
		{"room title missing", map[string]*string{"rooms/r.yaml": s("id: r\ndescription: d\n")}, []string{"rooms/r.yaml", "title"}},
		{"room title too long", map[string]*string{"rooms/r.yaml": s("id: r\ntitle: " + long(121) + "\ndescription: d\n")}, []string{"rooms/r.yaml", "title"}},
		{"room description missing", map[string]*string{"rooms/r.yaml": s("id: r\ntitle: R\n")}, []string{"rooms/r.yaml", "description"}},
		{"room description blank", map[string]*string{"rooms/r.yaml": s("id: r\ntitle: R\ndescription: \" \\n\"\n")}, []string{"rooms/r.yaml", "description"}},
		{"room description too long", map[string]*string{"rooms/r.yaml": s("id: r\ntitle: R\ndescription: " + long(MaxDescriptionBytes+1) + "\n")}, []string{"rooms/r.yaml", "description"}},
		{"script missing", map[string]*string{"rooms/r.yaml": s(goodRoom + "script: gone.lua\n")}, []string{"rooms/r.yaml", "script"}},
		{"script not lua", map[string]*string{"rooms/r.yaml": s(goodRoom + "script: r.txt\n"), "scripts/r.txt": s("x=1")}, []string{"rooms/r.yaml", "ending in .lua"}},
		{"script in a folder", map[string]*string{"rooms/r.yaml": s(goodRoom + "script: sub/r.lua\n"), "scripts/sub/r.lua": s("x=1")}, []string{"rooms/r.yaml", "script"}},
		{"script parent folder", map[string]*string{"rooms/r.yaml": s(goodRoom + "script: ../pack.lua\n"), "pack.lua": s("x=1")}, []string{"rooms/r.yaml", "script"}},
		{"script dot dot", map[string]*string{"rooms/r.yaml": s(goodRoom + "script: ..lua\n"), "scripts/..lua": s("x=1")}, []string{"rooms/r.yaml", "script"}},
		{"script hidden", map[string]*string{"rooms/r.yaml": s(goodRoom + "script: .r.lua\n"), "scripts/.r.lua": s("x=1")}, []string{"rooms/r.yaml", "script"}},
		{"script absolute", map[string]*string{"rooms/r.yaml": s(goodRoom + "script: /etc/r.lua\n")}, []string{"rooms/r.yaml", "script"}},
		{"script backslash", map[string]*string{"rooms/r.yaml": s(goodRoom + "script: \"..\\\\r.lua\"\n")}, []string{"rooms/r.yaml", "script"}},
		{"script too large", map[string]*string{"rooms/r.yaml": s(goodRoom + "script: r.lua\n"), "scripts/r.lua": s(long(MaxScriptBytes + 1))}, []string{"rooms/r.yaml", "larger than"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Load(fsWith(tt.files))
			if err == nil {
				t.Fatalf("Load = %+v, want an error", p)
			}
			if p != nil {
				t.Errorf("Load returned a pack with the error %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestLoadLimitsAreInclusive(t *testing.T) {
	p, err := Load(fsWith(map[string]*string{
		"pack.yaml":     s("id: " + strings.Repeat("a", 64) + "\ntitle: " + strings.Repeat("é", MaxTitleRunes) + "\nstart: " + strings.Repeat("b", 64) + "\n"),
		"rooms/r.yaml":  s("id: " + strings.Repeat("b", 64) + "\ntitle: " + strings.Repeat("x", MaxTitleRunes) + "\ndescription: " + strings.Repeat("d", MaxDescriptionBytes) + "\nscript: r.lua\n"),
		"scripts/r.lua": s(strings.Repeat("-", MaxScriptBytes)),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r := p.Rooms[p.Start]; len(r.Source) != MaxScriptBytes || len(r.Description) != MaxDescriptionBytes {
		t.Errorf("room = %d script bytes, %d description bytes", len(r.Source), len(r.Description))
	}

	// A YAML file of exactly MaxYAMLBytes loads.
	pad := MaxYAMLBytes - len(goodPack) - len("#\n")
	if _, err := Load(fsWith(map[string]*string{"pack.yaml": s(goodPack + "#" + strings.Repeat("x", pad) + "\n")})); err != nil {
		t.Errorf("pack.yaml of %d bytes: %v", MaxYAMLBytes, err)
	}
}

// Every problem is reported, each with its file, in one error.
func TestLoadReportsEveryProblem(t *testing.T) {
	_, err := Load(fsWith(map[string]*string{
		"pack.yaml":    s("id: BAD\ntitle: \"\"\nstart: r\n"),
		"rooms/a.yaml": s("id: a\ntitle: A\ndescription: \"\"\n"),
		"rooms/b.yaml": s("id: b\ntitle: B\ndescription: d\nscript: missing.lua\n"),
		"rooms/c.yaml": s("id: c\nbogus: 1\n"),
	}))
	if err == nil {
		t.Fatal("Load = nil error")
	}
	msg := err.Error()
	for _, want := range []string{"pack.yaml: id", "pack.yaml: title", "rooms/a.yaml: description", "rooms/b.yaml: script", "rooms/c.yaml"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q:\n%s", want, msg)
		}
	}
	// Each problem starts a line with its file.
	n := 0
	for _, line := range strings.Split(msg, "\n") {
		if strings.HasPrefix(line, "pack.yaml: ") || strings.HasPrefix(line, "rooms/") {
			n++
		}
	}
	if n != 5 {
		t.Errorf("error has %d problems, want 5:\n%s", n, msg)
	}
}

func TestLoadTooManyRooms(t *testing.T) {
	fsys := fsWith(nil)
	for i := range MaxRooms + 1 {
		fsys["rooms/x"+strconv.Itoa(i)+".yaml"] = &fstest.MapFile{Data: []byte("id: r" + strconv.Itoa(i) + "\ntitle: R\ndescription: d\n")}
	}
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("err = %v, want too many rooms", err)
	}
}

// An alias bomb fits in 64 KiB but would expand to billions of nodes.
func TestLoadAliasBomb(t *testing.T) {
	bomb := "a: &a [x,x,x,x,x,x,x,x,x]\n"
	prev := "a"
	for _, n := range []string{"b", "c", "d", "e", "f", "g", "h", "i"} {
		bomb += n + ": &" + n + " [*" + prev + ",*" + prev + ",*" + prev + ",*" + prev + ",*" + prev + ",*" + prev + ",*" + prev + ",*" + prev + ",*" + prev + "]\n"
		prev = n
	}
	for _, file := range []string{"pack.yaml", "rooms/r.yaml"} {
		t.Run(file, func(t *testing.T) {
			start := time.Now()
			_, err := Load(fsWith(map[string]*string{file: s(bomb + "title: *" + prev + "\n")}))
			if err == nil {
				t.Fatal("Load = nil error")
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Errorf("took %v", d)
			}
		})
	}
}

// FuzzLoad feeds outside YAML into the loader: it must not panic, and a pack
// it accepts must follow the rules in docs/content-format.md.
func FuzzLoad(f *testing.F) {
	f.Add(goodPack, goodRoom+"script: r.lua\n", "x = 1")
	f.Add("id: t\ntitle: T\nstart: r\n---\n", "id: r\ntitle: R\ndescription: >-\n  a\n  b\n", "")
	f.Add("{id: t, title: T, start: r}", "{id: r, title: R, description: d, script: ../r.lua}", "")
	f.Add("id: &a t\ntitle: *a\nstart: r\n", "id: r\ntitle: R\ndescription: !!binary aGk=\n", "")
	f.Fuzz(func(t *testing.T, packYAML, roomYAML, script string) {
		p, err := Load(fstest.MapFS{
			"pack.yaml":     {Data: []byte(packYAML)},
			"rooms/r.yaml":  {Data: []byte(roomYAML)},
			"scripts/r.lua": {Data: []byte(script)},
		})
		if err != nil {
			if p != nil {
				t.Fatal("pack returned with an error")
			}
			return
		}
		if !idPattern.MatchString(p.ID) || checkTitle(p.Title) != nil {
			t.Fatalf("accepted pack %+v", p)
		}
		start := p.Rooms[p.Start]
		if start == nil {
			t.Fatalf("start room %q missing", p.Start)
		}
		for id, r := range p.Rooms {
			if id != r.ID || !idPattern.MatchString(r.ID) || checkTitle(r.Title) != nil || len(r.Description) > MaxDescriptionBytes || strings.TrimSpace(r.Description) == "" {
				t.Fatalf("accepted room %+v", r)
			}
			if r.Script != "" && (r.Script != "r.lua" || r.Source != script) {
				t.Fatalf("room %q script %q source %q", id, r.Script, r.Source)
			}
		}
	})
}
