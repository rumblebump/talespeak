package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/hub"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// FuzzCleanTitle feeds typed world titles: a title it accepts is one
// line of at most MaxWorldTitle characters, without control or format
// characters, with single spaces between words and none at the ends, and
// cleaning it again changes nothing.
func FuzzCleanTitle(f *testing.F) {
	for _, s := range []string{
		"", "Our game", "  Our   game ", "a\tb", "a\nb", "a\rb", "x\u202ey\u200bz\u2066", "\xff", "\u00a0lead\u3000",
		"a\u2028b", "a\u0085b", "\ufeffbom", "e\u0301", strings.Repeat("\u00f6", MaxWorldTitle), strings.Repeat("x", MaxWorldTitle+1),
		strings.Repeat(" ", 4*MaxWorldTitle+1), strings.Repeat("\u200b", 40) + "a", "<script>alert(1)</script>", "\x00\x01\x7f",
		"\U0001f469\u200d\U0001f469\u200d\U0001f467", "\U0010ffff", "\ud7ff",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out, ok := cleanTitle(in)
		if strings.ContainsAny(in, "\r\n") || !utf8.ValidString(in) || len(in) > 4*MaxWorldTitle {
			if ok {
				t.Fatalf("cleanTitle(%q) = %q, ok", in, out)
			}
			return
		}
		if !ok {
			if utf8.RuneCountInString(out) <= MaxWorldTitle {
				t.Fatalf("cleanTitle(%q) refused %q of %d characters", in, out, utf8.RuneCountInString(out))
			}
			return
		}
		if !utf8.ValidString(out) || utf8.RuneCountInString(out) > MaxWorldTitle || len(out) > len(in) {
			t.Fatalf("cleanTitle(%q) = %q", in, out)
		}
		if strings.HasPrefix(out, " ") || strings.HasSuffix(out, " ") || strings.Contains(out, "  ") {
			t.Fatalf("cleanTitle(%q) = %q: spaces left", in, out)
		}
		for _, r := range out {
			if r != ' ' && unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("cleanTitle(%q) = %q: keeps %U", in, out, r)
			}
		}
		if again, ok := cleanTitle(out); !ok || again != out {
			t.Fatalf("cleanTitle(%q) = %q, but cleaning that gives %q, %v", in, out, again, ok)
		}
	})
}

// FuzzWorldParam: a world id from a query is a positive int64, or
// nothing.
func FuzzWorldParam(f *testing.F) {
	for _, s := range []string{"", "1", "0", "-1", "+1", "01", "x", "1.0", "1e3", " 1", "9223372036854775807", "9223372036854775808", "0x10", "\u0661"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, v string) {
		id, ok := worldParam(v)
		want, err := strconv.ParseInt(v, 10, 64)
		canonical := err == nil && want > 0 && strconv.FormatInt(want, 10) == v // as the lobby writes ids
		if ok != canonical || (ok && id != want) {
			t.Fatalf("worldParam(%q) = %d, %v", v, id, ok)
		}
	})
}

// How /ws?world= reads its world: the first one given, decimal only, and
// numbers out of range are no world.
func TestAuthWorldParamForms(t *testing.T) {
	f := newFixture(t, Config{})
	b := f.signUp("mira")
	home := strconv.FormatInt(f.world.ID, 10)
	for _, tt := range []struct {
		query string
		want  int
	}{
		{"world=" + home, 200},
		{"world=0" + home, 404},   // only the form the lobby writes: no leading zeros
		{"world=%2B" + home, 404}, // and no sign
		{"world=" + home + "&world=9999", 200},
		{"world=9999&world=" + home, 404},
		{"world=9223372036854775807", 404},
		{"world=9223372036854775808", 404},
		{"world=%20" + home, 404},
		{"world=" + home + ".0", 404},
		{"World=" + home, 404},
	} {
		if code := b.get("/ws?"+tt.query, nil); code != tt.want {
			t.Errorf("/ws?%s = %d, want %d", tt.query, code, tt.want)
		}
	}
}

