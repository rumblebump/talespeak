// Package store is the engine's persistence interface. Game code talks to a
// Store, never to SQL, so the SQLite backend can later be joined by Postgres.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrNameTaken is returned when an account name is in use already, in any
// letter case.
var ErrNameTaken = errors.New("store: name taken")

// Account roles.
const (
	RolePlayer = "player"
	RoleAdmin  = "admin"
)

// World member roles.
const (
	MemberPlayer = "player"
	MemberGM     = "gm"
)

// Account is a login.
type Account struct {
	ID   int64
	Name string // as the player typed it; unique in any letter case
	Role string // RolePlayer or RoleAdmin
}

// World visibilities.
const (
	VisibilityPublic  = "public"  // listed; every account may play
	VisibilityPrivate = "private" // members only, who join with a code
)

// World is one running game of a pack.
type World struct {
	ID          int64
	PackID      string
	PackVersion string
	Title       string // "" shows the pack's title
	Visibility  string // VisibilityPublic or VisibilityPrivate
	CreatedBy   int64  // the account that created it; 0 for the server's main world
	Home        bool   // the server's main world, which OpenWorld returns
	Created     time.Time
	Finished    bool
}

// NewWorld is a world an account creates.
type NewWorld struct {
	PackID, PackVersion, Title, Visibility string
	CreatedBy                              int64
	// PerAccount is how many unfinished worlds the creator may have
	// created, this one included; 0 has no limit. Total is how many
	// unfinished worlds the server may have.
	PerAccount, Total int
}

// WorldInfo is a world as an account sees it in the lobby.
type WorldInfo struct {
	World
	Creator string // the creator's name; "" for the main world or a deleted account
	Role    string // the account's member role, "" when not a member
	Players int    // characters in the world
}

// ErrAccountWorlds and ErrServerWorlds refuse a new world past
// NewWorld.PerAccount and NewWorld.Total.
var (
	ErrAccountWorlds = errors.New("store: too many worlds for the account")
	ErrServerWorlds  = errors.New("store: too many worlds on the server")
)

// MaxLiveInvites is how many invites one world may have that still work.
// CreateInvite refuses more with ErrInvites.
const MaxLiveInvites = 100

// ErrInvites refuses an invite to a world with MaxLiveInvites already.
var ErrInvites = errors.New("store: too many invites to the world")

// Character is one account's player in one world.
type Character struct {
	ID   int64 // never used twice, also after a restart
	Name string
	Room string // where the player was last; "" before the first room
}

// Invite lets accounts join a world. Only a hash of its code is stored.
type Invite struct {
	CodeHash  []byte
	WorldID   int64
	CreatedBy int64  // the account that made it; 0 for the command line
	Role      string // MemberPlayer or MemberGM
	Uses      int    // how many accounts may still use it
	Expires   time.Time
}

// Change is one key of state.player or state.world: Value is its encoding
// (package script), nil removes the key.
type Change struct {
	Key   string
	Value []byte
}

// Save is what one hook call changed. It is written in one transaction.
type Save struct {
	World      int64
	WorldState []Change

	Character   int64 // 0: no player state and no position in this save
	PlayerState []Change
	Room        string // the character's room; "" leaves it as it is
}

// Empty reports whether s writes nothing.
func (s Save) Empty() bool {
	return len(s.WorldState) == 0 && len(s.PlayerState) == 0 && s.Room == ""
}

// Store is implemented by every storage backend.
type Store interface {
	// Ping reports whether the backend can serve requests.
	Ping(ctx context.Context) error
	// Setting returns the value for key, or ErrNotFound.
	Setting(ctx context.Context, key string) (string, error)
	// SetSetting creates or replaces the value for key.
	SetSetting(ctx context.Context, key, value string) error

	Accounts
	Worlds

	// Close releases the backend's resources.
	Close() error
}

// Accounts are logins and their sign-in sessions.
type Accounts interface {
	// CreateAccount adds an account, or returns ErrNameTaken.
	CreateAccount(ctx context.Context, name, passwordHash, role string) (Account, error)
	// AccountByName returns the account named name in any letter case and
	// its password hash, or ErrNotFound.
	AccountByName(ctx context.Context, name string) (Account, string, error)
	// SetPassword replaces the account's password hash and ends all its
	// sign-in sessions.
	SetPassword(ctx context.Context, account int64, passwordHash string) error
	// SetRole changes the account's role.
	SetRole(ctx context.Context, account int64, role string) error

	// CreateLogin stores a sign-in session by the hash of its token. It
	// also drops the sessions expired at now, and the account's oldest
	// ones past keep (never the new one).
	CreateLogin(ctx context.Context, tokenHash []byte, account int64, now, expires time.Time, keep int) error
	// LoginAccount returns the account of an unexpired session, or
	// ErrNotFound.
	LoginAccount(ctx context.Context, tokenHash []byte, now time.Time) (Account, error)
	// DeleteLogin ends a sign-in session.
	DeleteLogin(ctx context.Context, tokenHash []byte) error
}

// Worlds are running games, who may join them, and what their players
// keep.
type Worlds interface {
	// OpenWorld returns the newest unfinished main world of the pack,
	// creating one when there is none. It records the pack version it now
	// runs.
	OpenWorld(ctx context.Context, packID, packVersion string) (World, error)
	// SetVisibility changes who may see and play the world.
	SetVisibility(ctx context.Context, world int64, visibility string) error
	// CreateWorld adds a world and makes its creator a game master of it,
	// or returns ErrAccountWorlds or ErrServerWorlds.
	CreateWorld(ctx context.Context, w NewWorld) (World, error)
	// World returns the world, finished or not, or ErrNotFound.
	World(ctx context.Context, world int64) (World, error)
	// ListWorlds returns the unfinished worlds account may see, at most
	// limit: the public ones and those it is a member of, or with all
	// every one. Those it is a member of come first, then the others; the
	// newest first in each.
	ListWorlds(ctx context.Context, account int64, all bool, limit int) ([]WorldInfo, error)
	// IsGM reports whether the account is a game master of an unfinished
	// world.
	IsGM(ctx context.Context, account int64) (bool, error)
	// FinishWorld ends the world: its state and its players' state are
	// deleted, and OpenWorld starts a new main world.
	FinishWorld(ctx context.Context, world int64) error

	// Member returns the account's role in the world, or ErrNotFound.
	Member(ctx context.Context, world, account int64) (string, error)
	// AddMember makes the account a member, or changes its role.
	AddMember(ctx context.Context, world, account int64, role string) error

	// CreateInvite stores an invite, and drops those expired at now or
	// used up. It returns ErrInvites when the world has MaxLiveInvites.
	CreateInvite(ctx context.Context, inv Invite, now time.Time) error
	// RedeemInvite uses one use of the invite with the code's hash: the
	// account becomes a member of its world with its role, unless it has
	// that role or a higher one already. It returns the world, or
	// ErrNotFound for an unknown, used up or expired code, or one of a
	// finished world.
	RedeemInvite(ctx context.Context, codeHash []byte, account int64, now time.Time) (int64, error)

	// Character returns the account's character in the world, creating it
	// with name when there is none.
	Character(ctx context.Context, world, account int64, name string) (Character, error)
	// PlayerState returns what the character keeps in state.player.
	PlayerState(ctx context.Context, character int64) (map[string][]byte, error)
	// WorldState returns what the world keeps in state.world.
	WorldState(ctx context.Context, world int64) (map[string][]byte, error)
	// Save writes what one hook call changed, all of it or nothing.
	Save(ctx context.Context, s Save) error
}
