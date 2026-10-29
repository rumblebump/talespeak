package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

// Rulesets and continuations (T-0010): what a pack.yaml says about the
// ruleset a story is for, the stories it continues and what carries over
// between them. See docs/rulesets.md.

// Kinds of pack.
const (
	// KindStory is a pack players play: rooms and scripts. It is the
	// kind of a pack.yaml without kind.
	KindStory = "story"
	// KindRuleset is a pack of rules that story packs share: lib modules
	// they can require, and migrations of the profiles' sheets. Nobody
	// plays it as a world.
	KindRuleset = "ruleset"
)

// Limits of the manifest, part of the pack format.
const (
	// MaxContinues bounds the packs one story continues.
	MaxContinues = 20
	// MaxCarried bounds the names in imports and in exports.
	MaxCarried = 50
	// MaxMigrations bounds the migration scripts of a ruleset.
	MaxMigrations = 100
)

// Manifest is the part of pack.yaml about rulesets and continuations.
type Manifest struct {
	// Kind is KindStory or KindRuleset; "" is a story.
	Kind string `yaml:"kind"`
	// Ruleset is the ruleset a story is for; nil for a story without
	// one, whose players need no profile.
	Ruleset *Need `yaml:"ruleset"`
	// Continues are the stories this one follows on the same ruleset.
	Continues []Continue `yaml:"continues"`
	// Imports are what a profile brings from the stories it continues,
	// like flags.rescued_fox; Exports what this story gives the stories
	// after it. Each is flags.<name> or items.<name>.
	Imports []string `yaml:"imports"`
	Exports []string `yaml:"exports"`
	// Migrations are a ruleset's migrations/<major>.lua by major version:
	// the one for 2 moves a sheet from 1.x to 2.x.
	Migrations map[uint64]string `yaml:"-"`
}

// Need names a pack and the versions that fit.
type Need struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	// R is Version parsed.
	R Range `yaml:"-"`
}

// Continue is one story a story pack follows.
type Continue struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	// Completed requires a profile to have finished that story before it
	// may start this one; without it the story only brings imports when
	// the profile played it.
	Completed bool  `yaml:"completed"`
	R         Range `yaml:"-"`
}

// IsRuleset reports whether p is a ruleset pack.
func (p *Pack) IsRuleset() bool { return p.Kind == KindRuleset }

// Exported reports whether the story gives name (flags.x or items.x) to
// the stories after it.
func (m *Manifest) Exported(name string) bool {
	for _, e := range m.Exports {
		if e == name {
			return true
		}
	}
	return false
}

var carriedPattern = regexp.MustCompile(`^(flags|items)\.[a-z0-9][a-z0-9_-]{0,57}$`)

// checkManifest checks the manifest fields of pack.yaml.
func checkManifest(p *Pack) []error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("pack.yaml: %s", fmt.Sprintf(format, args...)))
	}
	switch p.Kind {
	case "":
		p.Kind = KindStory
	case KindStory, KindRuleset:
	default:
		fail("kind %q must be story or ruleset", p.Kind)
	}
	if p.IsRuleset() {
		if p.Ruleset != nil || len(p.Continues) > 0 || len(p.Imports) > 0 || len(p.Exports) > 0 {
			fail("a ruleset pack has no ruleset, continues, imports or exports: those belong to story packs")
		}
		if p.Start != "" {
			fail("a ruleset pack has no start room: nobody plays it as a world")
		}
		return errs
	}
	if r := p.Ruleset; r != nil {
		if !idPattern.MatchString(r.ID) {
			fail("ruleset id %q must be 1 to 64 of a-z, 0-9, _ and -, starting with a letter or digit", r.ID)
		}
		if rg, err := ParseRange(r.Version); err != nil {
			fail("ruleset version: %v", err)
		} else {
			r.R = rg
		}
	}
	if len(p.Continues) > MaxContinues {
		fail("continues: %d packs, at most %d", len(p.Continues), MaxContinues)
	}
	seen := map[string]bool{}
	for i := range p.Continues {
		c := &p.Continues[i]
		switch {
		case !idPattern.MatchString(c.ID):
			fail("continues: id %q must be 1 to 64 of a-z, 0-9, _ and -, starting with a letter or digit", c.ID)
		case c.ID == p.ID:
			fail("continues: a pack cannot continue itself")
		case seen[c.ID]:
			fail("continues: %s is listed twice", c.ID)
		}
		seen[c.ID] = true
		if c.Version == "" {
			c.Version = "*"
		}
		if rg, err := ParseRange(c.Version); err != nil {
			fail("continues %s: %v", c.ID, err)
		} else {
			c.R = rg
		}
	}
	if (len(p.Continues) > 0 || len(p.Exports) > 0) && p.Ruleset == nil {
		fail("continues and exports need a ruleset: they live in a player's profile, which is bound to one")
	}
	if len(p.Imports) > 0 && len(p.Continues) == 0 {
		fail("imports come from the packs in continues, and there are none")
	}
	for _, list := range []struct {
		name  string
		names []string
	}{{"imports", p.Imports}, {"exports", p.Exports}} {
		if len(list.names) > MaxCarried {
			fail("%s: %d names, at most %d", list.name, len(list.names), MaxCarried)
		}
		dup := map[string]bool{}
		for _, n := range list.names {
			if !carriedPattern.MatchString(n) {
				fail("%s: %q must be flags.<name> or items.<name>, the name 1 to 58 of a-z, 0-9, _ and -", list.name, n)
			}
			if dup[n] {
				fail("%s: %q is listed twice", list.name, n)
			}
			dup[n] = true
		}
	}
	return errs
}

