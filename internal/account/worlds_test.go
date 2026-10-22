package account

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// fakeLive stands in for the hub: fixed online counts, and a record of
// the worlds it ended.
type fakeLive struct {
	mu     sync.Mutex
	online map[int64]int
	ended  []int64
}

func (l *fakeLive) Online(world int64) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.online[world]
}

func (l *fakeLive) Finish(world int64, finish func() error) error {
	l.mu.Lock()
	l.ended = append(l.ended, world)
	l.mu.Unlock()
	return finish()
}

// create posts a new world and returns the answer.
func (b *browser) create(body string) (Created, int) {
	b.f.t.Helper()
	var c Created
	code := b.post("/api/worlds", body, &c)
	return c, code
}

// A player creates a private world, gets its join code, and another player
// joins with it; a public world is in everyone's lobby.
func TestCreateWorldAndJoin(t *testing.T) {
	live := &fakeLive{online: map[int64]int{}}
	f := newFixture(t, Config{Live: live})
	ann, bob, cat := f.signUp("ann"), f.signUp("bob"), f.signUp("cat")

	made, code := ann.create(`{"pack":"test-pack","title":"  Our ‮  game ","visibility":"private"}`)
	w := made.World
	if code != 200 || w.ID == 0 || w.Title != "Our game" || w.Pack != "test-pack" || w.PackTitle != "Test pack" || w.Visibility != "private" ||
		w.GM != "ann" || w.Role != "gm" || !w.Play || !w.Invite || !w.Finish || w.Main || !w.Created.Equal(w.Created.UTC()) {
		t.Fatalf("create = %d %+v", code, made)
	}
	inv := made.Invite
	if len(inv.Code) != 11 || inv.Uses != JoinCodeUses || !inv.Expires.Equal(f.now.Add(defaultInviteTTL)) || inv.GM {
		t.Errorf("join code = %+v", inv)
	}
	if c, ok := ann.card(w.ID); !ok || c != w {
		t.Errorf("Ann's lobby card = %+v, want %+v", c, w)
	}
	if code := ann.get(f.ws(w.ID), nil); code != 200 {
		t.Errorf("creator's ws = %d", code)
	}

	// Bob does not see the private world, nor play it, until he has its code.
	if c, ok := bob.card(w.ID); ok {
		t.Errorf("a private world in another player's lobby: %+v", c)
	}
	if code := bob.get(f.ws(w.ID), nil); code != 404 { // as if it did not exist
		t.Errorf("ws without the code = %d", code)
	}
	var me Me
	if code := bob.post("/api/join", `{"code":"`+inv.Code+`"}`, &me); code != 200 || me.Joined != w.ID {
		t.Fatalf("join = %d %+v", code, me)
	}
	live.online[w.ID] = 2
	c, ok := bob.card(w.ID)
	if !ok || c.Role != "player" || !c.Play || c.Invite || c.Finish || c.GM != "ann" || c.Online != 2 {
		t.Errorf("Bob's card after joining = %+v, %v", c, ok)
	}
	if code := bob.get(f.ws(w.ID), nil); code != 200 {
		t.Errorf("ws with the code = %d", code)
	}
	var e map[string]string
	if code := bob.post("/api/invites", `{"world":`+strconv.FormatInt(w.ID, 10)+`}`, &e); code != 403 {
		t.Errorf("a player of the world made a code: %d %v", code, e)
	}

	// A public world needs no code; an empty title shows the pack's.
	pub, code := ann.create(`{"pack":"demo","visibility":"public"}`)
	if code != 200 || pub.World.Title != "The damp cell" || pub.World.Visibility != "public" {
		t.Fatalf("public create = %d %+v", code, pub)
	}
	if c, ok := cat.card(pub.World.ID); !ok || !c.Play || c.Role != "" || c.Invite || c.GM != "ann" {
		t.Errorf("Cat's card of the public world = %+v, %v", c, ok)
	}
	if code := cat.get(f.ws(pub.World.ID), nil); code != 200 {
		t.Errorf("ws to a public world = %d", code)
	}
	// Ann's lobby: her two worlds and the main world, the newest first.
	var ids []int64
	for _, c := range ann.lobby().Worlds {
		ids = append(ids, c.ID)
	}
	if len(ids) != 3 || ids[0] != pub.World.ID || ids[1] != w.ID || ids[2] != f.world.ID {
		t.Errorf("Ann's lobby = %v", ids)
	}
	if l := cat.lobby(); len(l.Packs) != 2 || l.Packs[0].ID != "demo" {
		t.Errorf("packs = %+v", l.Packs)
	}
}

