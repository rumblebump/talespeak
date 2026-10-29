package store

import (
	"context"
	"errors"
	"time"
)

// Profiles (T-0010): an account's characters, each bound to a ruleset, with
// its sheet (state.profile) and its progress through the story packs. A
// world's character points to one. See docs/rulesets.md.

// Progress statuses.
const (
	ProgressPlaying   = "playing"   // the profile started the story
	ProgressCompleted = "completed" // a script called progress.complete()
)

// MaxProfiles is how many profiles one account may have.
const MaxProfiles = 20

// ErrProfiles refuses a profile past MaxProfiles; ErrProfileChanged a
// write to a profile that a migration or an undo changed meanwhile.
var (
	ErrProfiles       = errors.New("store: too many profiles for the account")
	ErrProfileChanged = errors.New("store: the profile changed meanwhile")
)

// Profile is one character of an account, bound to a ruleset.
type Profile struct {
	ID      int64
	Account int64
	Name    string // unique per account in any letter case
	Ruleset string // the ruleset pack's id
	// Version is the ruleset version the sheet was last written with.
	Version string
	// Generation counts the migrations and undos: a world that loaded the
	// sheet before one writes nothing more to it.
	Generation int64
	// Undo is the ruleset version of the snapshot a migration kept, which
	// `talespeak profile undo` brings back; "" when there is none.
	Undo    string
	Created time.Time
}

// Progress is how far a profile got in one story pack.
type Progress struct {
	Profile int64
	Pack    string
	Version string // the pack version it last played or completed
	Status  string // ProgressPlaying or ProgressCompleted
}

// ProgressSave is what a hook changed in the profile's progress of the
// world's pack: exported values, and whether it completed the story.
type ProgressSave struct {
	Pack, Version string
	Complete      bool
	Exports       []Change // flags.x and items.x; a nil Value removes one
}

// Bind binds an account's character in a world to a profile, in one
// transaction: the character is created when there is none, a character
// already bound to another profile is refused (ErrProfileChanged), the
// profile's progress of the pack starts, and on the first binding Imports
// are written into the character's state.player.
type Bind struct {
	World, Account int64
	Name           string // the character's name when it is new
	Profile        int64
	Pack, Version  string
	Imports        []Change
}

// Profiles keeps the profiles, their sheets and their progress.
type Profiles interface {
	// CreateProfile adds a profile to the account on ruleset at version,
	// or returns ErrNameTaken or ErrProfiles.
	CreateProfile(ctx context.Context, account int64, name, ruleset, version string) (Profile, error)
	// Profile returns one profile, or ErrNotFound.
	Profile(ctx context.Context, profile int64) (Profile, error)
	// AccountProfiles returns the account's profiles, the oldest first.
	AccountProfiles(ctx context.Context, account int64) ([]Profile, error)
	// AccountProgress returns the progress of all the account's profiles.
	AccountProgress(ctx context.Context, account int64) ([]Progress, error)
	// BoundProfiles returns, for each world where the account has a
	// character bound to a profile, that profile.
	BoundProfiles(ctx context.Context, account int64) (map[int64]int64, error)
	// ProfileState returns what the profile keeps in state.profile.
	ProfileState(ctx context.Context, profile int64) (map[string][]byte, error)
	// Exports returns what the profile exported in pack, by name.
	Exports(ctx context.Context, profile int64, pack string) (map[string][]byte, error)
	// BindProfile binds a character to a profile (see Bind) and returns
	// the character.
	BindProfile(ctx context.Context, b Bind) (Character, error)
	// SetProfileVersion moves the profile to another ruleset version of
	// the same line, unless its generation is no longer gen
	// (ErrProfileChanged).
	SetProfileVersion(ctx context.Context, profile, gen int64, version string) error
	// MigrateProfile replaces the sheet of a profile that is still at
	// version from and generation gen with state at version to, keeping
	// the old sheet as the snapshot Undo brings back; else
	// ErrProfileChanged.
	MigrateProfile(ctx context.Context, profile, gen int64, from, to string, state map[string][]byte) error
	// UndoProfile brings back the sheet and version the last migration
	// kept, or returns ErrNotFound when there is none.
	UndoProfile(ctx context.Context, profile int64) (Profile, error)
}
