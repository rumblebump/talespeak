package account

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/clientip"
	"github.com/rumblebump/talespeak/internal/store"
)

// Registering, signing in and out, and the sign-in session cookie.

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
