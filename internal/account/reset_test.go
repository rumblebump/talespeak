package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// signOuts records the calls of Config.SignOut.
type signOuts struct {
	mu    sync.Mutex
	calls []string // "account login"
}

func (s *signOuts) signOut(account int64, login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, fmt.Sprint(account, " ", login))
}

func (s *signOuts) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.calls
	s.calls = nil
	return c
}

// who is what the WebSocket's sign-in check says of b in the main world.
func (b *browser) who() (ws.Who, int) {
	b.f.t.Helper()
	req, _ := http.NewRequest("GET", b.f.srv.URL+b.f.ws(0), nil)
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var who ws.Who
	if resp.StatusCode == 200 {
		_ = json.NewDecoder(resp.Body).Decode(&who)
	}
	return who, resp.StatusCode
}

func (b *browser) signIn(name, password string) int {
	b.f.t.Helper()
	return b.post("/api/login", `{"name":"`+name+`","password":"`+password+`"}`, nil)
}

func accountID(t *testing.T, f *fixture, name string) int64 {
	t.Helper()
	a, _, err := f.st.AccountByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestRegistrationModes(t *testing.T) {
	for _, c := range []struct {
		mode string
		code int
		msg  string
	}{
		{RegistrationSelf, 200, ""},
		{RegistrationOff, 403, "admin"},
		{RegistrationSSO, 403, "sign-in"},
	} {
		f := newFixture(t, Config{Registration: c.mode})
		var me Me
		f.browser().get("/api/me", &me)
		if me.Registration != c.mode || me.ClassCode || me.Email {
			t.Errorf("%s: me = %+v", c.mode, me)
		}
		var e map[string]any
		if code := f.browser().post("/api/register", `{"name":"Mira","password":"correct horse"}`, &e); code != c.code || !strings.Contains(fmt.Sprint(e["error"]), c.msg) {
			t.Errorf("%s: register = %d %v", c.mode, code, e)
		}
	}
}

func TestClassCode(t *testing.T) {
	f := newFixture(t, Config{ClassCode: "Owl-Tree 7"})
	var me Me
	if f.browser().get("/api/me", &me); !me.ClassCode {
		t.Fatalf("me = %+v, want classCode", me)
	}
	for _, body := range []string{
		`{"name":"Mira","password":"correct horse"}`,
		`{"name":"Mira","password":"correct horse","code":"owl tree"}`,
	} {
		var e map[string]string
		if code := f.browser().post("/api/register", body, &e); code != 403 || !strings.Contains(e["error"], "class code") {
			t.Errorf("%s = %d %v", body, code, e)
		}
	}
	if code := f.browser().post("/api/register", `{"name":"Mira","password":"correct horse","code":"OWLTREE7"}`, nil); code != 200 {
		t.Errorf("right code in another case and spacing = %d", code)
	}
	for typed, want := range map[string]bool{"owl-tree 7": true, " O W L T R E E 7 ": true, "owltree": false, "": false, "owltree77": false} {
		if got := SameClassCode(typed, "Owl-Tree 7"); got != want {
			t.Errorf("SameClassCode(%q) = %v", typed, got)
		}
	}
}

// Registering never grants access in edu mode: a new account sees no
// world, plays none and creates none, until a game master's code (kids)
// or an admin (teachers) lets it in. A class code changes none of it.
func TestEduRegistrationGrantsNothing(t *testing.T) {
	f := newInviteFixture(t, Config{Edu: true, ClassCode: "owls"})
	root := f.admin("root")
	teacher, kid := f.browser(), f.browser()
	for name, b := range map[string]*browser{"teach": teacher, "kid": kid} {
		var me Me
		if code := b.post("/api/register", `{"name":"`+name+`","password":"correct horse","code":"owls"}`, &me); code != 200 || me.Create {
			t.Fatalf("register %s = %d %+v", name, code, me)
		}
		if l := b.lobby(); len(l.Worlds) != 0 || len(l.Packs) != 0 {
			t.Errorf("%s's lobby = %+v", name, l)
		}
		if _, code := b.who(); code != 404 {
			t.Errorf("%s plays the main world: %d", name, code)
		}
		if _, code := b.create(`{"pack":"demo"}`); code != 403 {
			t.Errorf("%s creates a world: %d", name, code)
		}
	}
	made, code := root.create(`{"pack":"demo"}`)
	if code != 200 {
		t.Fatal(code)
	}
	if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"teach","role":"gm"}`, made.World.ID), nil); code != 200 {
		t.Fatal(code)
	}
	var me Me
	if teacher.get("/api/me", &me); !me.Create || !me.Account.ResetCodes {
		t.Errorf("teacher made GM by an admin: me = %+v", me.Account)
	}
	var kidMe Me // a fresh one: omitted fields keep old values
	if kid.get("/api/me", &kidMe); kidMe.Create || kidMe.Account.ResetCodes {
		t.Errorf("kid: me = %+v", kidMe.Account)
	}
}

func TestResetCodeByAdmin(t *testing.T) {
	so := &signOuts{}
	f := newFixture(t, Config{SignOut: so.signOut})
	root := f.admin("root")
	mira, other := f.player("mira"), f.browser()
	if other.signIn("mira", "correct horse") != 200 {
		t.Fatal("second sign-in")
	}
	var me Me
	if root.get("/api/me", &me); !me.Account.ResetCodes {
		t.Errorf("admin me = %+v", me.Account)
	}
	var e map[string]string
	if code := root.post("/api/resetcode", `{"name":"nobody"}`, &e); code != 404 {
		t.Errorf("code for nobody = %d %v", code, e)
	}
	var rc ResetCodeAnswer
	if code := root.post("/api/resetcode", `{"name":"MIRA"}`, &rc); code != 200 || rc.Name != "mira" || len(rc.Code) != 11 || !rc.Expires.Equal(f.now.Add(ResetCodeTTL)) {
		t.Fatalf("resetcode = %d %+v", code, rc)
	}
	so.take()
	// Wrong codes and bad passwords change nothing.
	for _, body := range []string{
		`{"code":"00000-00000","password":"a new password"}`,
		`{"code":"` + rc.Code + `","password":"short"}`,
		`{"code":"nonsense","password":"a new password"}`,
	} {
		if code := f.browser().post("/api/reset", body, &e); code != 400 {
			t.Errorf("%s = %d %v", body, code, e)
		}
	}
	reset := f.browser()
	typed := strings.ToLower(strings.ReplaceAll(rc.Code, "-", " "))
	if code := reset.post("/api/reset", `{"code":"`+typed+`","password":"a new password"}`, &me); code != 200 || me.Account == nil || me.Account.Name != "mira" || me.Notice == "" {
		t.Fatalf("reset = %d %+v", code, me)
	}
	id := accountID(t, f, "mira")
	if got := so.take(); len(got) != 1 || got[0] != fmt.Sprint(id, " ") {
		t.Errorf("sign-outs = %v, want all of mira's", got)
	}
	for _, b := range []*browser{mira, other} {
		if _, code := b.who(); code != 401 {
			t.Errorf("an old sign-in still plays: %d", code)
		}
	}
	if _, code := reset.who(); code != 200 {
		t.Errorf("the reset browser is not signed in: %d", code)
	}
	if f.browser().signIn("mira", "correct horse") != 401 || f.browser().signIn("mira", "a new password") != 200 {
		t.Error("the password did not change")
	}
	if code := f.browser().post("/api/reset", `{"code":"`+rc.Code+`","password":"another one"}`, &e); code != 400 {
		t.Errorf("a code used twice = %d", code)
	}
	// A code expires after a day.
	root.post("/api/resetcode", `{"name":"mira"}`, &rc)
	f.now = f.now.Add(ResetCodeTTL)
	if code := f.browser().post("/api/reset", `{"code":"`+rc.Code+`","password":"another one"}`, &e); code != 400 {
		t.Errorf("an expired code = %d", code)
	}
}

func TestResetCodeWho(t *testing.T) {
	// Outside edu mode only admins make codes, a game master included.
	f := newFixture(t, Config{})
	ann, bob := f.signUp("ann"), f.signUp("bob")
	made, _ := ann.create(`{"pack":"test-pack","visibility":"private"}`)
	if code := bob.post("/api/join", `{"code":"`+made.Invite.Code+`"}`, nil); code != 200 {
		t.Fatal(code)
	}
	var me Me
	if ann.get("/api/me", &me); me.Account.ResetCodes {
		t.Error("a public server's GM makes reset codes")
	}
	if code := ann.post("/api/resetcode", `{"name":"bob"}`, nil); code != 403 {
		t.Errorf("public GM = %d", code)
	}

	// In edu mode a game master makes codes only for the plain players
	// of their worlds.
	f = newInviteFixture(t, Config{Edu: true})
	root := f.admin("root")
	f.admin("root2")
	teach, kid := f.signUp("teach"), f.signUp("kid")
	f.signUp("stranger")
	f.signUp("other")
	w1, _ := root.create(`{"pack":"demo"}`)
	w2, _ := root.create(`{"pack":"demo"}`)
	for _, m := range []struct {
		world int64
		name  string
		role  string
	}{{w1.World.ID, "teach", "gm"}, {w1.World.ID, "kid", "player"}, {w1.World.ID, "other", "gm"}, {w2.World.ID, "stranger", "player"}} {
		if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":%q,"role":%q}`, m.world, m.name, m.role), nil); code != 200 {
			t.Fatal(code)
		}
	}
	var rc ResetCodeAnswer
	if code := teach.post("/api/resetcode", `{"name":"kid"}`, &rc); code != 200 || rc.Name != "kid" {
		t.Fatalf("GM for their player = %d %+v", code, rc)
	}
	for _, name := range []string{"stranger", "other", "teach", "root2", "nobody", ""} {
		var e map[string]string
		if code := teach.post("/api/resetcode", `{"name":"`+name+`"}`, &e); code != 403 || e["error"] != errNotYours.Error() {
			t.Errorf("GM for %q = %d %v", name, code, e)
		}
	}
	if code := kid.post("/api/resetcode", `{"name":"teach"}`, nil); code != 403 {
		t.Errorf("a kid made a code: %d", code)
	}
	// Admins make codes for anyone, admins included.
	if code := root.post("/api/resetcode", `{"name":"root2"}`, &rc); code != 200 {
		t.Errorf("admin for an admin = %d", code)
	}
	if code := f.browser().post("/api/reset", `{"code":"`+rc.Code+`","password":"a new password"}`, nil); code != 200 {
		t.Errorf("reset with the admin's code = %d", code)
	}
}

