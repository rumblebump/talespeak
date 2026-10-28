package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/clientip"
	"github.com/rumblebump/talespeak/internal/mail"
	"github.com/rumblebump/talespeak/internal/store"
)

// Registering, signing in and out, and the sign-in session cookie.

type credentials struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type registration struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Code     string `json:"code"`  // the class code, when the server has one
	Email    string `json:"email"` // optional, with email on (never in edu mode)
}

func (s *Service) registerAccount(w http.ResponseWriter, r *http.Request) (any, error) {
	switch s.cfg.Registration {
	case RegistrationSelf:
	case RegistrationSSO:
		return nil, fail(http.StatusForbidden, "Accounts here come from your school's or company's sign-in.")
	default:
		return nil, fail(http.StatusForbidden, "New accounts are made by an admin here. Ask yours for one.")
	}
	var c registration
	if err := decode(r, &c); err != nil {
		return nil, err
	}
	if err := ValidName(c.Name); err != nil {
		return nil, fail(http.StatusBadRequest, err.Error())
	}
	if err := ValidPassword(c.Password); err != nil {
		return nil, fail(http.StatusBadRequest, err.Error())
	}
	if c.Email != "" {
		if err := s.mayHaveEmail(r.Context(), nil); err != nil {
			return nil, err
		}
		if err := mail.ValidAddress(c.Email); err != nil {
			return nil, fail(http.StatusBadRequest, err.Error())
		}
	}
	// A wrong class code uses up a try like any other registration.
	if !s.register.allow(clientip.Wide(s.client(r))) {
		return nil, errSlowDown
	}
	if s.cfg.ClassCode != "" && !SameClassCode(c.Code, s.cfg.ClassCode) {
		return nil, fail(http.StatusForbidden, "That class code is not right. Ask your teacher or admin for it.")
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
	notice := ""
	if c.Email != "" {
		// The account works without it: a mail that cannot go now only
		// costs the address, which can be added again later.
		if notice, err = s.sendVerify(r.Context(), a, c.Email); err != nil {
			var api *apiError
			if !errors.As(err, &api) {
				return nil, err
			}
			notice = api.msg
		}
	}
	me, err := s.describe(r.Context(), &a)
	me.Notice = notice
	return me, err
}

// SameClassCode reports whether typed is the class code: letter case,
// spaces and dashes do not matter. It takes as long for any wrong code.
func SameClassCode(typed, code string) bool {
	norm := func(s string) []byte {
		s = strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(s))
		h := sha256.Sum256([]byte(s))
		return h[:]
	}
	return subtle.ConstantTimeCompare(norm(typed), norm(code)) == 1
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) (any, error) {
	var c credentials
	if err := decode(r, &c); err != nil {
		return nil, err
	}
	longest := maxName
	if strings.Contains(c.Name, "@") {
		longest = mail.MaxAddress
	}
	if c.Name == "" || len(c.Name) > longest || c.Password == "" || len(c.Password) > MaxPassword {
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
	a, err := s.signInWith(r.Context(), c.Name, c.Password)
	if errors.Is(err, ErrWrongLogin) {
		s.name.take(nameHere)
		s.nameAll.take(name)
		return nil, errWrongLogin
	}
	if err != nil {
		return nil, err
	}
	if err := s.startSession(w, r, a.ID); err != nil {
		return nil, err
	}
	return s.describe(r.Context(), &a)
}

// signInWith asks each provider in turn: the first that knows name and
// password signs the account in.
func (s *Service) signInWith(ctx context.Context, name, password string) (store.Account, error) {
	for _, p := range s.cfg.Providers {
		a, err := p.SignIn(ctx, name, password)
		if !errors.Is(err, ErrWrongLogin) {
			return a, err
		}
	}
	return store.Account{}, ErrWrongLogin
}

var errWrongLogin = fail(http.StatusUnauthorized, "Wrong name or password.")

func (s *Service) logout(w http.ResponseWriter, r *http.Request) (any, error) {
	if c, err := r.Cookie(s.cookieName(r)); err == nil {
		if hash, ok := tokenHash(c.Value); ok {
			a, err := s.cfg.Store.LoginAccount(r.Context(), hash, s.now())
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
			if err := s.cfg.Store.DeleteLogin(r.Context(), hash); err != nil {
				return nil, err
			}
			if a.ID != 0 {
				s.cfg.SignOut(a.ID, loginID(hash)) // its games on other tabs end too
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

// loginID names a sign-in session for ws.Who.Login: half its token's
// hash, which is no secret, as hex.
func loginID(hash []byte) string { return hex.EncodeToString(hash[:16]) }

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
