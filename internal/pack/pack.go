// Package pack loads a story pack's YAML: the pack file and its rooms.
// See docs/content-format.md.
package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
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
	// MaxModules bounds the shared Lua modules in a pack's lib folder.
	MaxModules = 100
	// MaxScriptBytes is the largest Lua script a room may use, and the
	// largest module.
	MaxScriptBytes = 256 << 10
	// MaxItems and MaxLabelBytes bound a room's clickable items, as
	// ui.items does.
	MaxItems      = 20
	MaxLabelBytes = 100
	// MaxAssets bounds the files in assets/.
	MaxAssets = 500
	// MaxPackDescriptionBytes bounds the pack's description in the lobby.
	MaxPackDescriptionBytes = 300
)

// Limits bound a pack's images. Admins can change them (see
// docs/admin-limits.md); the other limits are part of the pack format.
type Limits struct {
	// AssetBytes is the largest single image.
	AssetBytes int64
	// AssetsBytes is the most all of a pack's images may add up to.
	AssetsBytes int64
}

// DefaultLimits are the limits without configuration.
var DefaultLimits = Limits{AssetBytes: 2 << 20, AssetsBytes: 64 << 20}

// Pack is a loaded story pack.
type Pack struct {
	ID string `yaml:"id"`
	// Version is the pack's semantic version, like 1.2.0. Worlds keep
	// running on newer versions with the same major version; a new major
	// version starts new worlds. See docs/pack-versions.md.
	Version string `yaml:"version"`
	Title   string `yaml:"title"`
	// Description is what the lobby shows about the pack; optional.
	Description string `yaml:"description"`
	// Start is the room new players enter; "" for a ruleset pack.
	Start string `yaml:"start"`
	// Manifest is kind, ruleset, continues, imports and exports (ruleset.go).
	Manifest `yaml:",inline"`
	// Settings are the choices each world of the pack makes (settings.go).
	Settings []Setting        `yaml:"settings"`
	Rooms    map[string]*Room `yaml:"-"`
	// Assets are the pack's images by file name, from assets/.
	Assets map[string]*Asset `yaml:"-"`
	// Modules are the sources of lib/<name>.lua by name, what a script's
	// require(name) runs.
	Modules map[string]string `yaml:"-"`
	// V is Version parsed.
	V Version `yaml:"-"`
	// Hash is a SHA-256 of every file the engine read from the pack, in
	// hex: a pack whose files change gets a new hash.
	Hash string `yaml:"-"`
}

// Name is the pack's id and version, like demo 1.2.0, for messages.
func (p *Pack) Name() string { return p.ID + " " + p.Version }

// Room is one place in the story.
type Room struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	// Script is a Lua file in the pack's scripts/ folder, without the folder.
	Script string `yaml:"script"`
	// Items are the things the player can click when entering the room.
	Items []Item `yaml:"items"`
	// Source is the script's content, read by Load.
	Source string `yaml:"-"`
	// File is the YAML file the room came from, for messages.
	File string `yaml:"-"`
}

// Item is something in a room the player can click.
type Item struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Load is LoadLimits with DefaultLimits.
func Load(fsys fs.FS) (*Pack, error) { return LoadLimits(fsys, DefaultLimits) }

// LoadLimits reads pack.yaml, rooms/*.yaml, the scripts they name and the
// images in assets/ from fsys. Every problem found is reported, each
// prefixed with its file.
func LoadLimits(fsys fs.FS, limits Limits) (*Pack, error) {
	var errs []error
	fail := func(file, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", file, fmt.Sprintf(format, args...)))
	}

	p := &Pack{Rooms: map[string]*Room{}, Assets: map[string]*Asset{}, Modules: map[string]string{}}
	fsys = &hashFS{FS: fsys, sums: map[string][sha256.Size]byte{}}
	lint, err := decode(fsys, "pack.yaml", p)
	if err != nil {
		return nil, err
	}
	errs = append(errs, lint...)
	if !idPattern.MatchString(p.ID) {
		fail("pack.yaml", "id %q must be 1 to 64 of a-z, 0-9, _ and -, starting with a letter or digit", p.ID)
	}
	if p.Version == "" {
		fail("pack.yaml", "version is missing: add one like version: 1.0.0")
	} else if v, err := ParseVersion(p.Version); err != nil {
		fail("pack.yaml", "%v", err)
	} else {
		p.V = v
	}
	if err := checkTitle(p.Title); err != nil {
		fail("pack.yaml", "title: %v", err)
	}
	if len(p.Description) > MaxPackDescriptionBytes || !utf8.ValidString(p.Description) {
		fail("pack.yaml", "description: at most %d bytes of text", MaxPackDescriptionBytes)
	}
	errs = append(errs, checkManifest(p)...)
	errs = append(errs, checkSettings(p.Settings)...)

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
		lint, err := decode(fsys, file, r)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, lint...)
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
		if err := checkItems(r.Items); err != nil {
			fail(file, "items: %v", err)
		}
		if r.Script != "" {
			src, err := readScript(fsys, r.Script)
			if err != nil {
				fail(file, "script: %v", err)
			} else {
				errs = append(errs, lintLua("scripts/"+r.Script, src)...)
			}
			r.Source = src
		}
	}
	if p.IsRuleset() {
		errs = append(errs, checkRuleset(fsys, p)...)
	} else if p.Start == "" {
		fail("pack.yaml", "start is missing")
	} else if p.Rooms[p.Start] == nil {
		fail("pack.yaml", "start room %q does not exist", p.Start)
	}
	errs = append(errs, loadModules(fsys, p)...)
	errs = append(errs, loadAssets(fsys, p, limits)...)
	errs = append(errs, lintFiles(fsys)...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	p.Hash = fsys.(*hashFS).sum()
	return p, nil
}

