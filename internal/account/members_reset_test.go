package account

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// whoAt is what the WebSocket's sign-in check says of b at path, a
// WebSocket URL path with its query.
func (b *browser) whoAt(path string) (ws.Who, int) {
	b.f.t.Helper()
	req, _ := http.NewRequest("GET", b.f.srv.URL+path, nil)
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

// Reset codes (T-0013) follow the members list (T-0011) as it changes: on
// a school server a game master makes codes only for the plain players of
// worlds they run now, so promoting, removing or demoting a member, or
// ending the world, changes who gets one.
func TestResetCodesFollowMembers(t *testing.T) {
	f := newInviteFixture(t, Config{Edu: true})
	root := f.admin("root")
	teach, kid := f.signUp("teach"), f.signUp("kid")
	w1, _ := root.create(`{"pack":"demo"}`)
	w2, _ := root.create(`{"pack":"demo"}`)
	role := func(world int64, name, role string) {
		t.Helper()
		if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":%q,"role":%q}`, world, name, role), nil); code != 200 {
			t.Fatalf("%s %s in %d = %d", name, role, world, code)
		}
	}
	codeFor := func(step string, want int) {
		t.Helper()
		var e map[string]string
		if code := teach.post("/api/resetcode", `{"name":"kid"}`, &e); code != want {
			t.Errorf("%s: a code for kid = %d %v, want %d", step, code, e, want)
		}
	}
	makes := func(step string, want bool) {
		t.Helper()
		var me Me
		if teach.get("/api/me", &me); me.Account == nil || me.Account.ResetCodes != want {
			t.Errorf("%s: me = %+v, want ResetCodes %v", step, me.Account, want)
		}
	}
	role(w1.World.ID, "teach", "gm")
	role(w1.World.ID, "kid", "player")
	codeFor("kid plays teach's world", 200)
	makes("teach runs a world", true)

	// Teach's code resets kid's password and ends kid's sign-ins, so the
	// games they play close at the next recheck if no sign-out reached them.
	var rc ResetCodeAnswer
	if code := teach.post("/api/resetcode", `{"name":"kid"}`, &rc); code != 200 {
		t.Fatalf("code = %d", code)
	}
	if _, code := kid.whoAt(f.ws(w1.World.ID)); code != 200 {
		t.Fatalf("kid signed in = %d", code)
	}
	if code := f.browser().post("/api/reset", `{"code":"`+rc.Code+`","password":"a new password"}`, nil); code != 200 {
		t.Fatalf("reset = %d", code)
	}
	if _, code := kid.whoAt(f.ws(w1.World.ID)); code != 401 {
		t.Errorf("kid's old sign-in after the reset = %d", code)
	}

	role(w2.World.ID, "kid", "gm")
	codeFor("kid runs another world", 403)
	role(w2.World.ID, "kid", "none")
	codeFor("kid runs no world again", 200)

	role(w1.World.ID, "kid", "none")
	codeFor("kid left teach's world", 403)
	role(w1.World.ID, "kid", "player")

	role(w1.World.ID, "teach", "player")
	codeFor("teach demoted", 403)
	makes("teach demoted", false)
	if code := teach.get(fmt.Sprintf("/api/members?world=%d", w1.World.ID), nil); code != 403 {
		t.Errorf("a demoted GM lists the members: %d", code)
	}
	if code := teach.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"kid","role":"none"}`, w1.World.ID), nil); code != 403 {
		t.Errorf("a demoted GM changes a role: %d", code)
	}

	role(w1.World.ID, "teach", "gm")
	codeFor("teach a GM again", 200)
	if code := root.post("/api/finish", fmt.Sprintf(`{"world":%d}`, w1.World.ID), nil); code != 200 {
		t.Fatalf("finish = %d", code)
	}
	codeFor("teach's world ended", 403)
	makes("teach's world ended", false)
}

// A game master demoted in a world signs in to it as a player from then
// on: the WebSocket's sign-in check (what the recheck asks too) no longer
// gives GM, with or without as=player.
func TestDemotedGMSignsInAsPlayer(t *testing.T) {
	f := newFixture(t, Config{})
	root := f.admin("root")
	ann := f.signUp("ann")
	made, _ := ann.create(`{"pack":"demo","visibility":"private"}`)
	id := made.World.ID
	check := func(step string, query string, gm bool) {
		t.Helper()
		req := f.ws(id) + query
		who, code := ann.whoAt(req)
		if code != 200 || who.GM != gm {
			t.Errorf("%s %s: %+v %d, want GM %v", step, query, who, code, gm)
		}
	}
	check("creator", "", true)
	check("creator", "&as=player", true)
	if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"ann","role":"player"}`, id), nil); code != 200 {
		t.Fatalf("demote = %d", code)
	}
	check("demoted", "", false)
	check("demoted", "&as=player", false)
	if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":"ann","role":"none"}`, id), nil); code != 200 {
		t.Fatalf("remove = %d", code)
	}
	if _, code := ann.whoAt(f.ws(id)); code == 200 {
		t.Error("a member taken out of a private world still signs in to it")
	}
}

// A code a game master made works only while its maker may still make
// it: once the player became a GM, or left the maker's worlds, it is used
// up and refused. An admin's code always works.
func TestResetCodeCheckedWhenUsed(t *testing.T) {
	f := newInviteFixture(t, Config{Edu: true})
	root := f.admin("root")
	teach := f.signUp("teach")
	f.signUp("kid")
	f.signUp("kim")
	w, _ := root.create(`{"pack":"demo"}`)
	role := func(name, role string) {
		t.Helper()
		if code := root.post("/api/members", fmt.Sprintf(`{"world":%d,"name":%q,"role":%q}`, w.World.ID, name, role), nil); code != 200 {
			t.Fatalf("%s %s = %d", name, role, code)
		}
	}
	codeFor := func(by *browser, name string) string {
		t.Helper()
		var rc ResetCodeAnswer
		if code := by.post("/api/resetcode", fmt.Sprintf(`{"name":%q}`, name), &rc); code != 200 {
			t.Fatalf("a code for %s = %d", name, code)
		}
		return rc.Code
	}
	reset := func(code string) int {
		return f.browser().post("/api/reset", `{"code":"`+code+`","password":"a new password"}`, nil)
	}
	role("teach", "gm")
	role("kid", "player")
	role("kim", "player")

	promoted := codeFor(teach, "kid")
	role("kid", "gm")
	if code := reset(promoted); code != 400 {
		t.Errorf("a GM's code for a player made a GM since = %d, want 400", code)
	}
	removed := codeFor(teach, "kim")
	role("kim", "none")
	if code := reset(removed); code != 400 {
		t.Errorf("a GM's code for a player who left = %d, want 400", code)
	}
	if code := reset(codeFor(root, "kid")); code != 200 {
		t.Errorf("an admin's code for a GM = %d, want 200", code)
	}
}
