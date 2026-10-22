package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/clientip"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
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
	return mux
}

// apiError is an answer other than 2xx: its status and a sentence the
// player can read.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func fail(status int, msg string) error { return &apiError{status, msg} }

var (
	errSignIn     = fail(http.StatusUnauthorized, "Sign in first.")
	errBadRequest = fail(http.StatusBadRequest, "That request was not understood.")
	errSlowDown   = fail(http.StatusTooManyRequests, "Too many tries: wait a minute, then try again.")
	errServer     = fail(http.StatusServiceUnavailable, "The server is busy. Try again in a moment.")
)

// post wraps a JSON POST handler: same origin only, a JSON body of at most
// maxBodyBytes, and a JSON answer.
func (s *Service) post(h func(w http.ResponseWriter, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Requests must come from this site."})
			return
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Send JSON."})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		v, err := h(w, r.WithContext(ctx))
		var api *apiError
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, v)
		case errors.As(err, &api):
			writeJSON(w, api.status, map[string]string{"error": api.msg})
		case errors.Is(err, errBusy):
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": errServer.Error()})
		default:
			s.cfg.Log.Error("account request failed", "path", r.URL.Path, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Something went wrong on the server."})
		}
	}
}

// sameOrigin refuses requests a browser sent from another site. The
// session cookie is SameSite=Strict and the API wants JSON, which another
// site cannot send without asking first; this is the third lock.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser, or one too old to send it: the other locks still hold
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errBadRequest
	}
	return nil
}

// Me is what GET /api/me and the sign-in POSTs answer.
type Me struct {
	Account      *MeAccount `json:"account"` // nil when not signed in
	Registration string     `json:"registration"`
	Mode         string     `json:"mode"`             // "public" or "edu"
	Create       bool       `json:"create"`           // the account may create worlds
	Joined       int64      `json:"joined,omitempty"` // the world POST /api/join joined
}

// MeAccount is the signed-in account.
type MeAccount struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	a, err := s.account(ctx, r)
	if err != nil && !errors.Is(err, errSignIn) {
		s.cfg.Log.Error("account lookup failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Something went wrong on the server."})
		return
	}
	var acc *store.Account
	if err == nil {
		acc = &a
	}
	me, err := s.describe(ctx, acc)
	if err != nil {
		s.cfg.Log.Error("account lookup failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Something went wrong on the server."})
		return
	}
	writeJSON(w, http.StatusOK, me)
}

// describe is the Me of a (nil: not signed in).
func (s *Service) describe(ctx context.Context, a *store.Account) (Me, error) {
	me := Me{Registration: s.cfg.Registration, Mode: "public"}
	if s.cfg.Edu {
		me.Mode = "edu"
	}
	if a == nil {
		return me, nil
	}
	me.Account = &MeAccount{Name: a.Name, Role: a.Role}
	var err error
	me.Create, err = s.mayCreate(ctx, a)
	return me, err
}

// account returns the signed-in account of r, or errSignIn.
func (s *Service) account(ctx context.Context, r *http.Request) (store.Account, error) {
	c, err := r.Cookie(s.cookieName(r))
	if err != nil {
		return store.Account{}, errSignIn
	}
	hash, ok := tokenHash(c.Value)
	if !ok {
		return store.Account{}, errSignIn
	}
	a, err := s.cfg.Store.LoginAccount(ctx, hash, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return store.Account{}, errSignIn
	}
	return a, err
}

