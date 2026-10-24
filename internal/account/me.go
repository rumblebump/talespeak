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
// "world" query parameter, and "as=player" for a game master who plays
// as a player): 401 without a sign-in, 404 for a world that
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
	return ws.Who{Account: a.ID, Name: a.Name, World: wd.ID, GM: role == store.MemberGM,
		AsPlayer: r.URL.Query().Get("as") == "player"}, 0
}
