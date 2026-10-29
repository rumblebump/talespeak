package account

import (
	"context"
	"errors"
	"net/http"

	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Who is signed in: GET /api/me, and Auth, which tells the WebSocket
// endpoint who connects to which world.

// Me is what GET /api/me and the sign-in POSTs answer.
type Me struct {
	Account      *MeAccount `json:"account"`          // nil when not signed in
	Registration string     `json:"registration"`     // "self", "off" or "sso"
	Mode         string     `json:"mode"`             // "public" or "edu"
	Create       bool       `json:"create"`           // the account may create worlds
	Joined       int64      `json:"joined,omitempty"` // the world POST /api/join joined
	// ClassCode is set when registering needs the class code, Email when
	// the server sends mail (reset links, and in public mode an address
	// at registration).
	ClassCode bool `json:"classCode,omitempty"`
	Email     bool `json:"email,omitempty"`
	// Notice is a sentence about what the request did, such as a mail
	// that is on its way.
	Notice string `json:"notice,omitempty"`
}

// MeAccount is the signed-in account.
type MeAccount struct {
	Name string `json:"name"`
	Role string `json:"role"`
	// Email is the account's confirmed address; AddEmail is set when it
	// may have one (never an edu mode player), ResetCodes when it makes
	// reset codes for others (admins; game masters in edu mode).
	Email      string `json:"email,omitempty"`
	AddEmail   bool   `json:"addEmail,omitempty"`
	ResetCodes bool   `json:"resetCodes,omitempty"`
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
	me := Me{Registration: s.cfg.Registration, Mode: "public", Email: s.cfg.Email,
		ClassCode: s.cfg.ClassCode != "" && s.cfg.Registration == RegistrationSelf}
	if s.cfg.Edu {
		me.Mode = "edu"
	}
	if a == nil {
		return me, nil
	}
	me.Account = &MeAccount{Name: a.Name, Role: a.Role}
	var err error
	if me.Create, err = s.mayCreate(ctx, a); err != nil {
		return me, err
	}
	if me.Account.ResetCodes, err = s.makesResetCodes(ctx, a); err != nil {
		return me, err
	}
	if s.cfg.Email {
		if err := s.mayHaveEmail(ctx, a); err == nil {
			me.Account.AddEmail = true
		} else if !isAPI(err) {
			return me, err
		}
		if me.Account.Email, err = s.cfg.Store.AccountEmail(ctx, a.ID); err != nil {
			return me, err
		}
	}
	return me, nil
}

// account returns the signed-in account of r, or errSignIn.
func (s *Service) account(ctx context.Context, r *http.Request) (store.Account, error) {
	a, _, err := s.signedIn(ctx, r)
	return a, err
}

// signedIn returns the signed-in account of r and its session token's
// hash, or errSignIn.
func (s *Service) signedIn(ctx context.Context, r *http.Request) (store.Account, []byte, error) {
	c, err := r.Cookie(s.cookieName(r))
	if err != nil {
		return store.Account{}, nil, errSignIn
	}
	hash, ok := tokenHash(c.Value)
	if !ok {
		return store.Account{}, nil, errSignIn
	}
	a, err := s.cfg.Store.LoginAccount(ctx, hash, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return store.Account{}, nil, errSignIn
	}
	return a, hash, err
}

// Auth tells the WebSocket endpoint who connects to which world (the
// "world" query parameter, and "as=player" for a game master who plays
// as a player): 401 without a sign-in, 404 for a world that
// does not exist or has finished, 403 for one the account may not play.
func (s *Service) Auth(r *http.Request) (ws.Who, int) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	a, hash, err := s.signedIn(ctx, r)
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
	return ws.Who{Account: a.ID, Name: a.Name, World: wd.ID, GM: role == store.MemberGM, Login: loginID(hash),
		AsPlayer: r.URL.Query().Get("as") == "player"}, 0
}