func TestChangePassword(t *testing.T) {
	so := &signOuts{}
	f := newFixture(t, Config{SignOut: so.signOut})
	mira, other := f.player("mira"), f.browser()
	other.signIn("mira", "correct horse")
	var e map[string]string
	if code := mira.post("/api/password", `{"password":"wrong one!","new":"a new password"}`, &e); code != 401 {
		t.Errorf("wrong current password = %d %v", code, e)
	}
	if code := mira.post("/api/password", `{"password":"correct horse","new":"short"}`, &e); code != 400 {
		t.Errorf("short new password = %d %v", code, e)
	}
	if code := f.browser().post("/api/password", `{"password":"correct horse","new":"a new password"}`, &e); code != 401 {
		t.Errorf("signed out = %d", code)
	}
	if len(so.take()) != 0 {
		t.Error("a refused change signed out")
	}
	var me Me
	if code := mira.post("/api/password", `{"password":"correct horse","new":"a new password"}`, &me); code != 200 || me.Notice == "" {
		t.Fatalf("change = %d %+v", code, me)
	}
	if got := so.take(); len(got) != 1 || got[0] != fmt.Sprint(accountID(t, f, "mira"), " ") {
		t.Errorf("sign-outs = %v", got)
	}
	if _, code := other.who(); code != 401 {
		t.Errorf("the other device still plays: %d", code)
	}
	if _, code := mira.who(); code != 200 {
		t.Errorf("this browser is signed out: %d", code)
	}
}

