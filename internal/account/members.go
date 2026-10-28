package account

import (
	"errors"
	"net/http"

	"github.com/rumblebump/talespeak/internal/store"
)

// Who belongs to a world (T-0011): GET /api/members lists them for the
// world's game masters and admins, POST /api/members changes a role. A
// player who plays the world now connects again with the new role, so
// player.gm is never stale.
//
// Who may change what: admins everything. A world's game master takes
// players out of it, and in public mode makes its players game masters
// too; only admins change a game master's role or add accounts by name.
// In edu mode only admins ever make game masters (the edu floor: no game
// master, code or pack makes a kid one).

// MaxMembersListed is the most members GET /api/members lists.
const MaxMembersListed = 500

type memberRequest struct {
	World int64  `json:"world"`
	Name  string `json:"name"`
	Role  string `json:"role"` // "gm", "player" or "none"
}

// MemberAnswer is what POST /api/members answers, and one member in the
// list GET /api/members answers.
type MemberAnswer struct {
	Name   string `json:"name"`
	Role   string `json:"role"`             // "gm", "player" or "none"
	Change bool   `json:"change,omitempty"` // the asking account may change this member's role
}

// MemberList is what GET /api/members answers.
type MemberList struct {
	Members []MemberAnswer `json:"members"`
	// MakeGM is set when the asking account may make members game masters.
	MakeGM bool `json:"makeGM"`
	// Add is set when the asking account may add accounts by name.
	Add bool `json:"add"`
}

const roleNone = "none"

var (
	errNoAccount   = fail(http.StatusNotFound, "There is no account of that name.")
	errOnlyAdmins  = fail(http.StatusForbidden, "Only admins change who is a game master or player of a world.")
	errOnlyAdminGM = fail(http.StatusForbidden, "Only admins make game masters here.")
	errGMRole      = fail(http.StatusForbidden, "Only admins change a game master's role.")
	errNotMember   = fail(http.StatusForbidden, "Only admins add accounts to a world by name. Give them a join code instead.")
)

// mayMakeGM reports whether a, with role in w, may make members of w game
// masters: admins, and outside edu mode the world's game masters.
func (s *Service) mayMakeGM(a *store.Account, role string) bool {
	return a.Role == store.RoleAdmin || (!s.cfg.Edu && role == store.MemberGM)
}

// mayChange reports why a, with role in its world, may not give a member
// whose role is now ("" for none) the role want: nil when it may.
func (s *Service) mayChange(a *store.Account, role, now, want string) error {
	switch {
	case a.Role == store.RoleAdmin:
		return nil
	case role != store.MemberGM:
		return errOnlyAdmins
	case now == "":
		return errNotMember
	case now == store.MemberGM:
		return errGMRole
	case want == store.MemberGM && !s.mayMakeGM(a, role):
		return errOnlyAdminGM
	}
	return nil
}

func (s *Service) listMembers(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	id, ok := worldParam(r.URL.Query().Get("world"))
	if !ok {
		return nil, errNoWorld
	}
	wd, role, err := s.world(r.Context(), id, a.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && wd.Finished) {
		return nil, errNoWorld
	}
	if err != nil {
		return nil, err
	}
	if !s.mayKnow(&a, wd, role) {
		return nil, errNoWorld
	}
	if !s.mayInvite(&a, wd, role) {
		return nil, fail(http.StatusForbidden, "Only admins and the world's game masters see its members.")
	}
	list, err := s.cfg.Store.Members(r.Context(), wd.ID, MaxMembersListed)
	if err != nil {
		return nil, err
	}
	l := MemberList{Members: []MemberAnswer{}, MakeGM: s.mayMakeGM(&a, role), Add: a.Role == store.RoleAdmin}
	for _, m := range list {
		change := s.mayChange(&a, role, m.Role, store.MemberPlayer) == nil && m.Account != a.ID
		l.Members = append(l.Members, MemberAnswer{Name: m.Name, Role: m.Role, Change: change || a.Role == store.RoleAdmin})
	}
	return l, nil
}

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
	if a.Role != store.RoleAdmin && role != store.MemberGM {
		return nil, errOnlyAdmins
	}
	if q.Name == "" || len(q.Name) > maxName {
		return nil, errNoAccount
	}
	if a.Role != store.RoleAdmin && !s.codesAcc.allow(a.Name) {
		return nil, errSlowDown
	}
	who, _, err := s.cfg.Store.AccountByName(r.Context(), q.Name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNoAccount
	}
	if err != nil {
		return nil, err
	}
	now, err := s.cfg.Store.Member(r.Context(), wd.ID, who.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if err := s.mayChange(&a, role, now, q.Role); err != nil {
		return nil, err
	}
	// Logged first: a change the log cannot record does not happen.
	if err := s.cfg.Store.AddAudit(r.Context(), store.AuditEntry{World: wd.ID, Account: a.ID, Name: a.Name, Action: "role",
		Detail: who.Name + ": " + q.Role, At: s.now()}); err != nil {
		return nil, err
	}
	role = ""
	if q.Role == roleNone {
		if _, err := s.cfg.Store.RemoveMember(r.Context(), wd.ID, who.ID); err != nil {
			return nil, err
		}
	} else {
		if err := s.cfg.Store.AddMember(r.Context(), wd.ID, who.ID, q.Role); err != nil {
			return nil, err
		}
		role = q.Role
	}
	s.cfg.Live.RoleChanged(wd.ID, who.ID, role)
	s.cfg.Log.Info("member role changed", "account", a.ID, "world", wd.ID, "member", who.ID, "role", q.Role)
	return MemberAnswer{Name: who.Name, Role: q.Role}, nil
}
