package account

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// testIP is the header the fixtures' ClientIP reads, so one test can be
// several client addresses.
const testIP = "X-Test-IP"

func headerIP(r *http.Request) string {
	if ip := r.Header.Get(testIP); ip != "" {
		return ip
	}
	return "192.0.2.1"
}

// postFrom is post from another client address.
func (b *browser) postFrom(ip, path, body string, out any) int {
	b.f.t.Helper()
	req, _ := http.NewRequest("POST", b.f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.f.srv.URL)
	req.Header.Set(testIP, ip)
	return b.do(req, out)
}

func (f *fixture) player(name string) *browser {
	f.t.Helper()
	b := f.browser()
	if code := b.post("/api/register", `{"name":"`+name+`","password":"correct horse"}`, nil); code != 200 {
		f.t.Fatalf("register %s: %d", name, code)
	}
	return b
}

// Over HTTPS the cookie is Secure and named with the __Host- prefix, and
// so is the one that signs out; the plain-HTTP name is not read there.
func TestCookieSecureOverHTTPS(t *testing.T) {
	f := newFixture(t, Config{HTTPS: func(*http.Request) bool { return true }})
	send := func(path string, c *http.Cookie) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", f.srv.URL+path, strings.NewReader(`{"name":"Mira","password":"correct horse"}`))
		req.Header.Set("Content-Type", "application/json")
		if c != nil {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	resp := send("/api/register", nil)
	cs := resp.Cookies()
	if resp.StatusCode != 200 || len(cs) != 1 {
		t.Fatalf("register = %d, cookies %v", resp.StatusCode, cs)
	}
	c := cs[0]
	if c.Name != SecureCookieName || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode ||
		c.Path != "/" || c.Domain != "" || c.MaxAge != SessionDays*24*60*60 {
		t.Fatalf("cookie = %+v", c)
	}
	if len(c.Value) != base64.RawURLEncoding.EncodedLen(32) {
		t.Errorf("token = %d characters, want 32 random bytes", len(c.Value))
	}
	req := httptest.NewRequest("GET", "/ws", nil)
	req.AddCookie(c)
	if _, status := f.svc.Auth(req); status != 0 {
		t.Errorf("Auth with the __Host- cookie = %d", status)
	}
	plain := &http.Cookie{Name: CookieName, Value: c.Value}
	req = httptest.NewRequest("GET", "/ws", nil)
	req.AddCookie(plain)
	if _, status := f.svc.Auth(req); status != 401 {
		t.Errorf("Auth over HTTPS with the plain cookie name = %d, want 401", status)
	}

	resp = send("/api/logout", c)
	cs = resp.Cookies()
	if len(cs) != 1 || cs[0].Name != SecureCookieName || cs[0].MaxAge >= 0 || !cs[0].Secure || cs[0].Value != "" {
		t.Errorf("sign-out cookie = %+v", cs)
	}
	req = httptest.NewRequest("GET", "/ws", nil)
	req.AddCookie(c)
	if _, status := f.svc.Auth(req); status != 401 {
		t.Errorf("Auth after sign-out = %d", status)
	}
}

// Only the token's hash is in the database.
func TestSessionStoresOnlyHash(t *testing.T) {
	f := newFixture(t, Config{})
	b := f.player("Mira")
	raw, err := base64.RawURLEncoding.DecodeString(b.cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if _, err := f.st.LoginAccount(context.Background(), sum[:], f.now); err != nil {
		t.Errorf("no session under the token's hash: %v", err)
	}
	if _, err := f.st.LoginAccount(context.Background(), raw, f.now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the raw token is stored: %v", err)
	}
}

// Sessions last SessionDays; a password change ends them.
func TestSessionExpiryAndPasswordChange(t *testing.T) {
	f := newFixture(t, Config{})
	b := f.player("Mira")
	f.now = f.now.Add(SessionDays*24*time.Hour - time.Second)
	if code := b.get("/ws", nil); code != 200 {
		t.Errorf("a second before the end: %d", code)
	}
	f.now = f.now.Add(time.Second)
	var me Me
	if code := b.get("/api/me", &me); code != 200 || me.Account != nil {
		t.Errorf("after %d days: me = %d %+v, want signed out", SessionDays, code, me)
	}
	if code := b.get("/ws", nil); code != 401 {
		t.Errorf("ws after expiry = %d", code)
	}

	f.now = f.now.Add(-SessionDays * 24 * time.Hour)
	b2 := f.browser()
	b2.post("/api/login", `{"name":"Mira","password":"correct horse"}`, nil)
	a, _, _ := f.st.AccountByName(context.Background(), "Mira")
	if err := f.st.SetPassword(context.Background(), a.ID, "x"); err != nil {
		t.Fatal(err)
	}
	if code := b2.get("/ws", nil); code != 401 {
		t.Errorf("ws after a password change = %d", code)
	}
}

// At most MaxSessions per account: signing in once more ends the oldest.
func TestMaxSessions(t *testing.T) {
	f := newFixture(t, Config{})
	first := f.player("Mira")
	var bs []*browser
	for i := range MaxSessions {
		f.now = f.now.Add(time.Minute) // the name limit lets 10 a minute through; and the oldest is clear
		b := f.browser()
		if code := b.post("/api/login", `{"name":"mira","password":"correct horse"}`, nil); code != 200 {
			t.Fatalf("login %d: %d", i, code)
		}
		bs = append(bs, b)
	}
	if code := first.get("/ws", nil); code != 401 {
		t.Errorf("the oldest of %d sessions = %d, want ended", MaxSessions+1, code)
	}
	for i, b := range bs {
		if code := b.get("/ws", nil); code != 200 {
			t.Errorf("session %d = %d", i, code)
		}
	}
}

// Bad cookies are no sign-in, never an error.
func TestBadCookies(t *testing.T) {
	f := newFixture(t, Config{})
	for _, v := range []string{"", "x", "not base64 !!", base64.RawURLEncoding.EncodeToString(make([]byte, 31)),
		base64.RawURLEncoding.EncodeToString(make([]byte, 32)), strings.Repeat("A", 4000)} {
		b := f.browser()
		b.cookie = &http.Cookie{Name: CookieName, Value: v}
		var me Me
		if code := b.get("/api/me", &me); code != 200 || me.Account != nil {
			t.Errorf("cookie %.20q: me = %d %+v", v, code, me)
		}
		if code := b.get("/ws", nil); code != 401 {
			t.Errorf("cookie %.20q: ws = %d", v, code)
		}
		if code := b.post("/api/logout", `{}`, nil); code != 200 {
			t.Errorf("cookie %.20q: logout = %d", v, code)
		}
	}
}

// Who may play: everyone signed in in an open world; in an invite-only
// world members and admins.
func TestAuthAccess(t *testing.T) {
	open := newFixture(t, Config{})
	if who, status := open.svc.Auth(httptest.NewRequest("GET", "/ws", nil)); status != 401 || who.Account != 0 {
		t.Errorf("no cookie: %+v, %d", who, status)
	}
	p := open.player("Mira")
	req := httptest.NewRequest("GET", "/ws", nil)
	req.AddCookie(p.cookie)
	who, status := open.svc.Auth(req)
	a, _, _ := open.st.AccountByName(context.Background(), "Mira")
	if status != 0 || who.Account != a.ID || who.Name != "Mira" {
		t.Errorf("open world: %+v, %d", who, status)
	}

	inv := newFixture(t, Config{Access: AccessInvite})
	kid := inv.player("kid")
	admin := inv.admin("teacher")
	var me Me
	if code := admin.get("/api/me", &me); code != 200 || !me.World.Play || !me.World.Invite || me.World.Role != "" || me.Account.Role != "admin" {
		t.Errorf("admin me = %d %+v", code, me)
	}
	if code := admin.get("/ws", nil); code != 200 {
		t.Errorf("admin, not a member, ws = %d", code)
	}
	if code := kid.get("/ws", nil); code != 403 {
		t.Errorf("player, not a member, ws = %d", code)
	}
	k, _, _ := inv.st.AccountByName(context.Background(), "kid")
	if err := inv.st.AddMember(context.Background(), inv.world.ID, k.ID, store.MemberPlayer); err != nil {
		t.Fatal(err)
	}
	if code := kid.get("/ws", nil); code != 200 {
		t.Errorf("member ws = %d", code)
	}
}

// Requests the API refuses before they do anything.
func TestBadRequests(t *testing.T) {
	f := newFixture(t, Config{})
	b := f.browser()
	for _, tt := range []struct {
		name, path, body string
		want             int
	}{
		{"body too large", "/api/register", `{"name":"mira","password":"` + strings.Repeat("x", maxBodyBytes) + `"}`, 400},
		{"empty body", "/api/register", ``, 400},
		{"array", "/api/login", `[]`, 400},
		{"wrong type", "/api/login", `{"name":1,"password":"x"}`, 400},
		{"empty name", "/api/login", `{"name":"","password":"correct horse"}`, 401},
		{"long name", "/api/login", `{"name":"` + strings.Repeat("a", 21) + `","password":"correct horse"}`, 401},
		{"long password", "/api/login", `{"name":"mira","password":"` + strings.Repeat("x", MaxPassword+1) + `"}`, 401},
		{"join signed out", "/api/join", `{"code":"ABCDE-FGHJK"}`, 401},
		{"invite signed out", "/api/invites", `{}`, 401},
		{"unknown path", "/api/nothing", `{}`, 404},
	} {
		if code := b.post(tt.path, tt.body, nil); code != tt.want || b.cookie != nil {
			t.Errorf("%s: %d, want %d", tt.name, code, tt.want)
		}
	}
	for _, path := range []string{"/api/login", "/api/register", "/api/logout"} {
		if code := b.get(path, nil); code != 405 {
			t.Errorf("GET %s = %d, want 405", path, code)
		}
	}
	resp, err := http.Post(f.srv.URL+"/api/me", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("POST /api/me = %d, want 405", resp.StatusCode)
	}
	resp, err = http.Get(f.srv.URL + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Cache-Control") != "no-store" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("me headers = %v", resp.Header)
	}
}

func TestSameOrigin(t *testing.T) {
	for _, tt := range []struct {
		name    string
		host    string
		headers map[string]string
		want    bool
	}{
		{"no headers", "talespeak.example", nil, true},
		{"same origin", "talespeak.example", map[string]string{"Origin": "https://talespeak.example"}, true},
		{"host in other case", "Talespeak.Example", map[string]string{"Origin": "https://talespeak.example"}, true},
		{"with port", "localhost:8080", map[string]string{"Origin": "http://localhost:8080"}, true},
		{"other port", "localhost:8080", map[string]string{"Origin": "http://localhost:9090"}, false},
		{"other host", "talespeak.example", map[string]string{"Origin": "https://evil.example"}, false},
		{"suffix trick", "talespeak.example", map[string]string{"Origin": "https://talespeak.example.evil"}, false},
		{"null origin", "talespeak.example", map[string]string{"Origin": "null"}, false},
		{"junk origin", "talespeak.example", map[string]string{"Origin": "::"}, false},
		{"fetch same-origin", "talespeak.example", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"fetch none", "talespeak.example", map[string]string{"Sec-Fetch-Site": "none"}, true},
		{"fetch same-site", "talespeak.example", map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"fetch cross-site, origin matches", "talespeak.example", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://talespeak.example"}, false},
	} {
		r := httptest.NewRequest("POST", "/api/login", nil)
		r.Host = tt.host
		for k, v := range tt.headers {
			r.Header.Set(k, v)
		}
		if got := sameOrigin(r); got != tt.want {
			t.Errorf("%s: sameOrigin = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestTokenHash(t *testing.T) {
	good := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	if h, ok := tokenHash(good); !ok || len(h) != sha256.Size {
		t.Errorf("tokenHash(good) = %x, %v", h, ok)
	}
	for _, bad := range []string{"", good[:len(good)-1], good + "A", strings.ReplaceAll(good, "a", "+"),
		base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))} {
		if _, ok := tokenHash(bad); ok {
			t.Errorf("tokenHash(%q) accepted", bad)
		}
	}
}

// Rate limits count per client address: one address is limited, another
// is not.
func TestAddressLimits(t *testing.T) {
	f := newFixture(t, Config{ClientIP: headerIP, AddressBurst: 3})
	b := f.browser()
	for i := range 3 {
		if code := b.postFrom("198.51.100.1", "/api/register", fmt.Sprintf(`{"name":"reg%d","password":"correct horse"}`, i), nil); code != 200 {
			t.Fatalf("register %d: %d", i, code)
		}
	}
	if code := b.postFrom("198.51.100.1", "/api/register", `{"name":"reg9","password":"correct horse"}`, nil); code != 429 {
		t.Errorf("register past the burst: %d", code)
	}
	if code := b.postFrom("198.51.100.2", "/api/register", `{"name":"reg9","password":"correct horse"}`, nil); code != 200 {
		t.Errorf("register from another address: %d", code)
	}
	// Refused input costs no token: the limit counts hashes.
	for range 5 {
		b.postFrom("198.51.100.3", "/api/register", `{"name":"x","password":"correct horse"}`, nil)
	}
	if code := b.postFrom("198.51.100.3", "/api/register", `{"name":"reg8","password":"correct horse"}`, nil); code != 200 {
		t.Errorf("register after refused names: %d", code)
	}
	f.now = f.now.Add(30 * time.Second)
	if code := b.postFrom("198.51.100.1", "/api/register", `{"name":"reg7","password":"correct horse"}`, nil); code != 200 {
		t.Errorf("register 30 s later: %d", code)
	}

	// Sign-ins per address.
	limited := 0
	for i := range 6 {
		if f.browser().postFrom("203.0.113.9", "/api/login", fmt.Sprintf(`{"name":"nobody%d","password":"wrong password"}`, i), nil) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("6 sign-ins from one address with burst 3: never limited")
	}
	if code := f.browser().postFrom("203.0.113.10", "/api/login", `{"name":"reg0","password":"correct horse"}`, nil); code != 200 {
		t.Errorf("sign-in from another address: %d", code)
	}
	// IPv6 addresses count by their /64.
	limited = 0
	for i := range 6 {
		if f.browser().postFrom(fmt.Sprintf("2001:db8:1:2::%x", i+1), "/api/login", `{"name":"nobody","password":"wrong password"}`, nil) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("6 sign-ins from one IPv6 /64 with burst 3: never limited")
	}
	if code := f.browser().postFrom("2001:db8:1:3::1", "/api/login", `{"name":"reg0","password":"correct horse"}`, nil); code != 200 {
		t.Errorf("sign-in from another /64: %d", code)
	}
}

// Invite code guesses count per address and per account apart.
func TestJoinLimits(t *testing.T) {
	f := newFixture(t, Config{ClientIP: headerIP, AddressBurst: 3, Access: AccessInvite})
	var accounts []*browser
	for i := range 3 {
		accounts = append(accounts, f.player(fmt.Sprint("guess", i)))
	}
	f.now = f.now.Add(time.Hour) // the register limit is not the point here
	// One address, three accounts: the address runs out first.
	limited := 0
	for i := range 6 {
		if accounts[i%3].postFrom("198.51.100.7", "/api/join", `{"code":"AAAAA-AAAAA"}`, nil) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("6 guesses from one address with burst 3: never limited")
	}
	// One account, many addresses: the account runs out at 10.
	limited = 0
	for i := range 15 {
		if accounts[0].postFrom(fmt.Sprintf("198.51.100.%d", 20+i), "/api/join", `{"code":"AAAAA-AAAAA"}`, nil) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("15 guesses from one account: never limited")
	}
}

// The default invite is one use for a week.
func TestInviteDefaults(t *testing.T) {
	f := newFixture(t, Config{Access: AccessInvite})
	admin := f.admin("teacher")
	var inv InviteAnswer
	if code := admin.post("/api/invites", `{}`, &inv); code != 200 || inv.Uses != 1 || inv.GM ||
		!inv.Expires.Equal(f.now.Add(defaultInviteTTL)) {
		t.Errorf("default invite = %d %+v", code, inv)
	}
	for _, body := range []string{`{"uses":100,"days":30}`, `{"uses":1,"days":1}`} {
		if code := admin.post("/api/invites", body, nil); code != 200 {
			t.Errorf("%s: %d", body, code)
		}
	}
	if code := admin.post("/api/invites", `{"uses":1,"extra":true}`, nil); code != 400 {
		t.Errorf("unknown field: %d", code)
	}
}

// A GM who joins with a player invite stays a GM, and the invite's use is
// left for someone else.
func TestJoinKeepsHigherRole(t *testing.T) {
	f := newFixture(t, Config{Access: AccessInvite})
	admin := f.admin("teacher")
	var inv InviteAnswer
	admin.post("/api/invites", `{"gm":true}`, &inv)
	gm := f.player("gwen")
	var me Me
	if code := gm.post("/api/join", `{"code":"`+inv.Code+`"}`, &me); code != 200 || me.World.Role != "gm" {
		t.Fatalf("gm join = %d %+v", code, me)
	}
	admin.post("/api/invites", `{"uses":1}`, &inv)
	if code := gm.post("/api/join", `{"code":"`+inv.Code+`"}`, &me); code != 200 || me.World.Role != "gm" {
		t.Errorf("gm joins with a player invite = %d %+v", code, me)
	}
	kid := f.player("kid")
	if code := kid.post("/api/join", `{"code":"`+inv.Code+`"}`, &me); code != 200 || me.World.Role != "player" {
		t.Errorf("the use is left for a player: %d %+v", code, me)
	}
}

// A code made for another world of the same database is refused, and is
// not used up by trying it here.
func TestJoinOtherWorldCode(t *testing.T) {
	f := newFixture(t, Config{Access: AccessInvite})
	ctx := context.Background()
	other, err := f.st.OpenWorld(ctx, "other pack", "")
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := CreateInvite(ctx, f.st, other.ID, 0, store.MemberPlayer, 1, f.now, f.now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	kid := f.player("kid")
	var e map[string]string
	if status := kid.post("/api/join", `{"code":"`+code+`"}`, &e); status != 400 {
		t.Fatalf("code of another world: %d %v", status, e)
	}
	k, _, _ := f.st.AccountByName(ctx, "kid")
	if role, err := f.st.Member(ctx, other.ID, k.ID); err == nil {
		t.Errorf("a refused code made the account a %s of the other world", role)
	}
	f.player("zed")
	z, _, _ := f.st.AccountByName(ctx, "zed")
	norm, _ := normalizeCode(code)
	if err := f.st.RedeemInvite(ctx, codeHash(norm), other.ID, z.ID, f.now); err != nil {
		t.Errorf("the other world's code was used up by a refused try: %v", err)
	}
}

// Hashing waits for a free slot; a request that ends first is busy, not
// an error.
func TestHashBusy(t *testing.T) {
	for range maxHashing {
		hashing <- struct{}{}
	}
	defer func() {
		for range maxHashing {
			<-hashing
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := Hash(ctx, "correct horse"); !errors.Is(err, errBusy) {
		t.Errorf("Hash with no free slot = %v, want errBusy", err)
	}
	h := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5"
	if _, err := Verify(ctx, h, "x"); !errors.Is(err, errBusy) {
		t.Errorf("Verify with no free slot = %v, want errBusy", err)
	}
}

// Past maxHashing running and maxWaiting waiting, a hash is refused at
// once, without waiting for its request to end.
func TestHashQueueFull(t *testing.T) {
	for range maxHashing {
		hashing <- struct{}{}
	}
	release := func() {
		for range maxHashing {
			<-hashing
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, maxHashing+maxWaiting)
	for range maxHashing + maxWaiting {
		go func() { done <- withHashSlot(ctx, func() {}) }()
	}
	deadline := time.Now().Add(10 * time.Second)
	for waiting.Load() < maxHashing+maxWaiting {
		if time.Now().After(deadline) {
			cancel()
			release()
			t.Fatalf("waiting = %d, want %d", waiting.Load(), maxHashing+maxWaiting)
		}
		time.Sleep(time.Millisecond) // polling for the goroutines to queue, with a deadline
	}
	start := time.Now()
	if err := withHashSlot(context.Background(), func() { t.Error("ran past a full queue") }); !errors.Is(err, errBusy) {
		t.Errorf("withHashSlot on a full queue = %v, want errBusy", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("a full queue took %v to refuse", d)
	}
	cancel()
	for range maxHashing + maxWaiting {
		if err := <-done; !errors.Is(err, errBusy) {
			t.Errorf("a waiting hash whose request ended = %v", err)
		}
	}
	release()
	if n := waiting.Load(); n != 0 {
		t.Errorf("waiting = %d after all left", n)
	}
	ran := false
	if err := withHashSlot(context.Background(), func() { ran = true }); err != nil || !ran {
		t.Errorf("after the queue emptied: %v, ran = %v", err, ran)
	}
}

// failingStore fails every sign-in lookup, like a database that is gone.
type failingStore struct {
	Store
}

var errDisk = errors.New("disk on fire")

func (failingStore) LoginAccount(context.Context, []byte, time.Time) (store.Account, error) {
	return store.Account{}, errDisk
}

func (failingStore) AccountByName(context.Context, string) (store.Account, string, error) {
	return store.Account{}, "", errDisk
}

// A store that fails is a server error, never a sign-in: Auth answers
// 503, the API 500.
func TestStoreFailure(t *testing.T) {
	f := newFixture(t, Config{})
	svc := New(Config{Store: failingStore{f.st}, World: f.world, Registration: RegistrationSelf, Access: AccessOpen, Log: quiet})
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	cookie := &http.Cookie{Name: CookieName, Value: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}

	req := httptest.NewRequest("GET", "/ws", nil)
	req.AddCookie(cookie)
	if who, status := svc.Auth(req); status != 503 || who.Account != 0 {
		t.Errorf("Auth = %+v, %d; want 503", who, status)
	}
	greq, _ := http.NewRequest("GET", srv.URL+"/api/me", nil)
	greq.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(greq)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Errorf("me = %d, want 500", resp.StatusCode)
	}
	preq, _ := http.NewRequest("POST", srv.URL+"/api/login", strings.NewReader(`{"name":"mira","password":"correct horse"}`))
	preq.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(preq)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 500 || len(resp.Cookies()) != 0 {
		t.Errorf("login = %d, cookies %v; want 500 and none", resp.StatusCode, resp.Cookies())
	}
}

// Wrong passwords for one name from one address are limited there; the
// right password from another address still signs in, and right
// passwords use up no per-name tokens.
func TestNameLimits(t *testing.T) {
	f := newFixture(t, Config{ClientIP: headerIP})
	f.player("Mira")
	f.now = f.now.Add(time.Hour)
	limited := 0
	for range 12 {
		if f.browser().postFrom("198.51.100.66", "/api/login", `{"name":"MIRA","password":"wrong password"}`, nil) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("12 wrong passwords for one name from one address: never limited")
	}
	if code := f.browser().postFrom("198.51.100.66", "/api/login", `{"name":"mira","password":"correct horse"}`, nil); code != 429 {
		t.Errorf("the guessing address, right password: %d, want still limited", code)
	}
	if code := f.browser().postFrom("198.51.100.67", "/api/login", `{"name":"mira","password":"correct horse"}`, nil); code != 200 {
		t.Errorf("another address, right password: %d", code)
	}
	for i := range 12 {
		f.now = f.now.Add(6 * time.Second) // keep within the address limit
		if code := f.browser().postFrom("198.51.100.68", "/api/login", `{"name":"mira","password":"correct horse"}`, nil); code != 200 {
			t.Fatalf("right password %d from one address: %d", i, code)
		}
	}
}