// Signing out ends the games of that sign-in only.
func TestLogoutEndsItsGames(t *testing.T) {
	so := &signOuts{}
	f := newFixture(t, Config{SignOut: so.signOut})
	mira, other := f.player("mira"), f.browser()
	other.signIn("mira", "correct horse")
	who, _ := mira.who()
	otherWho, _ := other.who()
	if who.Login == "" || who.Login == otherWho.Login {
		t.Fatalf("logins %q and %q", who.Login, otherWho.Login)
	}
	mira.post("/api/logout", `{}`, nil)
	if got := so.take(); len(got) != 1 || got[0] != fmt.Sprint(who.Account, " ", who.Login) {
		t.Errorf("sign-outs = %v, want %d %s", got, who.Account, who.Login)
	}
	f.browser().post("/api/logout", `{}`, nil) // not signed in: nothing to end
	if got := so.take(); len(got) != 0 {
		t.Errorf("sign-outs = %v", got)
	}
}

func TestGroupCode(t *testing.T) {
	for in, want := range map[string]string{"ABCDE12345": "ABCDE-12345", "ABCDEFGHJK0123456789": "ABCDE-FGHJK-01234-56789", "ABC": "ABC"} {
		if got := groupCode(in); got != want {
			t.Errorf("groupCode(%q) = %q", in, got)
		}
	}
	if c := randomCode(linkCodeLen); len(c) != linkCodeLen || strings.Trim(c, codeAlphabet) != "" {
		t.Errorf("randomCode = %q", c)
	}
}
