package account

import (
	"context"
	"fmt"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
)

// Admins change who is a game master or player of a world; the world is
// told, so a player who plays it connects again with the new role. The
// audit log has every change.
func TestMembers(t *testing.T) {
	live := &fakeLive{online: map[int64]int{}}
	f := newFixture(t, Config{Live: live})
	root := f.admin("root")
	ann, bob := f.signUp("ann"), f.signUp("bob")
	made, _ := ann.create(`{"pack":"test-pack","visibility":"private"}`)
	w := made.World.ID
	ctx := context.Background()
	b, _, _ := f.st.AccountByName(ctx, "bob")

	var got MemberAnswer
	if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"BOB","role":"gm"}`, w), &got); code != 200 || got != (MemberAnswer{Name: "bob", Role: "gm"}) {
		t.Fatalf("make bob GM = %d %+v", code, got)
	}
	if r, _ := f.st.Member(ctx, w, b.ID); r != store.MemberGM {
		t.Errorf("bob's role = %q", r)
	}
	if c, ok := bob.card(w); !ok || c.Role != "gm" || !c.Invite {
		t.Errorf("bob's card = %+v, %v", c, ok)
	}
	if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"bob","role":"none"}`, w), &got); code != 200 {
		t.Fatalf("remove bob = %d", code)
	}
	if _, ok := bob.card(w); ok {
		t.Error("bob still sees the private world")
	}
	want := []string{fmt.Sprint(w, " ", b.ID, " gm"), fmt.Sprint(w, " ", b.ID, " ")}
	if fmt.Sprint(live.roles) != fmt.Sprint(want) {
		t.Errorf("worlds told %q, want %q", live.roles, want)
	}
	log, _ := f.st.AuditLog(ctx, w, 10)
	if len(log) != 2 || log[0].Action != "role" || log[0].Detail != "bob: none" || log[1].Detail != "bob: gm" || log[0].Name != "root" {
		t.Errorf("audit = %+v", log)
	}

	// Only admins; nobody learns of a private world this way.
	for _, tt := range []struct {
		who  *browser
		body string
		code int
	}{
		{ann, fmt.Sprintf(`{"world":%d,"name":"bob","role":"gm"}`, w), 403},        // its GM is no admin
		{bob, fmt.Sprintf(`{"world":%d,"name":"bob","role":"gm"}`, w), 404},        // not a member any more
		{root, fmt.Sprintf(`{"world":%d,"name":"nobody","role":"gm"}`, w), 404},    // no such account
		{root, fmt.Sprintf(`{"world":%d,"name":"","role":"gm"}`, w), 404},          // no name
		{root, fmt.Sprintf(`{"world":%d,"name":"bob","role":"admin"}`, w), 400},    // not a world role
		{root, fmt.Sprintf(`{"world":%d,"name":"bob"}`, w), 400},                   // no role
		{root, `{"world":999999,"name":"bob","role":"gm"}`, 404},                   // no such world
		{root, fmt.Sprintf(`{"world":%d,"name":"bob","role":"gm","x":1}`, w), 400}, // unknown field
	} {
		if code := tt.who.post("/api/members", tt.body, nil); code != tt.code {
			t.Errorf("%s: %d, want %d", tt.body, code, tt.code)
		}
	}
	if len(live.roles) != 2 {
		t.Errorf("refused changes told the world: %q", live.roles)
	}
}

// Joining with a game master's code tells the world the account's new
// role, so a player who plays it now becomes its GM without a reload.
func TestJoinTellsTheWorld(t *testing.T) {
	live := &fakeLive{online: map[int64]int{}}
	f := newFixture(t, Config{Live: live})
	root := f.admin("root")
	cat := f.signUp("cat")
	var inv InviteAnswer
	if code := root.post("/api/invites", fmt.Sprintf(`{"world":%d,"gm":true}`, f.world.ID), &inv); code != 200 {
		t.Fatalf("GM invite = %d", code)
	}
	if code := cat.post("/api/join", `{"code":"`+inv.Code+`"}`, nil); code != 200 {
		t.Fatalf("join = %d", code)
	}
	c, _, _ := f.st.AccountByName(context.Background(), "cat")
	if want := fmt.Sprint(f.world.ID, " ", c.ID, " gm"); len(live.roles) != 1 || live.roles[0] != want {
		t.Errorf("worlds told %q, want %q", live.roles, want)
	}
}