// An admin's lobby lists every world, private ones of others too, and
// the admin may play, invite to and end them.
func TestAdminLobbyListsPrivate(t *testing.T) {
	f := newFixture(t, Config{})
	ann := f.signUp("ann")
	made, _ := ann.create(`{"pack":"test-pack","visibility":"private","title":"Ann's"}`)
	admin := f.admin("root")
	c, ok := admin.card(made.World.ID)
	if !ok || c.Role != "" || !c.Play || !c.Invite || !c.Finish || c.GM != "ann" || c.Visibility != "private" {
		t.Errorf("admin's card of a private world = %+v, %v", c, ok)
	}
	if code := admin.get(f.ws(made.World.ID), nil); code != 200 {
		t.Errorf("admin ws to a private world = %d", code)
	}
	if l := admin.lobby(); len(l.Packs) != len(testPacks) {
		t.Errorf("admin's packs = %+v", l.Packs)
	}
	if h, ok := admin.card(0); !ok || !h.Main || h.Finish || h.GM != "" {
		t.Errorf("admin's card of the main world = %+v, %v", h, ok)
	}
}

// A game master of one world has no say over another: no codes, no
// ending it, no playing it when it is private.
func TestGMOfOtherWorld(t *testing.T) {
	f := newFixture(t, Config{})
	ann, bob := f.signUp("ann"), f.signUp("bob")
	a, _ := ann.create(`{"pack":"demo","visibility":"private"}`)
	b, _ := bob.create(`{"pack":"demo","visibility":"public"}`)
	priv, _ := bob.create(`{"pack":"demo","visibility":"private"}`)
	// A world Ann cannot see may be refused as not found, so as not to
	// tell her it exists.
	for _, tt := range []struct {
		id      int64
		private bool
	}{{b.World.ID, false}, {priv.World.ID, true}, {f.world.ID, false}} {
		w := `{"world":` + strconv.FormatInt(tt.id, 10) + `}`
		refused := func(code int) bool { return code == 403 || (tt.private && code == 404) }
		if code := ann.post("/api/invites", w, nil); !refused(code) {
			t.Errorf("Ann makes a code to world %d: %d", tt.id, code)
		}
		if code := ann.post("/api/finish", w, nil); !refused(code) {
			t.Errorf("Ann ends world %d: %d", tt.id, code)
		}
	}
	if code := ann.get(f.ws(priv.World.ID), nil); code != 403 && code != 404 {
		t.Errorf("Ann plays Bob's private world: %d", code)
	}
	if c, ok := ann.card(b.World.ID); !ok || c.Invite || c.Finish || !c.Play || c.Role != "" {
		t.Errorf("Ann's card of Bob's public world = %+v, %v", c, ok)
	}
	// Bob's own world is still there, and Ann's is hers.
	if c, ok := bob.card(priv.World.ID); !ok || !c.Finish {
		t.Errorf("Bob's world after Ann's tries = %+v, %v", c, ok)
	}
	if c, ok := ann.card(a.World.ID); !ok || c.Role != "gm" {
		t.Errorf("Ann's own card = %+v, %v", c, ok)
	}
}

// New worlds from one address are limited there, across accounts; other
// addresses are not slowed.
func TestCreateWorldAddressLimit(t *testing.T) {
	f := newFixture(t, Config{ClientIP: headerIP, AddressBurst: 2})
	var codes []int
	for i := range 3 {
		b := f.browser()
		name := "player" + strconv.Itoa(i)
		if code := b.postFrom("198.51.100."+strconv.Itoa(i+1), "/api/register", `{"name":"`+name+`","password":"correct horse"}`, nil); code != 200 {
			t.Fatalf("register %s: %d", name, code)
		}
		codes = append(codes, b.postFrom("203.0.113.9", "/api/worlds", `{"pack":"demo"}`, nil))
		if i == 2 {
			if code := b.postFrom("203.0.113.10", "/api/worlds", `{"pack":"demo"}`, nil); code != 200 {
				t.Errorf("from another address: %d", code)
			}
		}
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Errorf("three accounts from one address create: %v, want 200 200 429", codes)
	}
}

