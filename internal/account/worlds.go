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

	"github.com/rumblebump/talespeak/internal/clientip"
	"github.com/rumblebump/talespeak/internal/store"
)

// The lobby: the worlds an account may see, creating a world from a pack,
// and ending one.

// World limits without configuration.
const (
	DefaultWorldsPerAccount = 3
	DefaultMaxWorlds        = 1000
	// MaxListed is the most worlds one lobby page lists, the newest
	// first; MaxPage is the last page an account may ask for.
	MaxListed = 200
	MaxPage   = 1000
	// MaxWorldTitle is the longest world title in characters.
	MaxWorldTitle = 40
)

// Pack is a pack worlds can be created from, as the lobby shows it.
type Pack struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
}

// Live is what the lobby needs from the worlds that are loaded.
type Live interface {
	// Online is how many players play the world now.
	Online(world int64) int
	// Finish ends the world: its players are sent away, it unloads and
	// cannot load again while finish (which marks it finished in the
	// store) runs.
	Finish(world int64, finish func() error) error
	// RoleChanged tells a loaded world that the account's role in it is
	// role now ("" when it is no member any more): its sessions there
	// that play with another role end, and the client connects again
	// with the new one.
	RoleChanged(world, account int64, role string)
}

// noLive is Live when no world is ever loaded.
type noLive struct{}

func (noLive) Online(int64) int                          { return 0 }
func (noLive) Finish(_ int64, finish func() error) error { return finish() }
func (noLive) RoleChanged(int64, int64, string)          {}

var (
	errNoWorld      = fail(http.StatusNotFound, "That world does not exist or has ended.")
	errTooManyCodes = fail(http.StatusConflict, "This world has as many codes as it can keep. Wait until some run out or are used up.")
)

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
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Pack      string `json:"pack"`
	PackTitle string `json:"packTitle"`
	Version   string `json:"version,omitempty"` // the pack version it runs; "" when none installed can
	// Unavailable is why nobody can play it now, when no installed pack
	// version can run it.
	Unavailable string    `json:"unavailable,omitempty"`
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
	// More is set when there are more worlds than this page lists: the
	// next page is ?page=Page+1.
	More bool `json:"more,omitempty"`
	Page int  `json:"page"`
}

func (s *Service) worlds(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		n, ok := worldParam(v) // digits only, as for world ids
		if !ok || n > MaxPage {
			return nil, errBadRequest
		}
		page = int(n)
	}
	return s.lobbyPage(r.Context(), &a, page)
}

func (s *Service) lobby(ctx context.Context, a *store.Account) (Lobby, error) {
	return s.lobbyPage(ctx, a, 1)
}

// lobbyPage is page (from 1) of a's lobby: MaxListed worlds each.
func (s *Service) lobbyPage(ctx context.Context, a *store.Account, page int) (Lobby, error) {
	admin := a.Role == store.RoleAdmin
	list, err := s.cfg.Store.ListWorlds(ctx, a.ID, admin, (page-1)*MaxListed, MaxListed+1)
	if err != nil {
		return Lobby{}, err
	}
	l := Lobby{Worlds: []Card{}, Packs: []Pack{}, Page: page}
	if len(list) > MaxListed {
		l.More, list = true, list[:MaxListed]
	}
	for _, wi := range list { // the account's own worlds first: none drop off the end for others
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
		Players: wi.Players, Online: s.cfg.Live.Online(wi.ID), Role: wi.Role, Main: wi.ID == s.cfg.Home, Created: wi.Created}
	if s.cfg.Edu {
		c.Visibility = store.VisibilityPrivate
	}
	p, err := s.cfg.Runs(wi.World)
	if err == nil {
		c.PackTitle, c.Description, c.Version = p.Title, p.Description, p.Version
	} else {
		c.Unavailable = err.Error()
	}
	if c.Title == "" {
		c.Title = c.PackTitle
	}
	c.Play = s.mayPlay(a, wi.World, wi.Role)
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
	if _, err := s.cfg.Runs(w); err != nil {
		return false
	}
	return a.Role == store.RoleAdmin || role != "" || (!s.cfg.Edu && w.Visibility == store.VisibilityPublic)
}

// mayKnow reports whether a may learn that w exists: whoever may see it
// in the lobby. Everyone else is answered as if it did not exist.
func (s *Service) mayKnow(a *store.Account, w store.World, role string) bool {
	return a.Role == store.RoleAdmin || role != "" || (!s.cfg.Edu && w.Visibility == store.VisibilityPublic)
}

// mayInvite reports whether a may make join codes to w: admins and the
// world's game masters.
func (s *Service) mayInvite(a *store.Account, w store.World, role string) bool {
	return !w.Finished && (a.Role == store.RoleAdmin || role == store.MemberGM)
}

// mayFinish reports whether a may end w: its game masters and admins; the
// server's main world never ends from the lobby (a former one, of a pack
// the server played before, does).
func (s *Service) mayFinish(a *store.Account, w store.World, role string) bool {
	return w.ID != s.cfg.Home && s.mayInvite(a, w, role)
}

// mayCreate reports whether a may create worlds.
func (s *Service) mayCreate(ctx context.Context, a *store.Account) (bool, error) {
	if a.Role == store.RoleAdmin || (s.cfg.Create == CreateEveryone && !s.cfg.Edu) {
		return len(s.cfg.Packs) > 0, nil
	}
	gm, err := s.cfg.Store.IsGM(ctx, a.ID)
	return gm && len(s.cfg.Packs) > 0, err
}

// worldParam reads a world id from a query parameter: digits only, as
// the lobby writes them (no sign, no leading zero).
func worldParam(v string) (int64, bool) {
	if v == "" || v[0] < '1' || v[0] > '9' || strings.TrimLeft(v, "0123456789") != "" {
		return 0, false
	}
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
	if !s.create.allow(clientip.Wide(s.client(r))) || !s.makeAcc.allow(a.Name) {
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
	// A new world has no codes yet: the cap of live codes cannot refuse this one.
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
	if !s.mayKnow(&a, wd, role) {
		return nil, errNoWorld
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

// cleanTitle reads a world title as typed: one line, control, direction
// and blank-looking characters removed, at most 2 combining marks on a
// letter, spaces collapsed, at most MaxWorldTitle characters. "" is fine:
// the card shows the pack's title.
func cleanTitle(s string) (string, bool) {
	if !utf8.ValidString(s) || len(s) > 4*MaxWorldTitle {
		return "", false
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", false
	}
	var b strings.Builder
	space := false
	marks := 0 // combining marks on the last letter
	for _, r := range s {
		switch {
		case unicode.IsSpace(r), blank(r):
			space = true
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r): // format characters: bidi overrides, zero-width
			continue
		case unicode.In(r, unicode.Mn, unicode.Me):
			if marks++; marks > 2 || b.Len() == 0 || space {
				continue // stacked marks draw over the cards around
			}
			b.WriteRune(r)
			continue
		}
		marks = 0
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	t := b.String()
	return t, utf8.RuneCountInString(t) <= MaxWorldTitle
}

// blank reports whether r is a letter that shows as empty space: Hangul
// fillers and the empty Braille pattern.
func blank(r rune) bool {
	switch r {
	case '\u115F', '\u1160', '\u3164', '\uFFA0', '\u2800':
		return true
	}
	return false
}
