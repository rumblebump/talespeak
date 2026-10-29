package account

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fixture struct {
	t     *testing.T
	st    *sqlite.Store
	world store.World
	svc   *Service
	srv   *httptest.Server
	now   time.Time
}

// testPacks are the packs of a fixture.
var testPacks = []Pack{{ID: "demo", Title: "The damp cell", Description: "A cell."}, {ID: "test-pack", Title: "Test pack"}}

// newFixture serves the account API with a public main world of the demo
// pack (f.world).
func newFixture(t *testing.T, cfg Config) *fixture {
	t.Helper()
	return newFixtureVisibility(t, cfg, store.VisibilityPublic)
}

// newInviteFixture is newFixture with an invite-only (private) main world.
func newInviteFixture(t *testing.T, cfg Config) *fixture {
	t.Helper()
	return newFixtureVisibility(t, cfg, store.VisibilityPrivate)
}

func newFixtureVisibility(t *testing.T, cfg Config, visibility string) *fixture {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: real SQLite file")
	}
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w, err := st.OpenWorld(ctx, "demo", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetVisibility(ctx, w.ID, visibility); err != nil {
		t.Fatal(err)
	}
	w.Visibility = visibility
	f := &fixture{t: t, st: st, world: w, now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	cfg.Store, cfg.Log, cfg.Now = st, quiet, func() time.Time { return f.now }
	if cfg.Home == 0 {
		cfg.Home = w.ID
	}
	if cfg.Registration == "" {
		cfg.Registration = RegistrationSelf
	}
	if cfg.Create == "" {
		cfg.Create = CreateEveryone
	}
	if cfg.Packs == nil {
		cfg.Packs = testPacks
	}
	f.svc = New(cfg)
	mux := http.NewServeMux()
	mux.Handle("/api/", f.svc.Handler())
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		who, status := f.svc.Auth(r)
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(who)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// browser is one browser: it keeps its session cookie.
type browser struct {
	f      *fixture
	cookie *http.Cookie
}

func (f *fixture) browser() *browser { return &browser{f: f} }

// post sends body as JSON from the server's own origin and decodes the
// answer into out (if not nil). It returns the status.
func (b *browser) post(path, body string, out any) int {
	b.f.t.Helper()
	req, _ := http.NewRequest("POST", b.f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.f.srv.URL)
	return b.do(req, out)
}

func (b *browser) get(path string, out any) int {
	b.f.t.Helper()
	req, _ := http.NewRequest("GET", b.f.srv.URL+path, nil)
	return b.do(req, out)
}

func (b *browser) do(req *http.Request, out any) int {
	b.f.t.Helper()
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.f.t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == CookieName {
			if c.MaxAge < 0 {
				b.cookie = nil
			} else {
				b.cookie = c
			}
		}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			b.f.t.Fatalf("%s: %v", req.URL.Path, err)
		}
	}
	return resp.StatusCode
}

// ws is the WebSocket path to world (the main world when 0).
func (f *fixture) ws(world int64) string {
	if world == 0 {
		world = f.world.ID
	}
	return "/ws?world=" + strconv.FormatInt(world, 10)
}

// lobby returns what the browser's lobby shows.
func (b *browser) lobby() Lobby {
	b.f.t.Helper()
	var l Lobby
	if code := b.get("/api/worlds", &l); code != 200 {
		b.f.t.Fatalf("GET /api/worlds = %d", code)
	}
	return l
}

// card returns the browser's card of world (the main world when 0), and
// whether the lobby shows it.
func (b *browser) card(world int64) (Card, bool) {
	b.f.t.Helper()
	if world == 0 {
		world = b.f.world.ID
	}
	for _, c := range b.lobby().Worlds {
		if c.ID == world {
			return c, true
		}
	}
	return Card{}, false
}

