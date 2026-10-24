package account

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/rumblebump/talespeak/internal/clientip"
	"github.com/rumblebump/talespeak/internal/store"
)

// Registration settings: who may create an account.
const (
	RegistrationSelf = "self" // anyone, on the sign-in page
	RegistrationOff  = "off"  // only admins, with the command line
)

// World access settings: which signed-in accounts may play the server's
// main world.
const (
	AccessOpen   = "open"   // every account: the main world is public
	AccessInvite = "invite" // members only: the main world is private
)

// Who may create worlds.
const (
	CreateEveryone = "everyone" // every account (public mode only)
	CreateGM       = "gm"       // admins and the game masters of a world
)

// CookieName holds the sign-in session token over plain HTTP (local
// play); over HTTPS it is SecureCookieName, whose __Host- prefix the
// browser only accepts from this host itself, so a sibling subdomain
// cannot plant a session of its own.
const (
	CookieName       = "talespeak_session"
	SecureCookieName = "__Host-talespeak_session"
)

// Session and request limits.
const (
	SessionDays      = 30 // a sign-in lasts this long
	MaxSessions      = 10 // per account; signing in more drops the oldest
	maxBodyBytes     = 4 << 10
	requestTimeout   = 10 * time.Second
	MaxInviteUses    = 100
	MaxInviteDays    = 30
	defaultInviteTTL = 7 * 24 * time.Hour
	// JoinCodeUses is how many players the join code of a new world lets
	// in: a class or a group of friends.
	JoinCodeUses = 30
)

// DefaultAddressBurst lets a class of 30 behind one address register
// and sign in at once.
const DefaultAddressBurst = 40

// Store is what accounts need from the database.
type Store interface {
	store.Accounts
	store.Worlds
	store.Audit
}

// Config sets up a Service.
type Config struct {
	Store        Store
	Registration string // RegistrationSelf or RegistrationOff
	// Edu is edu mode: every world is private, listed only to its members
	// and admins, and only admins and game masters create worlds.
	Edu bool
	// Create is who may create worlds: CreateEveryone or CreateGM; edu
	// mode is always CreateGM.
	Create string
	// Home is the server's main world, which the lobby never ends.
	Home int64
	// Packs are the packs worlds can be created from: the version new
	// worlds start on of each pack id.
	Packs []Pack
	// Runs returns the pack version world w runs, or the reason its
	// players are told when none installed can (hub.Hub.Pack). Nil looks
	// w's pack up in Packs by id.
	Runs func(w store.World) (Pack, error)
	// Live tells how many players are online in a world and ends a world
	// that is loaded; nil when no world is ever loaded (tests).
	Live Live
	// WorldsPerAccount is how many unfinished worlds an account may have
	// created (admins have no limit), MaxWorlds how many the server may
	// have; 0 is DefaultWorldsPerAccount and DefaultMaxWorlds.
	WorldsPerAccount, MaxWorlds int
	// ClientIP finds the client address that the rate limits count by.
	ClientIP func(*http.Request) string
	// HTTPS reports whether the browser reached the server over HTTPS
	// (directly or through a trusted proxy): only then is the cookie
	// marked Secure, so local play over http://localhost still works.
	HTTPS func(*http.Request) bool
	// AddressBurst is how many accounts one client address may register,
	// and how many sign-ins it may try, at once; 0 is DefaultAddressBurst.
	AddressBurst int
	Log          *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Service answers the account API and tells the WebSocket endpoint who
// connects.
type Service struct {
	cfg Config
	now func() time.Time

	register *limiter // per client address
	signIn   *limiter // per client address
	name     *limiter // wrong passwords per account name and address
	nameAll  *limiter // wrong passwords per account name, from any address
	guess    *limiter // invite codes, per client address
	guessAcc *limiter // invite codes, per account
	create   *limiter // new worlds, per client address
	makeAcc  *limiter // new worlds, per account
	codesAcc *limiter // new invites and join codes, per account
	packs    map[string]Pack
}

// New returns a Service.
func New(cfg Config) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if cfg.ClientIP == nil {
		cfg.ClientIP = func(r *http.Request) string {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				return r.RemoteAddr
			}
			return host
		}
	}
	if cfg.HTTPS == nil {
		cfg.HTTPS = func(r *http.Request) bool { return r.TLS != nil }
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.AddressBurst <= 0 {
		cfg.AddressBurst = DefaultAddressBurst
	}
	if cfg.Create != CreateEveryone || cfg.Edu {
		cfg.Create = CreateGM
	}
	if cfg.WorldsPerAccount <= 0 {
		cfg.WorldsPerAccount = DefaultWorldsPerAccount
	}
	if cfg.MaxWorlds <= 0 {
		cfg.MaxWorlds = DefaultMaxWorlds
	}
	if cfg.Live == nil {
		cfg.Live = noLive{}
	}
	packs := map[string]Pack{}
	for _, p := range cfg.Packs {
		packs[p.ID] = p
	}
	if cfg.Runs == nil {
		cfg.Runs = func(w store.World) (Pack, error) {
			p, ok := packs[w.PackID]
			if !ok {
				return Pack{}, errors.New("This world's pack is not installed on this server.")
			}
			return p, nil
		}
	}
	return &Service{
		cfg: cfg, now: now,
		// A class behind one address registers and signs in at once: the
		// bursts fit it, the rates stop a script.
		register: newLimiter(cfg.AddressBurst, 30*time.Second, now),
		signIn:   newLimiter(cfg.AddressBurst, 5*time.Second, now),
		name:     newLimiter(10, time.Minute, now),
		nameAll:  newLimiter(100, 36*time.Second, now), // 100 an hour
		guess:    newLimiter(cfg.AddressBurst, 30*time.Second, now),
		guessAcc: newLimiter(10, time.Minute, now),
		create:   newLimiter(cfg.AddressBurst, time.Minute, now),
		makeAcc:  newLimiter(3, 10*time.Minute, now),
		codesAcc: newLimiter(20, time.Minute, now),
		packs:    packs,
	}
}

// client is what the per-address limits count by: the address, or for
// IPv6 its /64, as the WebSocket limits do.
func (s *Service) client(r *http.Request) string { return clientip.Key(s.cfg.ClientIP(r)) }

// Handler serves the account API under /api/.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("POST /api/register", s.post(s.registerAccount))
	mux.HandleFunc("POST /api/login", s.post(s.login))
	mux.HandleFunc("POST /api/logout", s.post(s.logout))
	mux.HandleFunc("POST /api/join", s.post(s.join))
	mux.HandleFunc("POST /api/invites", s.post(s.invite))
	mux.HandleFunc("GET /api/worlds", s.get(s.worlds))
	mux.HandleFunc("POST /api/worlds", s.post(s.createWorld))
	mux.HandleFunc("POST /api/finish", s.post(s.finish))
	mux.HandleFunc("GET /api/members", s.get(s.listMembers))
	mux.HandleFunc("POST /api/members", s.post(s.members))
	return mux
}
