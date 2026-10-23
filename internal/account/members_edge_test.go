package account

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/hub"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// auditDown is a store whose audit log cannot be written.
type auditDown struct{ Store }

func (auditDown) AddAudit(context.Context, store.AuditEntry) error { return errDisk }

// POST /api/members edges: signed out, an ended world, a log that cannot
// be written (the change does not happen), taking out an account that is
// no member, and making a GM a GM again.
func TestMembersEdges(t *testing.T) {
	live := &fakeLive{online: map[int64]int{}}
	f := newFixture(t, Config{Live: live})
	root := f.admin("root")
	ann := f.signUp("ann")
	f.signUp("bob")
	ctx := context.Background()
	b, _, _ := f.st.AccountByName(ctx, "bob")
	made, _ := ann.create(`{"pack":"test-pack","visibility":"private"}`)
	w := made.World.ID
	body := func(name, role string) string {
		return fmt.Sprintf(`{"world":%d,"name":%q,"role":%q}`, w, name, role)
	}

	if code := f.browser().post("/api/members", body("bob", "gm"), nil); code != http.StatusUnauthorized {
		t.Errorf("signed out = %d", code)
	}
	// Taking out an account that is no member is fine, and logged.
	if code := root.post("/api/members", body("bob", "none"), nil); code != 200 {
		t.Errorf("remove a non-member = %d", code)
	}
	if code := root.post("/api/members", body("bob", "gm"), nil); code != 200 {
		t.Fatalf("make bob GM = %d", code)
	}
	if code := root.post("/api/members", body("bob", "gm"), nil); code != 200 {
		t.Errorf("GM again = %d", code)
	}
	if r, _ := f.st.Member(ctx, w, b.ID); r != store.MemberGM {
		t.Errorf("bob's role = %q", r)
	}
	if log, _ := f.st.AuditLog(ctx, w, 10); len(log) != 3 {
		t.Errorf("audit = %+v", log)
	}
	told := len(live.roles)

	// The log cannot be written: nothing changes, and the world is not told.
	down := New(Config{Store: auditDown{f.st}, Registration: RegistrationSelf, Packs: testPacks, Log: quiet, Live: live, Home: f.world.ID})
	srv := httptest.NewServer(down.Handler())
	defer srv.Close()
	f2 := *f
	f2.srv = srv
	rootDown := &browser{f: &f2, cookie: root.cookie}
	if code := rootDown.post("/api/members", body("bob", "player"), nil); code < 500 {
		t.Errorf("with the log down = %d, want a server error", code)
	}
	if r, _ := f.st.Member(ctx, w, b.ID); r != store.MemberGM {
		t.Errorf("bob's role changed without a log entry: %q", r)
	}
	if len(live.roles) != told {
		t.Errorf("the world was told of a change that did not happen: %q", live.roles[told:])
	}

	// An ended world is gone.
	if code := ann.post("/api/finish", fmt.Sprintf(`{"world":%d}`, w), nil); code != 200 {
		t.Fatalf("finish = %d", code)
	}
	if code := root.post("/api/members", body("bob", "player"), nil); code != 404 {
		t.Errorf("ended world = %d", code)
	}
	// A name past the longest an account can have.
	if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":%q,"role":"gm"}`, f.world.ID, strings.Repeat("b", 200)), nil); code != 404 {
		t.Errorf("long name = %d", code)
	}
}

// Against a real hub: a player who plays the world is made its GM by an
// admin; their game ends with the role message, the next connection is
// GM, and /audit there shows the admin's change. Taken out of a private
// world, they may not connect again.
func TestMembersWithHub(t *testing.T) {
	live := &liveHub{}
	f := newFixture(t, Config{Live: live})
	demo, err := pack.Load(content.Demo())
	if err != nil {
		t.Fatal(err)
	}
	live.h = hub.New(hub.Config{Store: f.st, Packs: mustCatalog(t, demo), Log: quiet,
		Load: func(w store.World, p *pack.Pack) (*game.World, error) {
			return game.NewWorld(p, script.DefaultLimits, quiet, game.WithStore(f.st, w.ID), game.WithAudit(f.st))
		}})
	t.Cleanup(live.h.Close)

	root := f.admin("root")
	ann, bob := f.signUp("ann"), f.signUp("bob")
	made, _ := ann.create(`{"pack":"demo","visibility":"private"}`)
	id := made.World.ID
	bob.post("/api/join", `{"code":"`+made.Invite.Code+`"}`, nil)
	connect := func() (ws.Session, ws.Who) {
		t.Helper()
		req := httptest.NewRequest("GET", f.ws(id), nil)
		req.AddCookie(bob.cookie)
		who, status := f.svc.Auth(req)
		if status != 0 {
			t.Fatalf("Auth = %d", status)
		}
		s, ms := live.h.NewSession("", who)
		if s == nil {
			t.Fatalf("refused: %+v", ms)
		}
		return s, who
	}
	help := func(s ws.Session) string {
		for _, m := range s.Handle(ws.Message{Type: "input", Text: "/help"}) {
			if m.Type == "notice" {
				return m.Text
			}
		}
		return ""
	}
	s, who := connect()
	defer s.Close()
	if who.GM || strings.Contains(help(s), "/say") {
		t.Fatalf("bob is GM before: %+v", who)
	}

	if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"bob","role":"gm"}`, id), nil); code != 200 {
		t.Fatalf("make bob GM = %d", code)
	}
	if ms, ok := s.(ws.Pusher).Pending(); ok || len(ms) != 1 || !strings.Contains(ms[0].Message, "role in this world has changed") {
		t.Errorf("bob's game after the change: %+v, %v", ms, ok)
	}
	s.Close()
	gm, who := connect()
	defer gm.Close()
	if !who.GM || !strings.Contains(help(gm), "/say") {
		t.Errorf("bob is no GM after: %+v", who)
	}
	var audit string
	for _, m := range gm.Handle(ws.Message{Type: "input", Text: "/audit"}) {
		audit += m.Text
	}
	if !strings.Contains(audit, `root: role "bob: gm"`) {
		t.Errorf("/audit = %q", audit)
	}

	// Taken out: the game ends, and the world is closed to bob.
	if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"bob","role":"none"}`, id), nil); code != 200 {
		t.Fatalf("remove bob = %d", code)
	}
	if ms, ok := gm.(ws.Pusher).Pending(); ok || len(ms) != 1 {
		t.Errorf("bob's game after removal: %+v, %v", ms, ok)
	}
	req := httptest.NewRequest("GET", f.ws(id), nil)
	req.AddCookie(bob.cookie)
	if _, status := f.svc.Auth(req); status == 0 {
		t.Error("bob may still connect to the private world")
	}
	if n := live.h.Online(id); n != 0 {
		t.Errorf("online = %d", n)
	}
}
