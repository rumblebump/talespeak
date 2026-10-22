package account

import (
	"net/http/httptest"
	"testing"
)

// T-0036: the WebSocket sign-in tells the world who its game masters are
// (world_members.role gm), for player.gm. Admins who are not members, and
// players who joined with a code, are not.
func TestAuthGM(t *testing.T) {
	f := newFixture(t, Config{})
	ann, bob := f.signUp("ann"), f.signUp("bob")
	root := f.admin("root")
	made, _ := ann.create(`{"pack":"demo","visibility":"private"}`)
	id := made.World.ID
	bob.post("/api/join", `{"code":"`+made.Invite.Code+`"}`, nil)
	for _, tt := range []struct {
		name string
		b    *browser
		gm   bool
	}{{"creator", ann, true}, {"joined with a code", bob, false}, {"admin, not a member", root, false}} {
		req := httptest.NewRequest("GET", f.ws(id), nil)
		req.AddCookie(tt.b.cookie)
		who, status := f.svc.Auth(req)
		if status != 0 || who.World != id || who.GM != tt.gm {
			t.Errorf("%s: Auth = %+v, %d; want GM %v", tt.name, who, status, tt.gm)
		}
	}
}