// The lobby lists at most MaxListed worlds, the newest first.
func TestLobbyCap(t *testing.T) {
	f := newFixture(t, Config{})
	admin := f.admin("root")
	ctx := context.Background()
	a, _, _ := f.st.AccountByName(ctx, "root")
	var last int64
	for range MaxListed + 1 {
		w, err := f.st.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPrivate, CreatedBy: a.ID})
		if err != nil {
			t.Fatal(err)
		}
		last = w.ID
	}
	l := admin.lobby()
	if len(l.Worlds) != MaxListed || l.Worlds[0].ID != last {
		t.Errorf("lobby of %d worlds: %d listed, first %d; want %d, %d", MaxListed+2, len(l.Worlds), l.Worlds[0].ID, MaxListed, last)
	}
	for i := 1; i < len(l.Worlds); i++ {
		if l.Worlds[i].ID >= l.Worlds[i-1].ID {
			t.Fatalf("not newest first at %d: %d after %d", i, l.Worlds[i].ID, l.Worlds[i-1].ID)
		}
	}
}

// brokenStore fails the lobby's lookups it is told to, like a database
// that went away.
type brokenStore struct {
	Store
	list, world, member, gm bool
}

func (b brokenStore) ListWorlds(ctx context.Context, account int64, all bool, limit int) ([]store.WorldInfo, error) {
	if b.list {
		return nil, errDisk
	}
	return b.Store.ListWorlds(ctx, account, all, limit)
}

func (b brokenStore) World(ctx context.Context, id int64) (store.World, error) {
	if b.world {
		return store.World{}, errDisk
	}
	return b.Store.World(ctx, id)
}

func (b brokenStore) Member(ctx context.Context, world, account int64) (string, error) {
	if b.member {
		return "", errDisk
	}
	return b.Store.Member(ctx, world, account)
}

func (b brokenStore) IsGM(ctx context.Context, account int64) (bool, error) {
	if b.gm {
		return false, errDisk
	}
	return b.Store.IsGM(ctx, account)
}

// A store that fails is a server error with a plain message, never an
// empty lobby, a refusal or a played world.
func TestWorldsStoreFailure(t *testing.T) {
	f := newFixture(t, Config{})
	ann := f.signUp("ann")
	made, _ := ann.create(`{"pack":"demo"}`)
	world := `{"world":` + strconv.FormatInt(made.World.ID, 10) + `}`
	for _, tt := range []struct {
		name   string
		st     brokenStore
		method string
		path   string
		body   string
		want   int
	}{
		{"lobby, list", brokenStore{list: true}, "GET", "/api/worlds", "", 500},
		{"lobby, gm", brokenStore{gm: true}, "GET", "/api/worlds", "", 500},
		{"me, gm", brokenStore{gm: true}, "GET", "/api/me", "", 500},
		{"create, gm", brokenStore{gm: true}, "POST", "/api/worlds", `{"pack":"demo"}`, 500},
		{"finish, world", brokenStore{world: true}, "POST", "/api/finish", world, 500},
		{"finish, member", brokenStore{member: true}, "POST", "/api/finish", world, 500},
		{"invite, world", brokenStore{world: true}, "POST", "/api/invites", world, 500},
		{"invite, member", brokenStore{member: true}, "POST", "/api/invites", world, 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.st.Store = f.st
			// Create is gm here, so a failing IsGM is asked.
			svc := New(Config{Store: tt.st, Registration: RegistrationSelf, Create: CreateGM, Packs: testPacks, Log: quiet})
			srv := httptest.NewServer(svc.Handler())
			defer srv.Close()
			req, _ := http.NewRequest(tt.method, srv.URL+tt.path, strings.NewReader(tt.body))
			if tt.method == "POST" {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Origin", srv.URL)
			}
			req.AddCookie(ann.cookie)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var e map[string]string
			_ = json.NewDecoder(resp.Body).Decode(&e)
			if resp.StatusCode != tt.want || strings.Contains(e["error"], "disk") {
				t.Errorf("%s %s = %d %v, want %d", tt.method, tt.path, resp.StatusCode, e, tt.want)
			}
		})
	}
	// Auth: a failing world lookup is 503, not a refusal.
	for _, st := range []brokenStore{{world: true}, {member: true}} {
		st.Store = f.st
		svc := New(Config{Store: st, Registration: RegistrationSelf, Packs: testPacks, Log: quiet})
		req := httptest.NewRequest("GET", "/ws?world="+strconv.FormatInt(made.World.ID, 10), nil)
		req.AddCookie(ann.cookie)
		if who, status := svc.Auth(req); status != 503 || who.Account != 0 {
			t.Errorf("Auth with %+v = %+v, %d; want 503", st, who, status)
		}
	}
}