// Creating refuses what it does not understand, and past its limits.
func TestCreateWorldRefuses(t *testing.T) {
	f := newFixture(t, Config{WorldsPerAccount: 2, MaxWorlds: 5})
	ann := f.signUp("ann")
	var e map[string]string
	for _, body := range []string{
		`{"pack":"nope"}`,
		`{}`,
		`{"pack":"demo","visibility":"secret"}`,
		`{"pack":"demo","title":"` + strings.Repeat("x", MaxWorldTitle+1) + `"}`,
		`{"pack":"demo","title":"two\nlines"}`,
		`{"pack":"demo","title":"\xff"}`,
		`{"pack":"demo","gm":"bob"}`,
		`not json`,
	} {
		var raw map[string]any
		if code := ann.post("/api/worlds", body, &raw); code != 400 {
			t.Errorf("%.50s: %d %v", body, code, raw)
		}
	}
	if code := f.browser().post("/api/worlds", `{"pack":"demo"}`, &e); code != 401 {
		t.Errorf("signed out: %d", code)
	}
	if code := f.browser().get("/api/worlds", &e); code != 401 {
		t.Errorf("lobby signed out: %d", code)
	}
	if n := len(ann.lobby().Worlds); n != 1 {
		t.Fatalf("refused requests made worlds: %d in the lobby", n)
	}
	// A title of exactly the longest length is fine, in characters.
	if _, code := ann.create(`{"pack":"demo","title":"` + strings.Repeat("ö", MaxWorldTitle) + `"}`); code != 200 {
		t.Errorf("longest title: %d", code)
	}
	if _, code := ann.create(`{"pack":"demo"}`); code != 200 {
		t.Errorf("second world: %d", code)
	}
	if code := ann.post("/api/worlds", `{"pack":"demo"}`, &e); code != 409 || !strings.Contains(e["error"], "2 worlds") {
		t.Errorf("third world, limit 2: %d %v", code, e)
	}
	// The server's limit: the main world and Ann's two count; Bob gets two
	// more, Cat none.
	bob, cat := f.signUp("bob"), f.signUp("cat")
	for range 2 {
		if _, code := bob.create(`{"pack":"demo"}`); code != 200 {
			t.Fatalf("Bob's world: %d", code)
		}
	}
	if code := cat.post("/api/worlds", `{"pack":"demo"}`, &e); code != 503 {
		t.Errorf("past the server's limit: %d %v", code, e)
	}
	// Ending a world makes room again.
	w := ann.lobby().Worlds[0]
	if code := ann.post("/api/finish", `{"world":`+strconv.FormatInt(w.ID, 10)+`}`, nil); code != 200 {
		t.Fatalf("finish = %d", code)
	}
	if _, code := cat.create(`{"pack":"demo"}`); code != 200 {
		t.Errorf("after one ended: %d", code)
	}
}

// One account creating world after world is slowed down.
func TestCreateWorldRateLimit(t *testing.T) {
	f := newFixture(t, Config{WorldsPerAccount: 100})
	ann := f.signUp("ann")
	limited := 0
	for range 6 {
		if _, code := ann.create(`{"pack":"demo"}`); code == 429 {
			limited++
		}
	}
	if limited != 3 {
		t.Errorf("6 worlds at once: %d limited, want 3", limited)
	}
	f.now = f.now.Add(10 * time.Minute)
	if _, code := ann.create(`{"pack":"demo"}`); code != 200 {
		t.Errorf("10 minutes later: %d", code)
	}
}

// With TALESPEAK_CREATE_WORLDS=gm only admins and game masters create
// worlds.
func TestCreateWorldGMOnly(t *testing.T) {
	f := newFixture(t, Config{Create: CreateGM})
	kid := f.signUp("kid")
	var me Me
	if kid.get("/api/me", &me); me.Create {
		t.Errorf("a player may create: %+v", me)
	}
	if l := kid.lobby(); len(l.Packs) != 0 {
		t.Errorf("a player who may not create sees packs: %+v", l.Packs)
	}
	var e map[string]string
	if code := kid.post("/api/worlds", `{"pack":"demo"}`, &e); code != 403 {
		t.Errorf("player creates: %d %v", code, e)
	}
	admin := f.admin("teacher")
	made, code := admin.create(`{"pack":"demo","visibility":"public"}`)
	if code != 200 {
		t.Fatalf("admin creates: %d", code)
	}
	// An admin has no per-account limit.
	for range 2 {
		f.now = f.now.Add(time.Hour)
		if _, code := admin.create(`{"pack":"demo"}`); code != 200 {
			t.Errorf("admin's next world: %d", code)
		}
	}
	// A game master of any world may create.
	var inv InviteAnswer
	admin.post("/api/invites", `{"world":`+strconv.FormatInt(made.World.ID, 10)+`,"gm":true}`, &inv)
	teacher := f.signUp("mrs_h")
	teacher.post("/api/join", `{"code":"`+inv.Code+`"}`, nil)
	if teacher.get("/api/me", &me); !me.Create {
		t.Errorf("a game master may not create: %+v", me)
	}
	if _, code := teacher.create(`{"pack":"test-pack"}`); code != 200 {
		t.Errorf("game master creates: %d", code)
	}
}

