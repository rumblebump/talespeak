package account

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rumblebump/talespeak/internal/store"
)

// The lobby: the worlds an account may see, creating a world from a pack,
// and ending one.

// World limits without configuration.
const (
	DefaultWorldsPerAccount = 3
	DefaultMaxWorlds        = 1000
	// MaxListed is the most worlds one lobby lists, the newest first.
	MaxListed = 200
	// MaxWorldTitle is the longest world title in characters.
	MaxWorldTitle = 40
)

// Pack is a pack worlds can be created from, as the lobby shows it.
type Pack struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"-"`
}

// Live is what the lobby needs from the worlds that are loaded.
type Live interface {
	// Online is how many players play the world now.
	Online(world int64) int
	// Finish ends the world: its players are sent away, it unloads and
	// cannot load again while finish (which marks it finished in the
	// store) runs.
	Finish(world int64, finish func() error) error
}

// noLive is Live when no world is ever loaded.
type noLive struct{}

func (noLive) Online(int64) int                          { return 0 }
func (noLive) Finish(_ int64, finish func() error) error { return finish() }

var errNoWorld = fail(http.StatusNotFound, "That world does not exist or has ended.")

// get wraps a JSON GET handler.
func (s *Service) get(h func(w http.ResponseWriter, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		v, err := h(w, r.WithContext(ctx))
		var api *apiError
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, v)
		case errors.As(err, &api):
			writeJSON(w, api.status, map[string]string{"error": api.msg})
		default:
			s.cfg.Log.Error("account request failed", "path", r.URL.Path, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Something went wrong on the server."})
		}
	}
}

// Card is one world in the lobby, as the account sees it.
type Card struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Pack        string    `json:"pack"`
	PackTitle   string    `json:"packTitle"`
	Description string    `json:"description,omitempty"`
	Visibility  string    `json:"visibility"`
	GM          string    `json:"gm,omitempty"` // who created it; "" for the main world
	Players     int       `json:"players"`      // characters in it
	Online      int       `json:"online"`       // players playing it now
	Role        string    `json:"role,omitempty"`
	Main        bool      `json:"main,omitempty"` // the server's main world
	Created     time.Time `json:"created"`
	Play        bool      `json:"play"`   // the account may play it now
	Invite      bool      `json:"invite"` // the account may make join codes
	Finish      bool      `json:"finish"` // the account may end it
}

// Lobby is what GET /api/worlds answers.
type Lobby struct {
	Worlds []Card `json:"worlds"`
	Packs  []Pack `json:"packs"` // what the account may create worlds from; none when it may not
}

func (s *Service) worlds(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	return s.lobby(r.Context(), &a)
}

func (s *Service) lobby(ctx context.Context, a *store.Account) (Lobby, error) {
	admin := a.Role == store.RoleAdmin
	list, err := s.cfg.Store.ListWorlds(ctx, a.ID, admin, MaxListed)
	if err != nil {
		return Lobby{}, err
	}
	l := Lobby{Worlds: []Card{}, Packs: []Pack{}}
	for _, wi := range list {
		if s.cfg.Edu && wi.Role == "" && !admin {
			continue // edu worlds are for their members only, whatever they were before
		}
		l.Worlds = append(l.Worlds, s.card(a, wi))
	}
	create, err := s.mayCreate(ctx, a)
	if err != nil {
		return Lobby{}, err
	}
	if create {
		l.Packs = s.cfg.Packs
	}
	return l, nil
}

// card is wi as a sees it.
func (s *Service) card(a *store.Account, wi store.WorldInfo) Card {
	c := Card{ID: wi.ID, Title: wi.Title, Pack: wi.PackID, PackTitle: wi.PackID, Visibility: wi.Visibility, GM: wi.Creator,
		Players: wi.Players, Online: s.cfg.Live.Online(wi.ID), Role: wi.Role, Main: wi.Home, Created: wi.Created}
	if s.cfg.Edu {
		c.Visibility = store.VisibilityPrivate
	}
	p, installed := s.packs[wi.PackID]
	if installed {
		c.PackTitle, c.Description = p.Title, p.Description
	}
	if c.Title == "" {
		c.Title = c.PackTitle
	}
	c.Play = installed && s.mayPlay(a, wi.World, wi.Role)
	c.Invite = s.mayInvite(a, wi.World, wi.Role)
	c.Finish = s.mayFinish(a, wi.World, wi.Role)
	return c
}

// world returns the world and the account's role in it ("" when not a
// member), or store.ErrNotFound.
func (s *Service) world(ctx context.Context, id, account int64) (store.World, string, error) {
	if id <= 0 {
		return store.World{}, "", store.ErrNotFound
	}
	w, err := s.cfg.Store.World(ctx, id)
	if err != nil {
		return store.World{}, "", err
	}
	role, err := s.cfg.Store.Member(ctx, id, account)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.World{}, "", err
	}
	return w, role, nil
}

// mayPlay reports whether a may play w, where it has role: admins and
// members always, everyone in a public world outside edu mode; nobody once
// it finished or when its pack is not installed.
func (s *Service) mayPlay(a *store.Account, w store.World, role string) bool {
	if w.Finished {
		return false
	}
	if _, ok := s.packs[w.PackID]; !ok {
		return false
	}
	return a.Role == store.RoleAdmin || role != "" || (!s.cfg.Edu && w.Visibility == store.VisibilityPublic)
}

// mayInvite reports whether a may make join codes to w: admins and the
// world's game masters.
func (s *Service) mayInvite(a *store.Account, w store.World, role string) bool {
	return !w.Finished && (a.Role == store.RoleAdmin || role == store.MemberGM)
}

// mayFinish reports whether a may end w: its game masters and admins; the
// server's main world never ends from the lobby.
func (s *Service) mayFinish(a *store.Account, w store.World, role string) bool {
	return !w.Home && s.mayInvite(a, w, role)
}

// mayCreate reports whether a may create worlds.
func (s *Service) mayCreate(ctx context.Context, a *store.Account) (bool, error) {
	if a.Role == store.RoleAdmin || (s.cfg.Create == CreateEveryone && !s.cfg.Edu) {
		return len(s.cfg.Packs) > 0, nil
	}
	gm, err := s.cfg.Store.IsGM(ctx, a.ID)
	return gm && len(s.cfg.Packs) > 0, err
}

// worldParam reads a world id from a query parameter.
func worldParam(v string) (int64, bool) {
	id, err := strconv.ParseInt(v, 10, 64)
	return id, err == nil && id > 0
}

type createRequest struct {
	Pack       string `json:"pack"`
	Title      string `json:"title"`
	Visibility string `json:"visibility"`
}

// Created is what POST /api/worlds answers: the new world, and its join
// code.
type Created struct {
	World  Card         `json:"world"`
	Invite InviteAnswer `json:"invite"`
}

func (s *Service) createWorld(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	var q createRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	ok, err := s.mayCreate(r.Context(), &a)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fail(http.StatusForbidden, "Only admins and game masters create worlds here.")
	}
	p, ok := s.packs[q.Pack]
	if !ok {
		return nil, fail(http.StatusBadRequest, "Pick one of the packs.")
	}
	title, ok := cleanTitle(q.Title)
	if !ok {
		return nil, fail(http.StatusBadRequest, "A world's name is one line of at most "+strconv.Itoa(MaxWorldTitle)+" characters.")
	}
	switch {
	case q.Visibility == "":
		q.Visibility = store.VisibilityPrivate
	case q.Visibility != store.VisibilityPublic && q.Visibility != store.VisibilityPrivate:
		return nil, errBadRequest
	}
	if s.cfg.Edu {
		q.Visibility = store.VisibilityPrivate
	}
	if !s.create.allow(s.client(r)) || !s.makeAcc.allow(a.Name) {
		return nil, errSlowDown
	}
	perAccount := s.cfg.WorldsPerAccount
	if a.Role == store.RoleAdmin {
		perAccount = 0
	}
	wd, err := s.cfg.Store.CreateWorld(r.Context(), store.NewWorld{PackID: p.ID, PackVersion: p.Version, Title: title,
		Visibility: q.Visibility, CreatedBy: a.ID, PerAccount: perAccount, Total: s.cfg.MaxWorlds})
	switch {
	case errors.Is(err, store.ErrAccountWorlds):
		return nil, fail(http.StatusConflict, "You have "+strconv.Itoa(perAccount)+" worlds already. End one before you start another.")
	case errors.Is(err, store.ErrServerWorlds):
		return nil, fail(http.StatusServiceUnavailable, "This server has as many worlds as it can keep. Try again later.")
	case err != nil:
		return nil, err
	}
	code, expires, err := CreateInvite(r.Context(), s.cfg.Store, wd.ID, a.ID, store.MemberPlayer, JoinCodeUses, s.now(), s.now().Add(defaultInviteTTL))
	if err != nil {
		return nil, err
	}
	s.cfg.Log.Info("world created", "account", a.ID, "world", wd.ID, "pack", p.ID, "visibility", wd.Visibility)
	wi := store.WorldInfo{World: wd, Creator: a.Name, Role: store.MemberGM}
	return Created{World: s.card(&a, wi), Invite: InviteAnswer{Code: code, Uses: JoinCodeUses, Expires: expires}}, nil
}

type worldRequest struct {
	World int64 `json:"world"`
}

func (s *Service) finish(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	var q worldRequest
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
	if !s.mayFinish(&a, wd, role) {
		return nil, fail(http.StatusForbidden, "Only the world's game masters and admins end it; the server's main world cannot be ended here.")
	}
	err = s.cfg.Live.Finish(wd.ID, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		return s.cfg.Store.FinishWorld(ctx, wd.ID)
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNoWorld // ended meanwhile
	}
	if err != nil {
		return nil, err
	}
	s.cfg.Log.Info("world finished", "account", a.ID, "world", wd.ID)
	return s.lobby(r.Context(), &a)
}

// cleanTitle reads a world title as typed: one line, control and
// direction characters removed, spaces collapsed, at most MaxWorldTitle
// characters. "" is fine: the card shows the pack's title.
func cleanTitle(s string) (string, bool) {
	if !utf8.ValidString(s) || len(s) > 4*MaxWorldTitle {
		return "", false
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", false
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r): // format characters: bidi overrides, zero-width
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	t := b.String()
	return t, utf8.RuneCountInString(t) <= MaxWorldTitle
}