// liveHub is the account Live of a hub made after the fixture.
type liveHub struct{ h *hub.Hub }

func (l *liveHub) Online(w int64) int                        { return l.h.Online(w) }
func (l *liveHub) Finish(w int64, finish func() error) error { return l.h.Finish(w, finish) }

// The lobby against a real hub: a player online shows on the card,
// ending the world sends the player away, and of many requests ending it
// at once exactly one does.
func TestLobbyWithHub(t *testing.T) {
	live := &liveHub{}
	f := newFixture(t, Config{Live: live})
	demo, err := pack.Load(content.Demo())
	if err != nil {
		t.Fatal(err)
	}
	live.h = hub.New(hub.Config{Store: f.st, Packs: map[string]*pack.Pack{"demo": demo}, Log: quiet,
		Load: func(w store.World, p *pack.Pack) (*game.World, error) {
			return game.NewWorld(p, script.DefaultLimits, quiet, game.WithStore(f.st, w.ID))
		}})
	t.Cleanup(live.h.Close)

	ann, bob := f.signUp("ann"), f.signUp("bob")
	made, _ := ann.create(`{"pack":"demo","visibility":"private"}`)
	id := made.World.ID
	bob.post("/api/join", `{"code":"`+made.Invite.Code+`"}`, nil)
	req := httptest.NewRequest("GET", f.ws(id), nil)
	req.AddCookie(bob.cookie)
	who, status := f.svc.Auth(req)
	if status != 0 || who.World != id {
		t.Fatalf("Auth = %+v, %d", who, status)
	}
	s, ms := live.h.NewSession("", who)
	if s == nil {
		t.Fatalf("Bob refused: %+v", ms)
	}
	defer s.Close()
	if c, _ := ann.card(id); c.Online != 1 || c.Players != 1 {
		t.Errorf("card with Bob in: online %d, players %d", c.Online, c.Players)
	}

	body := `{"world":` + strconv.FormatInt(id, 10) + `}`
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := &browser{f: f, cookie: ann.cookie}
			codes <- b.post("/api/finish", body, nil)
		}()
	}
	wg.Wait()
	close(codes)
	ok := 0
	for code := range codes {
		switch code {
		case 200:
			ok++
		case 404:
		default:
			t.Errorf("finish at once: %d", code)
		}
	}
	if ok != 1 {
		t.Errorf("%d of 8 finishes at once succeeded, want 1", ok)
	}
	if ms, more := s.(ws.Pusher).Pending(); more || len(ms) != 1 || ms[0].Message != hub.EndedMessage {
		t.Errorf("Bob's connection after the end: %+v, %v", ms, more)
	}
	if _, status := f.svc.Auth(req); status != 404 {
		t.Errorf("Auth to the ended world = %d", status)
	}
	if s2, ms := live.h.NewSession("", who); s2 != nil || len(ms) != 1 || ms[0].Type != "error" {
		t.Errorf("hub session in the ended world: %v %+v", s2, ms)
	}
	if live.h.Loaded() != 0 {
		t.Errorf("%d loaded after the end", live.h.Loaded())
	}
	if _, ok := ann.card(id); ok {
		t.Error("the ended world is in the lobby")
	}
}

// A Live that reports the world ending meanwhile answers 404; another
// error is a server error.
func TestFinishLiveErrors(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want int
	}{
		{store.ErrNotFound, 404},
		{errDisk, 500},
	} {
		f := newFixture(t, Config{Live: errLive{tt.err}})
		ann := f.signUp("ann")
		made, _ := ann.create(`{"pack":"demo"}`)
		if code := ann.post("/api/finish", `{"world":`+strconv.FormatInt(made.World.ID, 10)+`}`, nil); code != tt.want {
			t.Errorf("Live.Finish = %v: %d, want %d", tt.err, code, tt.want)
		}
		if _, ok := ann.card(made.World.ID); !ok {
			t.Error("the world left the lobby though it did not end")
		}
	}
}

type errLive struct{ err error }

func (errLive) Online(int64) int                   { return 0 }
func (l errLive) Finish(int64, func() error) error { return l.err }
