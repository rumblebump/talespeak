package account

import (
	"errors"
	"net/http"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// Joining a world with a code, and the codes admins and game masters
// make (POST /api/join, POST /api/invites).

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
	if role, err := s.cfg.Store.Member(r.Context(), world, a.ID); err == nil {
		s.cfg.Live.RoleChanged(world, a.ID, role) // a game master invite promotes a player who plays now
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
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
	if !s.mayInvite(&a, wd, role) {
		return nil, fail(http.StatusForbidden, "Only admins and the world's game masters make invites.")
	}
	if q.GM && !s.mayMakeGM(&a, role) {
		return nil, errOnlyAdminGM
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
