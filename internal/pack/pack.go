// Package pack loads a story pack's YAML: the pack file and its rooms.
// See docs/content-format.md.
package pack

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const (
	// MaxYAMLBytes is the largest YAML file a pack may contain.
	MaxYAMLBytes = 64 << 10
	// MaxRooms bounds the rooms in one pack.
	MaxRooms = 1000
	// MaxTitleRunes and MaxDescriptionBytes bound a room's text.
	MaxTitleRunes       = 120
	MaxDescriptionBytes = 4000
	// MaxScriptBytes is the largest Lua script a room may use.
	MaxScriptBytes = 256 << 10
)

// Pack is a loaded story pack.
type Pack struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	// Start is the room new players enter.
	Start string           `yaml:"start"`
	Rooms map[string]*Room `yaml:"-"`
}

// Room is one place in the story.
type Room struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	// Script is a Lua file in the pack's scripts/ folder, without the folder.
	Script string `yaml:"script"`
	// Source is the script's content, read by Load.
	Source string `yaml:"-"`
	// File is the YAML file the room came from, for messages.
	File string `yaml:"-"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Load reads pack.yaml, rooms/*.yaml and the scripts they name from fsys.
// Every problem found is reported, each prefixed with its file.
func Load(fsys fs.FS) (*Pack, error) {
	var errs []error
	fail := func(file, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", file, fmt.Sprintf(format, args...)))
	}

	p := &Pack{Rooms: map[string]*Room{}}
	if err := decode(fsys, "pack.yaml", p); err != nil {
		return nil, err
	}
	if !idPattern.MatchString(p.ID) {
		fail("pack.yaml", "id %q must be 1 to 64 of a-z, 0-9, _ and -, starting with a letter or digit", p.ID)
	}
	if err := checkTitle(p.Title); err != nil {
		fail("pack.yaml", "title: %v", err)
	}

	files, err := fs.Glob(fsys, "rooms/*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) > MaxRooms {
		return nil, fmt.Errorf("rooms: %d files, at most %d", len(files), MaxRooms)
	}
	for _, file := range files {
		r := &Room{File: file}
		if err := decode(fsys, file, r); err != nil {
			errs = append(errs, err)
			continue
		}
		switch {
		case !idPattern.MatchString(r.ID):
			fail(file, "id %q must be 1 to 64 of a-z, 0-9, _ and -, starting with a letter or digit", r.ID)
			continue
		case p.Rooms[r.ID] != nil:
			fail(file, "id %q is already used by %s", r.ID, p.Rooms[r.ID].File)
			continue
		}
		p.Rooms[r.ID] = r
		if err := checkTitle(r.Title); err != nil {
			fail(file, "title: %v", err)
		}
		if strings.TrimSpace(r.Description) == "" || len(r.Description) > MaxDescriptionBytes || !utf8.ValidString(r.Description) {
			fail(file, "description must be 1 to %d bytes of UTF-8 text", MaxDescriptionBytes)
		}
		if r.Script != "" {
			src, err := readScript(fsys, r.Script)
			if err != nil {
				fail(file, "script: %v", err)
			}
			r.Source = src
		}
	}
	if p.Start == "" {
		fail("pack.yaml", "start is missing")
	} else if p.Rooms[p.Start] == nil {
		fail("pack.yaml", "start room %q does not exist", p.Start)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return p, nil
}

// decode reads one YAML file strictly: unknown fields, a second document or
// a file over MaxYAMLBytes are errors.
func decode(fsys fs.FS, file string, v any) error {
	data, err := readLimited(fsys, file, MaxYAMLBytes)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: only one YAML document is allowed", file)
	}
	return nil
}

// readScript reads scripts/<name>. name is a plain file name ending in .lua,
// so a pack cannot reach files outside its scripts folder.
func readScript(fsys fs.FS, name string) (string, error) {
	if path.Base(name) != name || !strings.HasSuffix(name, ".lua") || !fs.ValidPath(name) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("%q must be a file name ending in .lua, in the scripts folder", name)
	}
	data, err := readLimited(fsys, "scripts/"+name, MaxScriptBytes)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readLimited(fsys fs.FS, file string, limit int64) ([]byte, error) {
	f, err := fsys.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return data, nil
}

func checkTitle(s string) error {
	n := utf8.RuneCountInString(s)
	if strings.TrimSpace(s) == "" || n > MaxTitleRunes || !utf8.ValidString(s) || strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("must be one line of 1 to %d characters", MaxTitleRunes)
	}
	return nil
}