// signUp registers a new player on a fresh browser.
func (f *fixture) signUp(name string) *browser {
	f.t.Helper()
	b := f.browser()
	if code := b.post("/api/register", `{"name":"`+name+`","password":"correct horse"}`, nil); code != 200 {
		f.t.Fatalf("register %s: %d", name, code)
	}
	return b
}

func (f *fixture) admin(name string) *browser {
	f.t.Helper()
	hash, err := Hash(context.Background(), "admin password")
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.CreateAccount(context.Background(), name, hash, store.RoleAdmin); err != nil {
		f.t.Fatal(err)
	}
	b := f.browser()
	if code := b.post("/api/login", `{"name":"`+name+`","password":"admin password"}`, nil); code != 200 {
		f.t.Fatalf("admin login: %d", code)
	}
	return b
}

func TestRegisterSignInAndOut(t *testing.T) {
	f := newFixture(t, Config{})
	b := f.browser()
	var me Me
	if code := b.get("/api/me", &me); code != 200 || me.Account != nil || me.Registration != "self" || me.Mode != "public" || me.Create {
		t.Fatalf("signed out me = %d %+v", code, me)
	}
	if code := b.post("/api/register", `{"name":"Mira","password":"correct horse"}`, &me); code != 200 || me.Account == nil || me.Account.Name != "Mira" || !me.Create {
		t.Fatalf("register = %d %+v", code, me)
	}
	if c, ok := b.card(0); !ok || !c.Play || c.Invite || c.Finish || !c.Main || c.Role != "" || c.Title != "The damp cell" || c.Description != "A cell." {
		t.Fatalf("main world's card = %+v, %v", c, ok)
	}
	c := b.cookie
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure || c.Path != "/" {
		t.Errorf("cookie = %+v", c)
	}
	if code := b.get(f.ws(0), nil); code != 200 {
		t.Errorf("ws auth after register = %d", code)
	}

	other := f.browser()
	var e map[string]string
	if code := other.post("/api/register", `{"name":"mIRA","password":"another one"}`, &e); code != 409 || !strings.Contains(e["error"], "taken") {
		t.Errorf("same name in other case = %d %v", code, e)
	}
	if code := other.post("/api/login", `{"name":"mira","password":"wrong password"}`, &e); code != 401 || other.cookie != nil {
		t.Errorf("wrong password = %d %v", code, e)
	}
	if code := other.post("/api/login", `{"name":"nobody","password":"wrong password"}`, &e); code != 401 || e["error"] != errWrongLogin.Error() {
		t.Errorf("unknown name = %d %v", code, e)
	}
	if code := other.post("/api/login", `{"name":"mira","password":"correct horse"}`, &me); code != 200 || me.Account.Name != "Mira" || other.cookie == nil {
		t.Errorf("login = %d %+v", code, me)
	}

	if code := b.post("/api/logout", `{}`, &me); code != 200 || me.Account != nil || b.cookie != nil {
		t.Errorf("logout = %d %+v", code, me)
	}
	b.cookie = c // the old cookie no longer works
	if code := b.get(f.ws(0), nil); code != 401 {
		t.Errorf("ws auth after logout = %d", code)
	}
	if code := other.get(f.ws(0), nil); code != 200 {
		t.Errorf("the other session = %d", code)
	}
}

func TestRegisterRefuses(t *testing.T) {
	f := newFixture(t, Config{})
	b := f.browser()
	for _, body := range []string{
		`{"name":"ab","password":"correct horse"}`,
		`{"name":"1mira","password":"correct horse"}`,
		`{"name":"mi ra","password":"correct horse"}`,
		`{"name":"mira","password":"short"}`,
		`{"name":"mira","password":"` + strings.Repeat("x", MaxPassword+1) + `"}`,
		`{"name":"mira","password":"correct horse","admin":true}`,
		`{"name":"GameMaster","password":"correct horse"}`, // staff names (script.ReservedName)
		`{"name":"Admin_2","password":"correct horse"}`,
		`{"name":"teacher","password":"correct horse"}`,
		`not json`,
	} {
		if code := b.post("/api/register", body, nil); code != 400 || b.cookie != nil {
			t.Errorf("%.60s: %d", body, code)
		}
	}

	off := newFixture(t, Config{Registration: RegistrationOff})
	var e map[string]string
	if code := off.browser().post("/api/register", `{"name":"mira","password":"correct horse"}`, &e); code != 403 {
		t.Errorf("registration off: %d %v", code, e)
	}
}

