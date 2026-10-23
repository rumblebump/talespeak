package account

import (
	"errors"
	"net/http"

	"github.com/rumblebump/talespeak/internal/store"
)

// Changing an account's role in a world (T-0011): admins make game masters
// and players, and take accounts out of a world. A player who plays the
// world now connects again with the new role, so player.gm is never stale.

type memberRequest struct {
	World int64  `json:"world"`
	Name  string `json:"name"`
	Role  string `json:"role"` // "gm", "player" or "none"
}

// MemberAnswer is what POST /api/members answers.
type MemberAnswer struct {
	Name string `json:"name"`
	Role string `json:"role"` // "gm", "player" or "none"
}

const roleNone = "none"

func (s *Service) members(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	var q memberRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	if q.Role != store.MemberGM && q.Role != store.MemberPlayer && q.Role != roleNone {
		return nil, errBadRequest
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
	if a.Role != store.RoleAdmin {
		return nil, fail(http.StatusForbidden, "Only admins change who is a game master or player of a world.")
	}
	if q.Name == "" || len(q.Name) > maxName {
		return nil, errNoAccount
	}
	who, _, err := s.cfg.Store.AccountByName(r.Context(), q.Name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNoAccount
	}
	if err != nil {
		return nil, err
	}
	// Logged first: a change the log cannot record does not happen.
	if err := s.cfg.Store.AddAudit(r.Context(), store.AuditEntry{World: wd.ID, Account: a.ID, Name: a.Name, Action: "role",
		Detail: who.Name + ": " + q.Role, At: s.now()}); err != nil {
		return nil, err
	}
	now := ""
	if q.Role == roleNone {
		if _, err := s.cfg.Store.RemoveMember(r.Context(), wd.ID, who.ID); err != nil {
			return nil, err
		}
	} else {
		if err := s.cfg.Store.AddMember(r.Context(), wd.ID, who.ID, q.Role); err != nil {
			return nil, err
		}
		now = q.Role
	}
	s.cfg.Live.RoleChanged(wd.ID, who.ID, now)
	s.cfg.Log.Info("member role changed", "account", a.ID, "world", wd.ID, "member", who.ID, "role", q.Role)
	return MemberAnswer{Name: who.Name, Role: q.Role}, nil
}

var errNoAccount = fail(http.StatusNotFound, "There is no account of that name.")
