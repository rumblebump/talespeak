package profile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
)

// Config sets up a Service.
type Config struct {
	Store store.Profiles
	Packs *pack.Catalog
	// Runs returns the story pack version world w runs (hub.Hub.Pack),
	// or the reason none can.
	Runs func(w store.World) (*pack.Pack, error)
	// Limits bound each migration script, as a world's Lua.
	Limits script.Limits
	Log    *slog.Logger
}

// Service is the profiles of a server.
type Service struct{ cfg Config }

// New returns a Service.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg}
}

// Card is what the lobby shows about the profiles that may play a world
// on a ruleset.
type Card struct {
	// Ruleset is the ruleset's title and the version the world runs.
	Ruleset string `json:"ruleset"`
	// Bound is the profile the account's character in the world plays;
	// nil before it picked one.
	Bound *Option `json:"bound,omitempty"`
	// Options are the account's profiles on this ruleset, when none is
	// bound.
	Options []Option `json:"options"`
	// New is set when the account may make a new profile for the world.
	New bool `json:"new"`
	// Locked is why the account cannot play the world at all, like
	// "Finish Forest Start first."
	Locked string `json:"locked,omitempty"`
}

// Option is one profile as the lobby shows it.
type Option struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"` // the ruleset version of its sheet
	OK      bool   `json:"ok"`
	// Upgrade is the ruleset version the profile moves to when it plays
	// this world; it cannot go back to older worlds then.
	Upgrade string `json:"upgrade,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// rulesetOf returns the story world w runs and its ruleset; nil, nil for
// a story without one.
func (s *Service) rulesetOf(w store.World) (story, ruleset *pack.Pack, err error) {
	story, err = s.cfg.Runs(w)
	if err != nil || story.Ruleset == nil {
		return nil, nil, err
	}
	ruleset, err = s.cfg.Packs.Ruleset(story)
	return story, ruleset, err
}

// account is one account's profiles, their progress and the worlds
// their characters play.
type account struct {
	profiles []store.Profile
	progress []store.Progress
	bound    map[int64]int64 // world -> profile
}

func (s *Service) load(ctx context.Context, id int64) (account, error) {
	var a account
	var err error
	if a.profiles, err = s.cfg.Store.AccountProfiles(ctx, id); err != nil {
		return a, err
	}
	if a.progress, err = s.cfg.Store.AccountProgress(ctx, id); err != nil {
		return a, err
	}
	a.bound, err = s.cfg.Store.BoundProfiles(ctx, id)
	return a, err
}

func (a account) profile(id int64) (store.Profile, bool) {
	for _, p := range a.profiles {
		if p.ID == id {
			return p, true
		}
	}
	return store.Profile{}, false
}

// Cards returns the profile card of each world in worlds that runs a
// story on a ruleset, by world id, as the account sees it.
func (s *Service) Cards(ctx context.Context, accountID int64, worlds []store.World) (map[int64]*Card, error) {
	cards := map[int64]*Card{}
	var a *account
	for _, w := range worlds {
		story, ruleset, err := s.rulesetOf(w)
		if err != nil || ruleset == nil {
			continue // the card says why nobody can play it
		}
		if a == nil {
			loaded, err := s.load(ctx, accountID)
			if err != nil {
				return nil, err
			}
			a = &loaded
		}
		cards[w.ID] = s.card(story, ruleset, *a, w.ID)
	}
	return cards, nil
}

func (s *Service) card(story, ruleset *pack.Pack, a account, world int64) *Card {
	c := &Card{Ruleset: ruleset.Title + " " + ruleset.Version, Options: []Option{}}
	if id := a.bound[world]; id != 0 {
		if p, ok := a.profile(id); ok {
			o := option(p, Check(story, ruleset, s.cfg.Packs, p, a.progress, false))
			c.Bound = &o
			return c
		}
	}
	for _, p := range a.profiles {
		if p.Ruleset == ruleset.ID {
			c.Options = append(c.Options, option(p, Check(story, ruleset, s.cfg.Packs, p, a.progress, true)))
		}
	}
	locked := Locked(story, s.cfg.Packs, 0, nil)
	c.New = locked == "" && len(a.profiles) < store.MaxProfiles
	if !c.New {
		c.Locked = locked
		for _, o := range c.Options {
			if o.OK {
				c.Locked = ""
				break
			}
		}
	}
	return c
}

func option(p store.Profile, f Fit) Option {
	return Option{ID: p.ID, Name: p.Name, Version: p.Version, OK: f.OK, Upgrade: f.Upgrade, Reason: f.Reason}
}

// Refusal is why Pick did not bind a profile, a sentence the player
// reads.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return r.Reason }

func refuse(format string, args ...any) error { return &Refusal{Reason: fmt.Sprintf(format, args...)} }

// ErrNoRuleset is the error of Pick for a world whose story needs no
// profile.
var ErrNoRuleset = errors.New("profile: the world's story has no ruleset")

// Pick binds the account's character in world w (named name when it is
// new) to its profile id, or with id 0 to a new profile named newName.
// A profile on an older major version of the ruleset is migrated first;
// one on an older version of the same line moves up to the world's. The
// first binding copies the story's imports into the character's
// state.player. A *Refusal says why the profile may not play w.
func (s *Service) Pick(ctx context.Context, accountID int64, name string, w store.World, id int64, newName string) (Option, error) {
	story, ruleset, err := s.rulesetOf(w)
	if err != nil {
		return Option{}, &Refusal{Reason: err.Error()}
	}
	if ruleset == nil {
		return Option{}, ErrNoRuleset
	}
	a, err := s.load(ctx, accountID)
	if err != nil {
		return Option{}, err
	}
	bound := a.bound[w.ID]
	switch {
	case bound != 0 && id != bound:
		p, _ := a.profile(bound)
		return Option{}, refuse("Your character in this world is %s.", p.Name)
	case id == 0:
		if why := Locked(story, s.cfg.Packs, 0, nil); why != "" {
			return Option{}, &Refusal{Reason: why}
		}
		clean, err := ValidName(newName)
		if err != nil {
			return Option{}, &Refusal{Reason: err.Error()}
		}
		p, err := s.cfg.Store.CreateProfile(ctx, accountID, clean, ruleset.ID, ruleset.Version)
		switch {
		case errors.Is(err, store.ErrNameTaken):
			return Option{}, refuse("You have a character named %s already.", clean)
		case errors.Is(err, store.ErrProfiles):
			return Option{}, refuse("You have %d characters already, as many as an account may have.", store.MaxProfiles)
		case err != nil:
			return Option{}, err
		}
		a.profiles = append(a.profiles, p)
		id = p.ID
	}
	p, ok := a.profile(id)
	if !ok {
		return Option{}, refuse("That character does not exist.")
	}
	fit := Check(story, ruleset, s.cfg.Packs, p, a.progress, bound == 0)
	if !fit.OK {
		return option(p, fit), &Refusal{Reason: fit.Reason}
	}
	if p, err = s.moveUp(ctx, p, ruleset); err != nil {
		return Option{}, err
	}
	var imports []store.Change
	if bound == 0 {
		if imports, err = s.imports(ctx, story, p.ID, a.progress); err != nil {
			return Option{}, err
		}
	}
	_, err = s.cfg.Store.BindProfile(ctx, store.Bind{World: w.ID, Account: accountID, Name: name, Profile: p.ID,
		Pack: story.ID, Version: story.Version, Imports: imports})
	if errors.Is(err, store.ErrProfileChanged) {
		return Option{}, refuse("Your character in this world changed meanwhile. Look at the lobby again.")
	}
	if err != nil {
		return Option{}, err
	}
	s.cfg.Log.Info("profile picked", "account", accountID, "world", w.ID, "profile", p.ID, "ruleset", p.Ruleset, "version", p.Version)
	return option(p, Fit{OK: true}), nil
}

// moveUp brings p to ruleset's version: a migration for an older major
// version, a new version number for an older version of the same line.
func (s *Service) moveUp(ctx context.Context, p store.Profile, ruleset *pack.Pack) (store.Profile, error) {
	v, _ := pack.ParseVersion(p.Version) // Check read it
	switch {
	case v.Compare(ruleset.V) == 0:
		return p, nil
	case pack.Compatible(v).Contains(ruleset.V):
		if err := s.cfg.Store.SetProfileVersion(ctx, p.ID, p.Generation, ruleset.Version); err != nil {
			return p, s.changed(err)
		}
		p.Version = ruleset.Version
		return p, nil
	}
	sheet, err := s.cfg.Store.ProfileState(ctx, p.ID)
	if err != nil {
		return p, err
	}
	for m := v.Major + 1; m <= ruleset.V.Major; m++ {
		file := "migrations/" + strconv.FormatUint(m, 10) + ".lua"
		sheet, err = script.Migrate(s.cfg.Limits, file, ruleset.Migrations[m], sheet)
		if err != nil {
			s.cfg.Log.Error("profile migration failed", "profile", p.ID, "ruleset", ruleset.Name(), "migration", file, "err", err)
			return p, refuse("%s could not be moved to %s %s: the ruleset's migration failed. Tell an admin.", p.Name, ruleset.Title, ruleset.Version)
		}
	}
	if err := s.cfg.Store.MigrateProfile(ctx, p.ID, p.Generation, p.Version, ruleset.Version, sheet); err != nil {
		return p, s.changed(err)
	}
	s.cfg.Log.Info("profile migrated", "profile", p.ID, "ruleset", p.Ruleset, "from", p.Version, "to", ruleset.Version)
	p.Undo, p.Version, p.Generation = p.Version, ruleset.Version, p.Generation+1
	return p, nil
}

func (s *Service) changed(err error) error {
	if errors.Is(err, store.ErrProfileChanged) {
		return refuse("This character changed meanwhile. Try again.")
	}
	return err
}

// imports are what story imports from the stories it continues: for
// each name, the value the first story in continues that the profile
// played (in its range) exported.
func (s *Service) imports(ctx context.Context, story *pack.Pack, profile int64, progress []store.Progress) ([]store.Change, error) {
	if len(story.Imports) == 0 {
		return nil, nil
	}
	var out []store.Change
	exported := map[string]map[string][]byte{}
	for _, name := range story.Imports {
		for _, c := range story.Continues {
			if _, ok := played(c, profile, progress); !ok {
				continue
			}
			vals, ok := exported[c.ID]
			if !ok {
				var err error
				if vals, err = s.cfg.Store.Exports(ctx, profile, c.ID); err != nil {
					return nil, err
				}
				exported[c.ID] = vals
			}
			if v, ok := vals[name]; ok {
				out = append(out, store.Change{Key: name, Value: v})
				break
			}
		}
	}
	return out, nil
}

// MaxName is the longest profile name in characters.
const MaxName = 20

// ValidName cleans a new profile's name as typed: spaces trimmed and
// collapsed; 1 to MaxName ASCII letters, digits, spaces, - and _,
// starting with a letter. It returns what is wrong as a sentence.
func ValidName(name string) (string, error) {
	clean := strings.Join(strings.Fields(name), " ")
	if clean == "" || len(clean) > MaxName {
		return "", fmt.Errorf("A character's name is 1 to %d letters long.", MaxName)
	}
	for i, r := range clean {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if i == 0 && !letter {
			return "", errors.New("A character's name starts with a letter.")
		}
		if !letter && !(r >= '0' && r <= '9') && r != ' ' && r != '-' && r != '_' {
			return "", errors.New("A character's name uses only letters, digits, spaces, - and _.")
		}
	}
	return clean, nil
}