// Requests from another site, or not as JSON, are refused before they do
// anything.
func TestCrossSiteRefused(t *testing.T) {
	f := newFixture(t, Config{})
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"other origin", map[string]string{"Origin": "https://evil.example", "Content-Type": "application/json"}, 403},
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site", "Content-Type": "application/json"}, 403},
		{"same-site fetch", map[string]string{"Sec-Fetch-Site": "same-site", "Content-Type": "application/json"}, 403},
		{"form", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, 415},
		{"plain text", map[string]string{"Content-Type": "text/plain"}, 415},
	} {
		req, _ := http.NewRequest("POST", f.srv.URL+"/api/register", strings.NewReader(`{"name":"mira","password":"correct horse"}`))
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want || len(resp.Cookies()) != 0 {
			t.Errorf("%s: %d, cookies %v", tc.name, resp.StatusCode, resp.Cookies())
		}
	}
	if _, _, err := f.st.AccountByName(context.Background(), "mira"); err == nil {
		t.Error("a refused request made an account")
	}
}

// An invite-only world: registering lets no one in; an invite does.
func TestInviteOnlyWorld(t *testing.T) {
	f := newInviteFixture(t, Config{})
	kid := f.browser()
	var me Me
	if code := kid.post("/api/register", `{"name":"kid","password":"correct horse"}`, &me); code != 200 {
		t.Fatalf("register = %d %+v", code, me)
	}
	if c, ok := kid.card(0); ok {
		t.Fatalf("a private world in the lobby of a new account: %+v", c)
	}
	if code := kid.get(f.ws(0), nil); code != 404 { // as if it did not exist
		t.Errorf("ws auth before the invite = %d", code)
	}
	var e map[string]string
	home := `"world":` + strconv.FormatInt(f.world.ID, 10)
	if code := kid.post("/api/invites", `{`+home+`}`, &e); code != 404 {
		t.Errorf("a player made an invite: %d %v", code, e)
	}

	admin := f.admin("teacher")
	var inv InviteAnswer
	if code := admin.post("/api/invites", `{"uses":1,"days":2,`+home+`}`, &inv); code != 200 || len(inv.Code) != 11 || inv.Uses != 1 || !inv.Expires.Equal(f.now.Add(48*time.Hour)) {
		t.Fatalf("invite = %d %+v", code, inv)
	}
	typed := strings.ToLower(strings.ReplaceAll(inv.Code, "-", " "))
	if code := kid.post("/api/join", `{"code":"`+typed+`"}`, &me); code != 200 || me.Joined != f.world.ID {
		t.Fatalf("join = %d %+v", code, me)
	}
	if c, ok := kid.card(0); !ok || !c.Play || c.Role != "player" || c.Invite {
		t.Fatalf("card after joining = %+v, %v", c, ok)
	}
	if code := kid.get(f.ws(0), nil); code != 200 {
		t.Errorf("ws auth after the invite = %d", code)
	}

	// Used up: one use only.
	other := f.browser()
	other.post("/api/register", `{"name":"other","password":"correct horse"}`, nil)
	if code := other.post("/api/join", `{"code":"`+inv.Code+`"}`, &e); code != 400 {
		t.Errorf("used up code = %d %v", code, e)
	}
	// Expired.
	admin.post("/api/invites", `{"days":1,`+home+`}`, &inv)
	f.now = f.now.Add(25 * time.Hour)
	if code := other.post("/api/join", `{"code":"`+inv.Code+`"}`, &e); code != 400 {
		t.Errorf("expired code = %d %v", code, e)
	}
	if code := other.post("/api/join", `{"code":"not a code"}`, &e); code != 400 {
		t.Errorf("bad code = %d %v", code, e)
	}
	// A GM invite, from an admin only; a GM then makes player invites.
	if code := admin.post("/api/invites", `{"gm":true,`+home+`}`, &inv); code != 200 || !inv.GM {
		t.Fatalf("gm invite = %d %+v", code, inv)
	}
	if code := kid.post("/api/invites", `{"gm":true,`+home+`}`, &e); code != 403 {
		t.Errorf("a player made a gm invite: %d", code)
	}
	if code := other.post("/api/join", `{"code":"`+inv.Code+`"}`, &me); code != 200 || me.Joined != f.world.ID {
		t.Fatalf("gm join = %d %+v", code, me)
	}
	if c, _ := other.card(0); c.Role != "gm" || !c.Invite || c.Finish {
		t.Fatalf("gm's card of the main world = %+v", c)
	}
	if code := other.post("/api/invites", `{"uses":100,`+home+`}`, &inv); code != 200 {
		t.Errorf("gm invite for players = %d", code)
	}
	// Outside edu mode a world's GM makes GMs too (edu: edu_floor_test.go).
	if code := other.post("/api/invites", `{"gm":true,`+home+`}`, &inv); code != 200 || !inv.GM {
		t.Errorf("a gm's gm invite in public mode = %d %+v", code, inv)
	}
	for _, body := range []string{`{"uses":101}`, `{"uses":-1}`, `{"days":31}`, `{"days":-1}`, `{}`, `{"world":0}`, `{"world":9999}`} {
		if !strings.Contains(body, "world") && body != "{}" {
			body = strings.Replace(body, "{", "{"+home+",", 1)
		}
		if code := admin.post("/api/invites", body, nil); code != 400 && code != 404 {
			t.Errorf("%s: %d", body, code)
		}
	}
}

