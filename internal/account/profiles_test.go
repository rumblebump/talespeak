package account

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/profile"
	"github.com/rumblebump/talespeak/internal/store"
)

// fakeProfiles gives every world of pack "test-pack" a profile card and
// refuses picks of the name "Locked".
type fakeProfiles struct {
	picks []string
}

func (p *fakeProfiles) Cards(_ context.Context, account int64, worlds []store.World) (map[int64]*profile.Card, error) {
	cards := map[int64]*profile.Card{}
	for _, w := range worlds {
		if w.PackID == "test-pack" {
			cards[w.ID] = &profile.Card{Ruleset: "Tiny Rules 1.0.0", Options: []profile.Option{}, New: true}
		}
	}
	return cards, nil
}

func (p *fakeProfiles) Pick(_ context.Context, account int64, name string, w store.World, id int64, newName string) (profile.Option, error) {
	if newName == "Locked" {
		return profile.Option{}, &profile.Refusal{Reason: "Finish Forest Start first."}
	}
	p.picks = append(p.picks, name+":"+strconv.FormatInt(w.ID, 10)+":"+strconv.FormatInt(id, 10)+":"+newName)
	return profile.Option{ID: 1, Name: newName, OK: true}, nil
}

func TestProfilesInTheLobby(t *testing.T) {
	fp := &fakeProfiles{}
	f := newFixture(t, Config{Profiles: fp})
	ann, bob := f.signUp("ann"), f.signUp("bob")
	made, code := ann.create(`{"pack":"test-pack","visibility":"private"}`)
	if code != 200 {
		t.Fatalf("create = %d", code)
	}
	id := made.World.ID
	c, ok := ann.card(id)
	if !ok || c.Profiles == nil || c.Profiles.Ruleset != "Tiny Rules 1.0.0" || !c.Profiles.New {
		t.Fatalf("card = %+v", c)
	}
	if main, _ := ann.card(0); main.Profiles != nil {
		t.Errorf("a world without a ruleset has profiles: %+v", main.Profiles)
	}

	var l Lobby
	if code := ann.post("/api/profiles", `{"world":`+strconv.FormatInt(id, 10)+`,"profile":0,"name":"Hero"}`, &l); code != 200 || len(l.Worlds) == 0 {
		t.Errorf("pick = %d, %+v", code, l)
	}
	if len(fp.picks) != 1 || fp.picks[0] != "ann:"+strconv.FormatInt(id, 10)+":0:Hero" {
		t.Errorf("picks = %v", fp.picks)
	}
	var e struct{ Error string }
	if code := ann.post("/api/profiles", `{"world":`+strconv.FormatInt(id, 10)+`,"name":"Locked"}`, &e); code != 409 || e.Error != "Finish Forest Start first." {
		t.Errorf("refused = %d, %q", code, e.Error)
	}
	// Bob is no member of the private world: it does not exist for him.
	if code := bob.post("/api/profiles", `{"world":`+strconv.FormatInt(id, 10)+`,"name":"Hero"}`, &e); code != 404 {
		t.Errorf("not a member = %d", code)
	}
	for _, body := range []string{`{"world":0,"name":"x"}`, `{"world":999,"name":"x"}`} {
		if code := ann.post("/api/profiles", body, nil); code != 404 {
			t.Errorf("%s = %d", body, code)
		}
	}
	for _, body := range []string{`{"world":1,"profile":-1}`, `{"world":1,"nope":1}`, `{"world":1,"name":"` + strings.Repeat("a", 100) + `"}`} {
		if code := ann.post("/api/profiles", body, nil); code != 400 {
			t.Errorf("%s = %d", body, code)
		}
	}
	if code := f.browser().post("/api/profiles", `{"world":1}`, nil); code != 401 {
		t.Errorf("signed out = %d", code)
	}
	if len(fp.picks) != 1 {
		t.Errorf("picks = %v", fp.picks)
	}
}

func TestProfilesOff(t *testing.T) {
	f := newFixture(t, Config{})
	ann := f.signUp("ann")
	var e struct{ Error string }
	if code := ann.post("/api/profiles", `{"world":`+strconv.FormatInt(f.world.ID, 10)+`,"name":"Hero"}`, &e); code != 400 || e.Error != "This world needs no character." {
		t.Errorf("pick = %d, %q", code, e.Error)
	}
	if c, _ := ann.card(0); c.Profiles != nil {
		t.Errorf("card = %+v", c)
	}
}