// Edu mode: worlds are always private, listed to their members only, and
// only admins and game masters create them, whatever Create says.
func TestEduWorlds(t *testing.T) {
	f := newInviteFixture(t, Config{Edu: true, Create: CreateEveryone, Registration: RegistrationSelf})
	ctx := context.Background()
	kid := f.signUp("kid")
	var me Me
	if kid.get("/api/me", &me); me.Mode != "edu" || me.Create {
		t.Errorf("edu me = %+v", me)
	}
	if l := kid.lobby(); len(l.Worlds) != 0 || len(l.Packs) != 0 {
		t.Errorf("a new account's edu lobby = %+v", l)
	}
	if code := kid.post("/api/worlds", `{"pack":"demo","visibility":"public"}`, nil); code != 403 {
		t.Errorf("kid creates in edu mode: %d", code)
	}
	admin := f.admin("teacher")
	made, code := admin.create(`{"pack":"demo","visibility":"public"}`)
	if code != 200 || made.World.Visibility != "private" {
		t.Fatalf("admin creates a public world in edu mode: %d %+v", code, made.World)
	}
	if w, _ := f.st.World(ctx, made.World.ID); w.Visibility != store.VisibilityPrivate {
		t.Errorf("stored visibility = %q", w.Visibility)
	}
	// A world left public from before edu mode is still members only.
	k, _, _ := f.st.AccountByName(ctx, "kid")
	old, err := f.st.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPublic, CreatedBy: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	other := f.signUp("other_kid")
	if c, ok := other.card(old.ID); ok {
		t.Errorf("an old public world in an edu lobby: %+v", c)
	}
	if code := other.get(f.ws(old.ID), nil); code != 404 {
		t.Errorf("ws to an old public world in edu mode = %d", code)
	}
	if c, ok := admin.card(old.ID); !ok || c.Visibility != "private" || !c.Play {
		t.Errorf("admin's card of it = %+v, %v", c, ok)
	}
}

// Its game masters and admins end a world: it leaves the lobby, nobody
// plays it or joins it with its codes; the main world cannot end here.
func TestFinishWorld(t *testing.T) {
	live := &fakeLive{online: map[int64]int{}}
	f := newFixture(t, Config{Live: live})
	ann, bob := f.signUp("ann"), f.signUp("bob")
	made, _ := ann.create(`{"pack":"demo","visibility":"private"}`)
	id := strconv.FormatInt(made.World.ID, 10)
	bob.post("/api/join", `{"code":"`+made.Invite.Code+`"}`, nil)
	var inv InviteAnswer
	ann.post("/api/invites", `{"world":`+id+`,"uses":5}`, &inv)

	var e map[string]string
	if code := bob.post("/api/finish", `{"world":`+id+`}`, &e); code != 403 {
		t.Errorf("a player ends the world: %d %v", code, e)
	}
	var l Lobby
	if code := ann.post("/api/finish", `{"world":`+id+`}`, &l); code != 200 || len(l.Worlds) != 1 || l.Worlds[0].ID != f.world.ID {
		t.Fatalf("finish = %d %+v", code, l)
	}
	if len(live.ended) != 1 || live.ended[0] != made.World.ID {
		t.Errorf("worlds ended live: %v", live.ended)
	}
	if code := ann.post("/api/finish", `{"world":`+id+`}`, &e); code != 404 {
		t.Errorf("finish twice = %d %v", code, e)
	}
	if code := bob.get(f.ws(made.World.ID), nil); code != 404 {
		t.Errorf("ws to an ended world = %d", code)
	}
	if code := ann.post("/api/invites", `{"world":`+id+`}`, &e); code != 404 {
		t.Errorf("invite to an ended world = %d %v", code, e)
	}
	cat := f.signUp("cat")
	if code := cat.post("/api/join", `{"code":"`+inv.Code+`"}`, &e); code != 400 {
		t.Errorf("a code of an ended world = %d %v", code, e)
	}

	admin := f.admin("root")
	home := strconv.FormatInt(f.world.ID, 10)
	if code := admin.post("/api/finish", `{"world":`+home+`}`, &e); code != 403 {
		t.Errorf("admin ends the main world: %d %v", code, e)
	}
	other, _ := bob.create(`{"pack":"demo"}`)
	if code := admin.post("/api/finish", `{"world":`+strconv.FormatInt(other.World.ID, 10)+`}`, nil); code != 200 {
		t.Errorf("admin ends a player's world: %d", code)
	}
	for _, body := range []string{`{}`, `{"world":0}`, `{"world":9999}`, `{"world":"1"}`} {
		if code := admin.post("/api/finish", body, nil); code != 404 && code != 400 {
			t.Errorf("finish %s = %d", body, code)
		}
	}
}