// Guessing invite codes and passwords is slowed down.
func TestRateLimits(t *testing.T) {
	f := newInviteFixture(t, Config{})
	b := f.browser()
	b.post("/api/register", `{"name":"guesser","password":"correct horse"}`, nil)
	limited := 0
	for range 20 {
		if b.post("/api/join", `{"code":"AAAAA-AAAAA"}`, nil) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("20 wrong invite codes from one account: never limited")
	}
	limited = 0
	for range 20 {
		if f.browser().post("/api/login", `{"name":"guesser","password":"wrong password"}`, nil) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("20 wrong passwords for one name: never limited")
	}
}

func TestHashVerify(t *testing.T) {
	ctx := context.Background()
	h, err := Hash(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q", h)
	}
	if h2, _ := Hash(ctx, "correct horse"); h2 == h {
		t.Error("two hashes of one password are the same: no salt")
	}
	for pw, want := range map[string]bool{"correct horse": true, "correct horsE": false, "": false} {
		if ok, err := Verify(ctx, h, pw); ok != want || err != nil {
			t.Errorf("Verify(%q) = %v, %v", pw, ok, err)
		}
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=4194304,t=2,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=19456,t=99,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5a2V5"} {
		if _, err := Verify(ctx, bad, "x"); err == nil {
			t.Errorf("Verify accepted hash %q", bad)
		}
	}
}

func TestNormalizeCode(t *testing.T) {
	for in, want := range map[string]string{
		"ABCDE-FGHJK":   "ABCDEFGHJK",
		"abcde fghjk":   "ABCDEFGHJK",
		"0OIl1-22222":   "00111" + "22222",
		"ABCDE":         "",
		"ABCDE-FGHJK-1": "",
		"ABCDU-FGHJK":   "", // no U in the alphabet
	} {
		got, ok := normalizeCode(in)
		if got != want || ok != (want != "") {
			t.Errorf("normalizeCode(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}