// Auth tells the WebSocket endpoint who connects to which world (the
// "world" query parameter): 401 without a sign-in, 404 for a world that
// does not exist or has finished, 403 for one the account may not play.
func (s *Service) Auth(r *http.Request) (ws.Who, int) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	a, err := s.account(ctx, r)
	if errors.Is(err, errSignIn) {
		return ws.Who{}, http.StatusUnauthorized
	}
	if err != nil {
		s.cfg.Log.Error("account lookup failed", "err", err)
		return ws.Who{}, http.StatusServiceUnavailable
	}
	id, ok := worldParam(r.URL.Query().Get("world"))
	if !ok {
		return ws.Who{}, http.StatusNotFound
	}
	wd, role, err := s.world(ctx, id, a.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && wd.Finished) {
		return ws.Who{}, http.StatusNotFound
	}
	if err != nil {
		s.cfg.Log.Error("world lookup failed", "err", err)
		return ws.Who{}, http.StatusServiceUnavailable
	}
	if !s.mayPlay(&a, wd, role) {
		if !s.mayKnow(&a, wd, role) {
			return ws.Who{}, http.StatusNotFound // as if it did not exist
		}
		return ws.Who{}, http.StatusForbidden
	}
	return ws.Who{Account: a.ID, Name: a.Name, World: wd.ID, GM: role == store.MemberGM}, 0
}

type credentials struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (s *Service) registerAccount(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.cfg.Registration != RegistrationSelf {
		return nil, fail(http.StatusForbidden, "New accounts are made by an admin here. Ask yours for one.")
	}
	var c credentials
	if err := decode(r, &c); err != nil {
		return nil, err
	}
	if err := ValidName(c.Name); err != nil {
		return nil, fail(http.StatusBadRequest, err.Error())
	}
	if err := ValidPassword(c.Password); err != nil {
		return nil, fail(http.StatusBadRequest, err.Error())
	}
	if !s.register.allow(clientip.Wide(s.client(r))) {
		return nil, errSlowDown
	}
	hash, err := Hash(r.Context(), c.Password)
	if err != nil {
		return nil, err
	}
	a, err := s.cfg.Store.CreateAccount(r.Context(), c.Name, hash, store.RolePlayer)
	if errors.Is(err, store.ErrNameTaken) {
		return nil, fail(http.StatusConflict, "That name is taken. Pick another one.")
	}
	if err != nil {
		return nil, err
	}
	s.cfg.Log.Info("account registered", "account", a.ID)
	if err := s.startSession(w, r, a.ID); err != nil {
		return nil, err
	}
	return s.describe(r.Context(), &a)
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) (any, error) {
	var c credentials
	if err := decode(r, &c); err != nil {
		return nil, err
	}
	if c.Name == "" || len(c.Name) > maxName || c.Password == "" || len(c.Password) > MaxPassword {
		return nil, errWrongLogin
	}
	// Only wrong passwords use up the per-name limits. The strict one
	// counts per address, so one guesser cannot lock an account out. The
	// loose one counts from every address: a guesser with many addresses
	// can use it up and block the account's sign-ins for a while (a
	// correct password included, else guessing would have no limit).
	client, name := s.client(r), strings.ToLower(c.Name)
	nameHere := name + " " + client
	if !s.signIn.allow(client) || !s.name.ok(nameHere) || !s.nameAll.ok(name) {
		return nil, errSlowDown
	}
	a, hash, err := s.cfg.Store.AccountByName(r.Context(), c.Name)
	missing := errors.Is(err, store.ErrNotFound)
	if err != nil && !missing {
		return nil, err
	}
	if missing {
		hash = dummyHash()
	}
	ok, err := Verify(r.Context(), hash, c.Password)
	if err != nil && !errors.Is(err, errHash) {
		return nil, err
	}
	if missing || !ok {
		s.name.take(nameHere)
		s.nameAll.take(name)
		return nil, errWrongLogin
	}
	if err := s.startSession(w, r, a.ID); err != nil {
		return nil, err
	}
	return s.describe(r.Context(), &a)
}

var errWrongLogin = fail(http.StatusUnauthorized, "Wrong name or password.")

func (s *Service) logout(w http.ResponseWriter, r *http.Request) (any, error) {
	if c, err := r.Cookie(s.cookieName(r)); err == nil {
		if hash, ok := tokenHash(c.Value); ok {
			if err := s.cfg.Store.DeleteLogin(r.Context(), hash); err != nil {
				return nil, err
			}
		}
	}
	http.SetCookie(w, s.cookie(r, "", -1))
	return s.describe(r.Context(), nil)
}

