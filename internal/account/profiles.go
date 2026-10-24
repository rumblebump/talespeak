package account

import (
	"context"
	"errors"
	"net/http"

	"github.com/rumblebump/talespeak/internal/profile"
	"github.com/rumblebump/talespeak/internal/store"
)

// Profiles in the lobby (T-0010): the card of a world on a ruleset says
// which of the account's characters may play it, and POST /api/profiles
// picks one, or makes a new one, for the world. See docs/rulesets.md.

// Profiles is what the lobby needs from package profile.
type Profiles interface {
	// Cards returns the profile card of each world that runs a story on
	// a ruleset, by world id.
	Cards(ctx context.Context, account int64, worlds []store.World) (map[int64]*profile.Card, error)
	// Pick binds the account's character in w, named name, to profile
	// id, or to a new profile named newName when id is 0. A
	// *profile.Refusal says why it may not.
	Pick(ctx context.Context, account int64, name string, w store.World, id int64, newName string) (profile.Option, error)
}

// addProfiles fills in the profile cards of cards, the lobby's cards of
// the worlds in list.
func (s *Service) addProfiles(ctx context.Context, a *store.Account, cards []Card, list []store.WorldInfo) error {
	if s.cfg.Profiles == nil || len(cards) == 0 {
		return nil
	}
	shown := map[int64]bool{}
	for _, c := range cards {
		shown[c.ID] = true
	}
	var worlds []store.World
	for _, wi := range list {
		if shown[wi.ID] {
			worlds = append(worlds, wi.World)
		}
	}
	got, err := s.cfg.Profiles.Cards(ctx, a.ID, worlds)
	if err != nil {
		return err
	}
	for i := range cards {
		cards[i].Profiles = got[cards[i].ID]
	}
	return nil
}

type pickRequest struct {
	World   int64  `json:"world"`
	Profile int64  `json:"profile"` // 0 makes a new one named Name
	Name    string `json:"name"`
}

// pickProfile answers POST /api/profiles with the lobby, in which the
// world's card shows the picked profile.
func (s *Service) pickProfile(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	var q pickRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	if q.Profile < 0 || len(q.Name) > 4*profile.MaxName {
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
	if !s.mayPlay(&a, wd, role) {
		return nil, fail(http.StatusForbidden, "You cannot play this world now.")
	}
	if s.cfg.Profiles == nil {
		return nil, fail(http.StatusBadRequest, "This world needs no character.")
	}
	if !s.picks.allow(a.Name) {
		return nil, errSlowDown
	}
	_, err = s.cfg.Profiles.Pick(r.Context(), a.ID, a.Name, wd, q.Profile, q.Name)
	var refused *profile.Refusal
	switch {
	case errors.As(err, &refused):
		return nil, fail(http.StatusConflict, refused.Reason)
	case errors.Is(err, profile.ErrNoRuleset):
		return nil, fail(http.StatusBadRequest, "This world needs no character.")
	case err != nil:
		return nil, err
	}
	return s.lobby(r.Context(), &a)
}
