package account

import (
	"strings"
	"testing"
)

// A link opened in a signed-in browser answers with that browser's
// account: its own address once confirmed, or another account's page
// unchanged when it was someone else's link.
func TestVerifySignedIn(t *testing.T) {
	f, _, _ := mailFixture(t, Config{})
	mira, bob := f.player("mira"), f.player("bobby")
	for _, tc := range []struct {
		opener *browser
		name   string
		email  string
	}{
		{mira, "mira", "mira@example.org"},
		{bob, "bobby", ""},
	} {
		before := len(f.mails())
		if code := mira.post("/api/email", `{"email":"mira@example.org"}`, nil); code != 200 {
			t.Fatalf("POST /api/email = %d", code)
		}
		ms := f.mails()
		if len(ms) != before+1 {
			t.Fatalf("mails = %d", len(ms))
		}
		var me Me
		code := tc.opener.post("/api/verify", `{"code":"`+codeIn(t, ms[len(ms)-1], "verify")+`"}`, &me)
		if code != 200 || me.Account == nil || me.Account.Name != tc.name || me.Account.Email != tc.email || !strings.Contains(me.Notice, "mira is confirmed") {
			t.Errorf("verify in %s's browser = %d %+v %+v", tc.name, code, me.Account, me)
		}
	}
	var me Me
	if mira.get("/api/me", &me); me.Account.Email != "mira@example.org" {
		t.Errorf("mira's address = %q", me.Account.Email)
	}
}