type joinRequest struct {
	Code string `json:"code"`
}

func (s *Service) join(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	var j joinRequest
	if err := decode(r, &j); err != nil {
		return nil, err
	}
	if !s.guess.allow(s.client(r)) || !s.guessAcc.allow(a.Name) {
		return nil, errSlowDown
	}
	code, ok := normalizeCode(j.Code)
	if !ok {
		return nil, errBadCode
	}
	world, err := s.cfg.Store.RedeemInvite(r.Context(), codeHash(code), a.ID, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, errBadCode
	}
	if err != nil {
		return nil, err
	}
	s.cfg.Log.Info("invite used", "account", a.ID, "world", world)
	me, err := s.describe(r.Context(), &a)
	me.Joined = world
	return me, err
}

var errBadCode = fail(http.StatusBadRequest, "That code is not valid, used up or expired.")

type inviteRequest struct {
	World int64 `json:"world"`
	Uses  int   `json:"uses"`
	Days  int   `json:"days"`
	GM    bool  `json:"gm"`
}

// InviteAnswer is what POST /api/invites answers.
type InviteAnswer struct {
	Code    string    `json:"code"`
	Uses    int       `json:"uses"`
	Expires time.Time `json:"expires"`
	GM      bool      `json:"gm,omitempty"`
}

func (s *Service) invite(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	var q inviteRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	wd, role, err := s.world(r.Context(), q.World, a.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && wd.Finished) {
		return nil, errNoWorld
	}
	if err != nil {
		return nil, err
	}
	if !s.mayKnow(&a, wd, role) {
		return nil, errNoWorld
	}
	if !s.mayInvite(&a, wd, role) || (q.GM && a.Role != store.RoleAdmin) {
		return nil, fail(http.StatusForbidden, "Only admins and the world's game masters make invites; only admins make game masters.")
	}
	if q.Uses == 0 {
		q.Uses = 1
	}
	ttl := defaultInviteTTL
	if q.Days != 0 {
		ttl = time.Duration(q.Days) * 24 * time.Hour
	}
	if q.Uses < 1 || q.Uses > MaxInviteUses || q.Days < 0 || q.Days > MaxInviteDays {
		return nil, errBadRequest
	}
	role = store.MemberPlayer
	if q.GM {
		role = store.MemberGM
	}
	if !s.codesAcc.allow(a.Name) {
		return nil, errSlowDown
	}
	code, expires, err := CreateInvite(r.Context(), s.cfg.Store, wd.ID, a.ID, role, q.Uses, s.now(), s.now().Add(ttl))
	if errors.Is(err, store.ErrInvites) {
		return nil, errTooManyCodes
	}
	if err != nil {
		return nil, err
	}
	s.cfg.Log.Info("invite made", "account", a.ID, "world", wd.ID, "uses", q.Uses, "role", role)
	return InviteAnswer{Code: code, Uses: q.Uses, Expires: expires, GM: q.GM}, nil
}

// startSession signs the account in on this browser.
func (s *Service) startSession(w http.ResponseWriter, r *http.Request, account int64) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash, _ := tokenHash(token)
	expires := s.now().Add(SessionDays * 24 * time.Hour)
	if err := s.cfg.Store.CreateLogin(r.Context(), hash, account, s.now(), expires, MaxSessions); err != nil {
		return err
	}
	http.SetCookie(w, s.cookie(r, token, SessionDays*24*60*60))
	return nil
}

func (s *Service) cookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: s.cookieName(r), Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: s.cfg.HTTPS(r), SameSite: http.SameSiteStrictMode}
}

// cookieName is the session cookie's name for r: only the __Host- one is
// read over HTTPS.
func (s *Service) cookieName(r *http.Request) string {
	if s.cfg.HTTPS(r) {
		return SecureCookieName
	}
	return CookieName
}

// tokenHash is what the database keeps of a session token: its SHA-256.
// ok is false for a value no token can have.
func tokenHash(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, false
	}
	h := sha256.Sum256(raw)
	return h[:], true
}
