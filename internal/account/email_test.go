package account

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
)

const publicURL = "https://play.example.org"

// mailbox counts the outbox's kicks.
type mailbox struct {
	mu    sync.Mutex
	kicks int
}

func (m *mailbox) kick() { m.mu.Lock(); m.kicks++; m.mu.Unlock() }

// mailFixture is a fixture with email on, its log in a buffer.
func mailFixture(t *testing.T, cfg Config) (*fixture, *syncBuffer, *mailbox) {
	t.Helper()
	logs := &syncBuffer{}
	box := &mailbox{}
	cfg.Email, cfg.PublicURL, cfg.Kick = true, publicURL, box.kick
	var f *fixture
	if cfg.Edu {
		f = newInviteFixture(t, cfg)
	} else {
		f = newFixture(t, cfg)
	}
	f.svc.cfg.Log = slog.New(slog.NewTextHandler(logs, nil))
	return f, logs, box
}

// syncBuffer is a bytes.Buffer for a logger that requests write to at
// once.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// mails returns the mails waiting in the outbox.
func (f *fixture) mails() []store.Mail {
	f.t.Helper()
	m, err := f.st.DueMail(context.Background(), f.now, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

var linkCode = regexp.MustCompile(`/#(reset|verify)=([0-9A-Z-]+)\n`)

// codeIn returns the code of m's link of kind.
func codeIn(t *testing.T, m store.Mail, kind string) string {
	t.Helper()
	got := linkCode.FindStringSubmatch(m.Body)
	if got == nil || got[1] != kind || !strings.Contains(m.Body, publicURL+"/#"+kind+"=") {
		t.Fatalf("no %s link in %q", kind, m.Body)
	}
	return got[2]
}

// confirm gives the account of b the confirmed address email.
func (f *fixture) confirm(b *browser, email string) {
	f.t.Helper()
	before := len(f.mails())
	if code := b.post("/api/email", `{"email":"`+email+`"}`, nil); code != 200 {
		f.t.Fatalf("POST /api/email = %d", code)
	}
	ms := f.mails()
	if len(ms) != before+1 {
		f.t.Fatalf("mails = %d", len(ms))
	}
	if code := f.browser().post("/api/verify", `{"code":"`+codeIn(f.t, ms[len(ms)-1], "verify")+`"}`, nil); code != 200 {
		f.t.Fatalf("verify = %d", code)
	}
}

func TestEmailOff(t *testing.T) {
	f := newFixture(t, Config{})
	mira := f.player("mira")
	var e map[string]string
	for path, body := range map[string]string{
		"/api/forgot": `{"name":"mira"}`, "/api/email": `{"email":"m@example.org"}`, "/api/verify": `{"code":"ABCDE-ABCDE-ABCDE-ABCDE"}`,
		"/api/register": `{"name":"Jo","password":"correct horse","email":"jo@example.org"}`,
	} {
		if code := mira.post(path, body, &e); code != 403 && code != 400 {
			t.Errorf("%s with mail off = %d %v", path, code, e)
		}
	}
}

func TestEmailConfirm(t *testing.T) {
	f, logs, box := mailFixture(t, Config{})
	b := f.browser()
	var me Me
	if b.get("/api/me", &me); !me.Email {
		t.Fatalf("me = %+v", me)
	}
	if code := b.post("/api/register", `{"name":"mira","password":"correct horse","email":"Mira@Example.org"}`, &me); code != 200 || !strings.Contains(me.Notice, "Mira@Example.org") || !me.Account.AddEmail || me.Account.Email != "" {
		t.Fatalf("register = %d %+v %+v", code, me, me.Account)
	}
	ms := f.mails()
	if len(ms) != 1 || ms[0].To != "Mira@Example.org" || ms[0].Subject != verifySubject || box.kicks != 1 {
		t.Fatalf("mails = %+v, kicks %d", ms, box.kicks)
	}
	code := codeIn(t, ms[0], "verify")
	var e map[string]string
	for _, bad := range []string{"ABCDE-ABCDE-ABCDE-ABCDE", "nonsense", code[:11]} {
		if c := f.browser().post("/api/verify", `{"code":"`+bad+`"}`, &e); c != 400 {
			t.Errorf("verify %q = %d %v", bad, c, e)
		}
	}
	// The link works on another device, signed out.
	var out Me
	if c := f.browser().post("/api/verify", `{"code":"`+strings.ToLower(code)+`"}`, &out); c != 200 || out.Account != nil || !strings.Contains(out.Notice, "mira") {
		t.Fatalf("verify = %d %+v", c, out)
	}
	var fresh Me
	if b.get("/api/me", &fresh); fresh.Account.Email != "Mira@Example.org" {
		t.Errorf("me after confirming = %+v", fresh.Account)
	}
	if c := f.browser().post("/api/verify", `{"code":"`+code+`"}`, &e); c != 400 {
		t.Errorf("a link used twice = %d", c)
	}
	// Signing in with the address works, in any case.
	if f.browser().signIn("MIRA@example.org", "correct horse") != 200 || f.browser().signIn("mira@example.org", "wrong one!") != 401 {
		t.Error("sign-in by email")
	}
	// Another account cannot confirm the same address.
	jo := f.player("joan")
	if c := jo.post("/api/email", `{"email":"mira@example.org"}`, nil); c != 200 {
		t.Fatal(c)
	}
	ms = f.mails()
	if c := f.browser().post("/api/verify", `{"code":"`+codeIn(t, ms[len(ms)-1], "verify")+`"}`, &e); c != 409 {
		t.Errorf("verify a taken address = %d %v", c, e)
	}
	// Removing it.
	if c := b.post("/api/email", `{"email":""}`, &me); c != 200 || me.Account.Email != "" {
		t.Errorf("remove = %d %+v", c, me.Account)
	}
	for _, bad := range []string{"not an address", "Mira <m@example.org>", "m@example"} {
		if c := b.post("/api/email", `{"email":"`+bad+`"}`, &e); c != 400 {
			t.Errorf("email %q = %d", bad, c)
		}
	}
	if strings.Contains(strings.ToLower(logs.String()), "example.org") {
		t.Errorf("an address reached the log:\n%s", logs.String())
	}
}

func TestEmailLimits(t *testing.T) {
	f, _, _ := mailFixture(t, Config{})
	mira := f.player("mira")
	for i := range 3 {
		if c := mira.post("/api/email", fmt.Sprintf(`{"email":"m%d@example.org"}`, i), nil); c != 200 {
			t.Fatalf("mail %d = %d", i, c)
		}
	}
	if c := mira.post("/api/email", `{"email":"m9@example.org"}`, nil); c != 429 {
		t.Errorf("fourth mail at once = %d", c)
	}
	// One address gets few mails, whoever asks.
	for i, name := range []string{"joan", "alex", "bobo", "cyan"} {
		want := 200
		if i == 3 {
			want = 429
		}
		if c := f.player(name).post("/api/email", `{"email":"victim@example.org"}`, nil); c != want {
			t.Errorf("mail %d to one address = %d, want %d", i+1, c, want)
		}
	}
}

func TestForgotPassword(t *testing.T) {
	so := &signOuts{}
	f, logs, _ := mailFixture(t, Config{SignOut: so.signOut})
	mira := f.player("mira")
	f.player("joan") // no address
	f.confirm(mira, "mira@example.org")
	before := len(f.mails())
	var answers []string
	for _, name := range []string{"nobody", "joan", "nobody@example.org", "a-name-far-too-long-for-a-name"} {
		var n Notice
		if c := f.browser().post("/api/forgot", `{"name":"`+name+`"}`, &n); c != 200 {
			t.Fatalf("forgot %s = %d", name, c)
		}
		answers = append(answers, n.Message)
	}
	if len(f.mails()) != before {
		t.Fatal("a mail went to an account without an address")
	}
	var n Notice
	if c := f.browser().post("/api/forgot", `{"name":"MIRA"}`, &n); c != 200 {
		t.Fatal(c)
	}
	for _, a := range answers {
		if a != n.Message {
			t.Errorf("answers differ: %q and %q", a, n.Message)
		}
	}
	ms := f.mails()
	if len(ms) != before+1 || ms[len(ms)-1].To != "mira@example.org" || ms[len(ms)-1].Subject != resetSubject {
		t.Fatalf("mails = %+v", ms)
	}
	m := ms[len(ms)-1]
	code := codeIn(t, m, "reset")
	if !m.Expires.Equal(f.now.Add(ResetLinkTTL)) || !strings.Contains(m.Body, "mira") {
		t.Errorf("reset mail = %+v", m)
	}
	// By address too; a new link replaces the old one.
	f.browser().post("/api/forgot", `{"name":"Mira@example.org"}`, nil)
	ms = f.mails()
	newCode := codeIn(t, ms[len(ms)-1], "reset")
	if len(ms) != before+1 || newCode == code {
		t.Fatalf("after a second request: %d mails", len(ms))
	}
	var e map[string]string
	if c := f.browser().post("/api/reset", `{"code":"`+code+`","password":"a new password"}`, &e); c != 400 {
		t.Errorf("the replaced link = %d", c)
	}
	so.take()
	var me Me
	if c := f.browser().post("/api/reset", `{"code":"`+newCode+`","password":"a new password"}`, &me); c != 200 || me.Account.Name != "mira" {
		t.Fatalf("reset by link = %d %+v", c, me)
	}
	if len(so.take()) != 1 {
		t.Error("the reset did not end mira's games")
	}
	if len(f.mails()) != before {
		t.Error("the used link's mail stays in the outbox")
	}
	// Past three mails at once (the confirmation and two links), the
	// same answer, but no mail: a stranger cannot fill the mailbox.
	f.browser().post("/api/forgot", `{"name":"mira"}`, &n)
	if n.Message != answers[0] || len(f.mails()) != before {
		t.Errorf("a limited request answers %q, mails %d", n.Message, len(f.mails()))
	}
	if strings.Contains(logs.String(), "example.org") {
		t.Errorf("an address reached the log:\n%s", logs.String())
	}
}

func TestForgotRateLimit(t *testing.T) {
	f, _, _ := mailFixture(t, Config{})
	b := f.browser()
	for i := range 10 {
		if c := b.post("/api/forgot", `{"name":"nobody"}`, nil); c != 200 {
			t.Fatalf("request %d = %d", i, c)
		}
	}
	if c := b.post("/api/forgot", `{"name":"nobody"}`, nil); c != 429 {
		t.Errorf("11th request = %d", c)
	}
	if c := b.post("/api/forgot", `{"name":""}`, nil); c != 400 {
		t.Errorf("empty name = %d", c)
	}
}

// In edu mode players never give an address: not at registration, not
// later; teachers (game masters) and admins may.
func TestEduEmail(t *testing.T) {
	f, _, _ := mailFixture(t, Config{Edu: true})
	root := f.admin("root")
	var e map[string]string
	if c := f.browser().post("/api/register", `{"name":"kid","password":"correct horse","email":"kid@example.org"}`, &e); c != 403 {
		t.Errorf("edu registration with an address = %d %v", c, e)
	}
	kid, teach := f.player("kid"), f.player("teach")
	var me Me
	if kid.get("/api/me", &me); me.Account.AddEmail || !me.Email {
		t.Errorf("kid me = %+v %+v", me, me.Account)
	}
	if c := kid.post("/api/email", `{"email":"kid@example.org"}`, &e); c != 403 {
		t.Errorf("kid adds an address = %d", c)
	}
	if c := teach.post("/api/email", `{"email":"teach@example.org"}`, nil); c != 403 {
		t.Errorf("a teacher before an admin made them GM = %d", c)
	}
	made, _ := root.create(`{"pack":"demo"}`)
	root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"teach","role":"gm"}`, made.World.ID), nil)
	f.confirm(teach, "teach@example.org")
	f.confirm(root, "root@example.org")
	// A teacher taken out of every world keeps no way to confirm a new
	// address, and their old one gets no reset link.
	if c := teach.post("/api/email", `{"email":"new@example.org"}`, nil); c != 200 {
		t.Fatal(c)
	}
	ms := f.mails()
	root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"teach","role":"none"}`, made.World.ID), nil)
	if c := f.browser().post("/api/verify", `{"code":"`+codeIn(t, ms[len(ms)-1], "verify")+`"}`, &e); c != 403 {
		t.Errorf("verify after losing the GM role = %d", c)
	}
	before := len(f.mails())
	f.browser().post("/api/forgot", `{"name":"teach"}`, nil)
	if len(f.mails()) != before {
		t.Error("a former teacher got a reset link")
	}
	f.browser().post("/api/forgot", `{"name":"root"}`, nil)
	if len(f.mails()) != before+1 {
		t.Error("the admin got no reset link")
	}
}
