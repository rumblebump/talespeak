package account

import (
	"context"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/script"
)

// settingsPacks offer the demo with a death setting.
var settingsPacks = []Pack{{ID: "demo", Title: "The damp cell", Settings: []Setting{
	{Name: "death", Label: "When a player dies", Choices: []string{"respawn", "revive", "stay"}, Default: "respawn"},
	{Name: "hints", Label: "Hints", Choices: []string{"on", "off"}, Default: "on"},
}}}

// A new world keeps the settings picked in the lobby, and the defaults
// of the others, as its first state.game; the lobby lists the settings.
func TestCreateWorldSettings(t *testing.T) {
	f := newFixture(t, Config{Packs: settingsPacks})
	ann := f.signUp("ann")
	var l Lobby
	if code := ann.get("/api/worlds", &l); code != 200 || len(l.Packs) != 1 || len(l.Packs[0].Settings) != 2 || l.Packs[0].Settings[0].Default != "respawn" {
		t.Fatalf("lobby = %d %+v", code, l.Packs)
	}
	made, code := ann.create(`{"pack":"demo","settings":{"death":"stay"}}`)
	if code != 200 {
		t.Fatalf("create = %d", code)
	}
	vals, err := f.st.GameState(context.Background(), made.World.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(vals["death"]) != string(script.EncodeString("stay")) || string(vals["hints"]) != string(script.EncodeString("on")) || len(vals) != 2 {
		t.Errorf("game state = %q", vals)
	}
}

func TestCreateWorldSettingsRefused(t *testing.T) {
	f := newFixture(t, Config{Packs: settingsPacks})
	ann := f.signUp("ann")
	for body, want := range map[string]string{
		`{"pack":"demo","settings":{"death":"zombie"}}`:                       "Pick one of the choices for When a player dies: respawn, revive, stay.",
		`{"pack":"demo","settings":{"color":"red"}}`:                          `This pack has no setting "color".`,
		`{"pack":"demo","settings":{"death":["stay"]}}`:                       "",
		`{"pack":"demo","settings":"death=stay"}`:                             "",
		`{"pack":"demo","settings":{"` + strings.Repeat("x", 100) + `":"a"}}`: `This pack has no setting "` + strings.Repeat("x", 32) + `…".`,
	} {
		var e map[string]string
		if code := ann.post("/api/worlds", body, &e); code != 400 || (want != "" && e["error"] != want) {
			t.Errorf("%s = %d %q, want 400 %q", body, code, e["error"], want)
		}
	}
	var l Lobby
	ann.get("/api/worlds", &l)
	for _, c := range l.Worlds {
		if c.GM == "ann" {
			t.Errorf("a refused request created %+v", c)
		}
	}
}
