package game

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
)

// A story on a ruleset (T-0010): the ruleset's lib modules, and the
// profile each player plays, with its sheet (state.profile) and what the
// story exports to it. The lobby binds a character to a profile and
// migrates it (package profile); a world only loads one that fits and
// writes what its hooks change. See docs/rulesets.md.

// WithRuleset runs the world's story on ruleset, the ruleset version the
// story's worlds run: its lib modules join the story's (a story module of
// the same name wins), and every player plays a profile from st.
func WithRuleset(ruleset *pack.Pack, st store.Profiles) Option {
	return func(w *World) { w.ruleset, w.profiles = ruleset, st }
}

// sessionProfile is the profile a session's player plays. w.mu guards it.
type sessionProfile struct {
	id, gen  int64
	state    script.State      // state.profile
	dirty    []string          // keys of state that changed since the last save
	exports  map[string][]byte // exports since the last save; nil values remove
	complete bool              // progress.complete() since the last save
}

// joinRefusal is why a player may not join, as they read it.
type joinRefusal struct{ msg string }

func (e *joinRefusal) Error() string { return e.msg }

// What a player whose character has no fitting profile is told: the
// lobby binds or checks it again.
var (
	errNoProfile = &joinRefusal{"Pick a character for this world in the lobby first."}
	errRecheck   = &joinRefusal{"Your character has to be checked again: pick it for this world in the lobby."}
)

// modules are the lib modules the world's scripts can require: the
// ruleset's, then the story's over them.
func (w *World) modules() map[string]string {
	if w.ruleset == nil {
		return w.pack.Modules
	}
	all := maps.Clone(w.ruleset.Modules)
	maps.Copy(all, w.pack.Modules)
	return all
}

// packInfo is what scripts read from pack.
func (w *World) packInfo() script.PackInfo {
	info := script.PackInfo{ID: w.pack.ID, Version: w.pack.Version}
	if w.ruleset != nil {
		info.Ruleset, info.RulesetVersion = w.ruleset.ID, w.ruleset.Version
	}
	return info
}

// checkProfile returns the profile character c plays and its sheet, when
// it fits the world's ruleset; nil without a ruleset. A profile on an
// older version of the ruleset's line moves up to the world's. The caller
// holds w.mu.
func (s *session) checkProfile(ctx context.Context, c store.Character) (*store.Profile, map[string][]byte, error) {
	w := s.w
	if w.ruleset == nil {
		return nil, nil, nil
	}
	if c.Profile == 0 {
		return nil, nil, errNoProfile
	}
	p, err := w.profiles.Profile(ctx, c.Profile)
	if err != nil {
		return nil, nil, err
	}
	v, err := pack.ParseVersion(p.Version)
	if err != nil || p.Ruleset != w.ruleset.ID || !pack.Compatible(v).Contains(w.ruleset.V) {
		return nil, nil, errRecheck
	}
	if p.Version != w.ruleset.Version {
		err := w.profiles.SetProfileVersion(ctx, p.ID, p.Generation, w.ruleset.Version)
		if errors.Is(err, store.ErrProfileChanged) {
			return nil, nil, errRecheck
		}
		if err != nil {
			return nil, nil, err
		}
	}
	sheet, err := w.profiles.ProfileState(ctx, p.ID)
	if err != nil {
		return nil, nil, err
	}
	return &p, sheet, nil
}

// loadProfile makes p, with sheet, the profile s plays. The caller holds
// w.mu.
func (s *session) loadProfile(p *store.Profile, sheet map[string][]byte) error {
	if p == nil {
		return nil
	}
	sp := &sessionProfile{id: p.ID, gen: p.Generation}
	sp.state.Reserve = s.reserve
	if err := sp.state.Load(sheet); err != nil {
		return err
	}
	sp.state.Changed = func(key string) {
		if !slices.Contains(sp.dirty, key) {
			sp.dirty = append(sp.dirty, key)
		}
	}
	s.profile = sp
	return nil
}

// releaseProfile gives back the memory the profile's sheet holds. The
// caller holds w.mu.
func (s *session) releaseProfile() {
	if s.profile != nil {
		_ = s.reserve(-s.profile.state.Size())
		s.profile = nil
	}
}

// ProfileState, Export and Complete implement script.ProfileHost.
func (s *session) ProfileState() *script.State {
	if s.profile == nil {
		return nil
	}
	return &s.profile.state
}

func (s *session) Export(name string, value []byte) error {
	if s.profile == nil {
		return errors.New("this player plays no profile")
	}
	if !s.w.pack.Exported(name) {
		return fmt.Errorf("the pack does not export %s: add it to exports in pack.yaml", name)
	}
	if s.profile.exports == nil {
		s.profile.exports = map[string][]byte{}
	}
	s.profile.exports[name] = value
	return nil
}

func (s *session) Complete() error {
	if s.profile == nil {
		return errors.New("this player plays no profile")
	}
	s.profile.complete = true
	return nil
}

// saveProfile adds what the hook changed in s's profile to sv. The caller
// holds w.mu.
func (s *session) saveProfile(sv *store.Save) {
	sp := s.profile
	if sp == nil || s.char == 0 {
		return
	}
	sv.Profile, sv.ProfileGen = sp.id, sp.gen
	for _, k := range sp.dirty {
		v, _ := sp.state.Encoded(k)
		sv.ProfileState = append(sv.ProfileState, store.Change{Key: k, Value: v})
	}
	if len(sp.exports) == 0 && !sp.complete {
		return
	}
	pr := &store.ProgressSave{Pack: s.w.pack.ID, Version: s.w.pack.Version, Complete: sp.complete}
	for _, k := range slices.Sorted(maps.Keys(sp.exports)) {
		pr.Exports = append(pr.Exports, store.Change{Key: k, Value: sp.exports[k]})
	}
	sv.Progress = pr
}

// savedProfile forgets what a save wrote. The caller holds w.mu.
func (s *session) savedProfile() {
	if sp := s.profile; sp != nil {
		sp.dirty, sp.exports, sp.complete = nil, nil, false
	}
}