func checkItems(items []Item) error {
	if len(items) > MaxItems {
		return fmt.Errorf("%d items, at most %d", len(items), MaxItems)
	}
	seen := map[string]bool{}
	for _, it := range items {
		if !idPattern.MatchString(it.ID) || seen[it.ID] {
			return fmt.Errorf("id %q must be unique and 1 to 64 of a-z, 0-9, _ and -, starting with a letter or digit", it.ID)
		}
		seen[it.ID] = true
		if strings.TrimSpace(it.Label) == "" || len(it.Label) > MaxLabelBytes || !utf8.ValidString(it.Label) || strings.ContainsAny(it.Label, "\r\n") {
			return fmt.Errorf("label of %q must be one line of 1 to %d bytes", it.ID, MaxLabelBytes)
		}
	}
	return nil
}

// decode reads one YAML file strictly: unknown fields, a second document or
// a file over MaxYAMLBytes are errors. It also returns the file's external
// references, which do not stop the rest of the pack from being checked.
func decode(fsys fs.FS, file string, v any) ([]error, error) {
	data, err := readLimited(fsys, file, MaxYAMLBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: only one YAML document is allowed", file)
	}
	return lintYAML(file, data), nil
}

// loadModules reads lib/*.lua into p.Modules. Other files in lib are an
// error, so a misspelled module is noticed.
func loadModules(fsys fs.FS, p *Pack) []error {
	entries, err := fs.ReadDir(fsys, "lib")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []error{fmt.Errorf("lib: %w", err)}
	}
	var errs []error
	for _, e := range entries {
		file := "lib/" + e.Name()
		name, ok := strings.CutSuffix(e.Name(), ".lua")
		switch {
		case strings.HasPrefix(e.Name(), "."):
			continue
		case e.IsDir() || !ok || !idPattern.MatchString(name):
			errs = append(errs, fmt.Errorf("%s: lib holds only modules named <name>.lua, with a name of 1 to 64 of a-z, 0-9, _ and -", file))
			continue
		case len(p.Modules) >= MaxModules:
			return append(errs, fmt.Errorf("lib: more than %d modules", MaxModules))
		}
		data, err := readRegular(fsys, file, MaxScriptBytes)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", file, err))
			continue
		}
		p.Modules[name] = string(data)
		errs = append(errs, lintLua(file, string(data))...)
	}
	return errs
}

// readScript reads scripts/<name>. name is a plain file name ending in .lua,
// so a pack cannot reach files outside its scripts folder.
func readScript(fsys fs.FS, name string) (string, error) {
	if path.Base(name) != name || !strings.HasSuffix(name, ".lua") || !fs.ValidPath(name) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("%q must be a file name ending in .lua, in the scripts folder", name)
	}
	data, err := readRegular(fsys, "scripts/"+name, MaxScriptBytes)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readRegular is readLimited for a regular file only: a link could reach
// a file outside the pack, and a device or pipe could block the load.
func readRegular(fsys fs.FS, file string, limit int64) ([]byte, error) {
	fi, err := fs.Lstat(fsys, file)
	if err != nil {
		return nil, errors.New("not found")
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("must be a plain file, not a link or folder")
	}
	return readLimited(fsys, file, limit)
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

// hashFS is a file system that keeps a SHA-256 of every file read whole
// from it, for Pack.Hash.
type hashFS struct {
	fs.FS
	mu   sync.Mutex
	sums map[string][sha256.Size]byte
}

func (h *hashFS) Open(name string) (fs.File, error) {
	f, err := h.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &hashFile{File: f, h: h, name: name, sum: sha256.New()}, nil
}

// Lstat and ReadLink keep fs.Lstat working through the wrapper: without
// both, it would follow links.
func (h *hashFS) Lstat(name string) (fs.FileInfo, error) { return fs.Lstat(h.FS, name) }
func (h *hashFS) ReadLink(name string) (string, error)   { return fs.ReadLink(h.FS, name) }

// Stat keeps fs.Stat working through the wrapper.
func (h *hashFS) Stat(name string) (fs.FileInfo, error) { return fs.Stat(h.FS, name) }

// ReadDir keeps fs.ReadDir working through the wrapper, and with it the
// file types its entries report.
func (h *hashFS) ReadDir(name string) ([]fs.DirEntry, error) { return fs.ReadDir(h.FS, name) }

// Glob keeps fs.Glob working through the wrapper.
func (h *hashFS) Glob(pattern string) ([]string, error) { return fs.Glob(h.FS, pattern) }

// sum is the hash over the names and hashes of every file read, in name
// order.
func (h *hashFS) sum() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := make([]string, 0, len(h.sums))
	for n := range h.sums {
		names = append(names, n)
	}
	sort.Strings(names)
	all := sha256.New()
	for _, n := range names {
		s := h.sums[n]
		fmt.Fprintf(all, "%d:%s\x00", len(n), n)
		all.Write(s[:])
	}
	return hex.EncodeToString(all.Sum(nil))
}

// hashFile hashes what is read from it; a file read to its end is
// recorded when it closes.
type hashFile struct {
	fs.File
	h    *hashFS
	name string
	sum  hash.Hash
	eof  bool
}

func (f *hashFile) Read(b []byte) (int, error) {
	n, err := f.File.Read(b)
	f.sum.Write(b[:n])
	if errors.Is(err, io.EOF) {
		f.eof = true
	}
	return n, err
}

func (f *hashFile) Close() error {
	if f.eof {
		var s [sha256.Size]byte
		copy(s[:], f.sum.Sum(nil))
		f.h.mu.Lock()
		f.h.sums[f.name] = s
		f.h.mu.Unlock()
	}
	return f.File.Close()
}