// checkRuleset checks what only a ruleset pack has, and reads its
// migrations.
func checkRuleset(fsys fs.FS, p *Pack) []error {
	var errs []error
	if len(p.Rooms) > 0 {
		errs = append(errs, errors.New("rooms: a ruleset pack has no rooms: its rules are lib modules the story packs require"))
	}
	entries, err := fs.ReadDir(fsys, "migrations")
	if errors.Is(err, fs.ErrNotExist) {
		return errs
	}
	if err != nil {
		return append(errs, fmt.Errorf("migrations: %w", err))
	}
	p.Migrations = map[uint64]string{}
	for _, e := range entries {
		file := "migrations/" + e.Name()
		num, ok := strings.CutSuffix(e.Name(), ".lua")
		major, err := strconv.ParseUint(num, 10, 64)
		switch {
		case strings.HasPrefix(e.Name(), "."):
			continue
		case e.IsDir() || !ok || err != nil || num != strconv.FormatUint(major, 10):
			errs = append(errs, fmt.Errorf("%s: migrations holds only <major>.lua files, like 2.lua", file))
			continue
		case major < 2 || major > p.V.Major:
			errs = append(errs, fmt.Errorf("%s: a migration moves sheets into a major version from 2 to this pack's %d", file, p.V.Major))
			continue
		case len(p.Migrations) >= MaxMigrations:
			return append(errs, fmt.Errorf("migrations: more than %d", MaxMigrations))
		}
		data, err := readRegular(fsys, file, MaxScriptBytes)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", file, err))
			continue
		}
		p.Migrations[major] = string(data)
		errs = append(errs, lintLua(file, string(data))...)
	}
	return errs
}

// ErrNoRuleset is the error of Catalog.Ruleset when no installed ruleset
// pack fits a story.
var ErrNoRuleset = errors.New("no installed ruleset fits")

// Ruleset returns the version of story's ruleset its worlds run: the
// newest installed ruleset pack in its range (as Match picks). It
// returns nil for a story without a ruleset.
func (c *Catalog) Ruleset(story *Pack) (*Pack, error) {
	if story.Ruleset == nil {
		return nil, nil
	}
	r, err := c.Match(story.Ruleset.ID, story.Ruleset.R)
	if err != nil {
		return nil, fmt.Errorf("%s needs ruleset %s %s: %w", story.Name(), story.Ruleset.ID, story.Ruleset.Version, ErrNoRuleset)
	}
	if !r.IsRuleset() {
		return nil, fmt.Errorf("%s needs ruleset %s, which is a story pack: %w", story.Name(), story.Ruleset.ID, ErrNoRuleset)
	}
	return r, nil
}

// Check reports what makes the catalog unusable: a pack id that is a
// story in one version and a ruleset in another, and a story whose
// ruleset is not installed.
func (c *Catalog) Check() error {
	var errs []error
	for _, id := range c.ids {
		list := c.byID[id]
		for _, p := range list[1:] {
			if p.IsRuleset() != list[0].IsRuleset() {
				errs = append(errs, fmt.Errorf("pack %s is a ruleset in one version and a story in another", id))
				break
			}
		}
		for _, p := range list {
			if _, err := c.Ruleset(p); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Title is the title of the newest installed version of pack id, for
// messages like "Finish Forest Start first"; the id when none is
// installed.
func (c *Catalog) Title(id string) string {
	if p := c.Newest(id); p != nil {
		return p.Title
	}
	return id
}
