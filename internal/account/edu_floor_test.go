package account

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

// The edu safety floor (T-0011): in edu mode only admins make game
// masters, whatever a game master asks. A game master runs their world's
// players (takes them out) but never makes one a GM, by a code or by the
// members list; players see no members; registering grants nothing.
func TestEduFloor(t *testing.T) {
	f := newFixture(t, Config{Edu: true, Registration: RegistrationSelf})
	root := f.admin("root")
	tess, kim := f.signUp("tess"), f.signUp("kim")
	f.signUp("tom")
	w := f.world.ID
	member := func(b *browser, name, role string) int {
		return b.post("/api/members", fmt.Sprintf(`{"world":%d,"name":%q,"role":%q}`, w, name, role), nil)
	}
	if l := kim.lobby(); len(l.Worlds) != 0 || len(l.Packs) != 0 {
		t.Fatalf("a new kid's lobby = %+v", l)
	}
	for _, name := range []string{"tess", "tom"} {
		if code := member(root, name, "gm"); code != 200 {
			t.Fatalf("admin makes %s GM = %d", name, code)
		}
	}
	var inv InviteAnswer
	if code := tess.post("/api/invites", fmt.Sprintf(`{"world":%d}`, w), &inv); code != 200 {
		t.Fatalf("teacher's player code = %d", code)
	}
	if code := kim.post("/api/join", `{"code":"`+inv.Code+`"}`, nil); code != 200 {
		t.Fatalf("kid joins = %d", code)
	}

	// No GM rights from a game master.
	if code := tess.post("/api/invites", fmt.Sprintf(`{"world":%d,"gm":true}`, w), nil); code != 403 {
		t.Errorf("teacher's GM code = %d, want 403", code)
	}
	for _, tt := range []struct {
		who        *browser
		name, role string
		code       int
	}{
		{tess, "kim", "gm", 403},     // a GM makes no GMs in edu mode
		{tess, "tom", "player", 403}, // nor changes another GM
		{tess, "tom", "none", 403},
		{tess, "tess", "player", 403}, // nor themselves
		{tess, "root", "player", 403}, // nor adds accounts by name
		{kim, "kim", "gm", 403},       // players change nothing
		{kim, "tess", "none", 403},
	} {
		if code := member(tt.who, tt.name, tt.role); code != tt.code {
			t.Errorf("%s -> %s: %d, want %d", tt.name, tt.role, code, tt.code)
		}
	}
	if c, _ := kim.card(w); c.Role != "player" {
		t.Errorf("kim's role = %q", c.Role)
	}

	// The members list: for GMs and admins only, and it says what each may do.
	var l MemberList
	if code := kim.get(fmt.Sprintf("/api/members?world=%d", w), &l); code != 403 {
		t.Errorf("kid's members list = %d", code)
	}
	if code := tess.get(fmt.Sprintf("/api/members?world=%d", w), &l); code != 200 || l.MakeGM || l.Add {
		t.Fatalf("teacher's members list = %d %+v", code, l)
	}
	got := fmt.Sprint(l.Members)
	if want := "[{tess gm false} {tom gm false} {kim player true}]"; got != want {
		t.Errorf("teacher's list = %s, want %s", got, want)
	}
	if code := root.get(fmt.Sprintf("/api/members?world=%d", w), &l); code != 200 || !l.MakeGM || !l.Add {
		t.Errorf("admin's members list = %d %+v", code, l)
	}

	// A GM takes a player out.
	if code := member(tess, "kim", "none"); code != 200 {
		t.Errorf("teacher removes kid = %d", code)
	}
	if _, ok := kim.card(w); ok {
		t.Error("the kid still sees the world")
	}
	// Taken out, a kid cannot learn of the world.
	if code := kim.get(fmt.Sprintf("/api/members?world=%d", w), &l); code != 404 {
		t.Errorf("removed kid's members list = %d, want 404", code)
	}
}

// Outside edu mode a world's GM makes its players GMs too, by the list or
// a code (design: "its GM adds you"), but changes no GM.
func TestPublicGMMakesGMs(t *testing.T) {
	f := newFixture(t, Config{})
	ann, bob := f.signUp("ann"), f.signUp("bob")
	made, _ := ann.create(`{"pack":"test-pack","visibility":"private"}`)
	w := made.World.ID
	var inv InviteAnswer
	if code := ann.post("/api/invites", fmt.Sprintf(`{"world":%d}`, w), &inv); code != 200 {
		t.Fatalf("code = %d", code)
	}
	if code := bob.post("/api/join", `{"code":"`+inv.Code+`"}`, nil); code != 200 {
		t.Fatalf("join = %d", code)
	}
	var l MemberList
	if code := ann.get(fmt.Sprintf("/api/members?world=%d", w), &l); code != 200 || !l.MakeGM || l.Add {
		t.Fatalf("list = %d %+v", code, l)
	}
	if code := ann.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"bob","role":"gm"}`, w), nil); code != 200 {
		t.Fatalf("ann makes bob GM = %d", code)
	}
	if c, _ := bob.card(w); c.Role != "gm" {
		t.Errorf("bob's card = %+v", c)
	}
	// Now a GM, bob is out of reach for ann, and ann for bob.
	if code := ann.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"bob","role":"none"}`, w), nil); code != 403 {
		t.Errorf("ann removes GM bob = %d", code)
	}
	if code := bob.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"ann","role":"player"}`, w), nil); code != 403 {
		t.Errorf("bob demotes ann = %d", code)
	}
	for _, q := range []string{"", "?world=", "?world=0", "?world=x", "?world=999999"} {
		if code := ann.get("/api/members"+q, nil); code != 404 {
			t.Errorf("GET /api/members%s = %d", q, code)
		}
	}
}

// "as=player" on the WebSocket URL starts a game master's session with GM
// mode off; anything else leaves it on. It never makes a player a GM.
func TestAuthAsPlayer(t *testing.T) {
	f := newFixture(t, Config{})
	ann, bob := f.signUp("ann"), f.signUp("bob")
	made, _ := ann.create(`{"pack":"test-pack","visibility":"public"}`)
	for _, tt := range []struct {
		b            *browser
		query        string
		gm, asPlayer bool
	}{
		{ann, "", true, false},
		{ann, "&as=player", true, true},
		{ann, "&as=gm", true, false},
		{ann, "&as=PLAYER", true, false},
		{bob, "&as=player", false, true},
		{bob, "&as=gm", false, false},
	} {
		req := httptest.NewRequest("GET", fmt.Sprintf("/ws?world=%d%s", made.World.ID, tt.query), nil)
		req.AddCookie(tt.b.cookie)
		who, status := f.svc.Auth(req)
		if status != 0 || who.GM != tt.gm || who.AsPlayer != tt.asPlayer {
			t.Errorf("%q: %d %+v", tt.query, status, who)
		}
	}
}