// A world whose pack the server no longer has stays in the lobby, but
// nobody can play it.
func TestWorldWithoutPack(t *testing.T) {
	f := newFixture(t, Config{})
	ctx := context.Background()
	ann := f.signUp("ann")
	a, _, _ := f.st.AccountByName(ctx, "ann")
	w, err := f.st.CreateWorld(ctx, store.NewWorld{PackID: "removed", Visibility: store.VisibilityPublic, CreatedBy: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	c, ok := ann.card(w.ID)
	if !ok || c.Play || c.PackTitle != "removed" || c.Title != "removed" || !c.Finish {
		t.Errorf("card = %+v, %v", c, ok)
	}
	if code := ann.get(f.ws(w.ID), nil); code != 403 {
		t.Errorf("ws = %d", code)
	}
}

// A main world of a pack the server played before is not the main world
// any more: admins end it from the lobby.
func TestFormerMainWorld(t *testing.T) {
	f := newFixture(t, Config{})
	ctx := context.Background()
	now, err := f.st.OpenWorld(ctx, "test-pack", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.cfg.Home = now.ID // the server plays test-pack now
	admin := f.admin("root")
	if c, ok := admin.card(f.world.ID); !ok || c.Main || !c.Finish {
		t.Errorf("former main world's card = %+v, %v", c, ok)
	}
	if c, ok := admin.card(now.ID); !ok || !c.Main || c.Finish {
		t.Errorf("main world's card = %+v, %v", c, ok)
	}
	if code := admin.post("/api/finish", `{"world":`+strconv.FormatInt(f.world.ID, 10)+`}`, nil); code != 200 {
		t.Errorf("admin ends the former main world: %d", code)
	}
}

// Join codes are limited per account and per world.
func TestCodeLimits(t *testing.T) {
	f := newFixture(t, Config{})
	ann := f.signUp("ann")
	made, _ := ann.create(`{"pack":"demo"}`)
	body := `{"world":` + strconv.FormatInt(made.World.ID, 10) + `}`
	n := 0
	for ; n < 30; n++ {
		if code := ann.post("/api/invites", body, nil); code != 200 {
			if code != 429 {
				t.Fatalf("code %d = %d", n, code)
			}
			break
		}
	}
	if n != 20 {
		t.Errorf("%d codes at once, want 20", n)
	}
	// The world has 21 codes now; one a minute after that, up to
	// MaxLiveInvites.
	var e map[string]any
	for range store.MaxLiveInvites {
		f.now = f.now.Add(time.Minute) // one more code a minute
		if code := ann.post("/api/invites", body, &e); code != 200 {
			if code != 409 {
				t.Fatalf("past the cap = %d %v", code, e)
			}
			break
		}
		n++
	}
	if n+1 != store.MaxLiveInvites {
		t.Errorf("%d codes before the cap, want %d", n+1, store.MaxLiveInvites)
	}
}

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"":                          "",
		"  Our   game ":             "Our game",
		"a\tb":                      "a b",
		"x‮y​z⁦":                    "xyz",
		" lead":                     "lead",
		strings.Repeat("ab", 20):    strings.Repeat("ab", 20),
		"Cafe\u0301 e\u0301\u0300":  "Cafe\u0301 e\u0301\u0300",
		"Z\u0301\u0302\u0303\u0304": "Z\u0301\u0302", // stacked marks draw over other cards
		"\u0301\u0301lead":          "lead",
		"a \u0301b":                 "a b",
		"\u3164\u2800 x\u3164y":     "x y", // blank-looking letters are spaces
		"\u115F\u1160\uFFA0":        "",
	} {
		if got, ok := cleanTitle(in); !ok || got != want {
			t.Errorf("cleanTitle(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"a\nb", "a\rb", strings.Repeat("x", MaxWorldTitle+1), "\xff", strings.Repeat(" ", 4*MaxWorldTitle+1)} {
		if got, ok := cleanTitle(bad); ok {
			t.Errorf("cleanTitle(%.20q) = %q, ok", bad, got)
		}
	}
}
