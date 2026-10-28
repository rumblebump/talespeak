package game

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The lowest state limits a server may set (script.MinStateLimits) still
// hold every setting a pack may have, at their longest: the world loads
// with all of them, /set changes them when state.game is full, and game
// masters' hooks keep only what is left.
func TestSettingsAtLowestStateLimits(t *testing.T) {
	var y strings.Builder
	y.WriteString("id: t\nversion: 1.0.0\ntitle: T\nstart: a\nsettings:\n")
	long := func(prefix string, i int) string {
		s := fmt.Sprintf("%s%02d", prefix, i)
		return s + strings.Repeat("x", pack.MaxSettingName-len(s))
	}
	for i := range pack.MaxSettings {
		fmt.Fprintf(&y, "  - name: %s\n    label: Setting %d\n    choices: [%s, %s]\n", long("s", i), i, long("a", i), long("b", i))
	}
	fsys := roomsFS(gameRoom, `function on_enter() end`)
	fsys["pack.yaml"] = &fstest.MapFile{Data: []byte(y.String())}
	p, err := pack.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	limits := script.DefaultLimits
	limits.State = script.MinStateLimits
	w, err := NewWorld(p, limits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	for i := range pack.MaxSettings {
		if got := say1(t, gm, "get "+long("s", i)); got != long("a", i) {
			t.Fatalf("setting %d = %q", i, got)
		}
	}
	// The other keys fill state.game up to its key count; settings change
	// in place.
	free := script.MinStateLimits.GameKeys - pack.MaxSettings
	for i := range free {
		// One write per hook at the lowest limits.
		if got := say1(t, gm, fmt.Sprintf("set k%d v", i)); got != "true nil" {
			t.Fatalf("key %d of %d: %q", i+1, free, got)
		}
	}
	if got := say1(t, gm, "set one more"); got != "nil state.game.set: more than 30 keys" {
		t.Errorf("past the key count: %q", got)
	}
	if got := say1(t, gm, "set "+long("s", 3)+" "+long("b", 3)); got != "true nil" {
		t.Errorf("a setting with state.game full: %q", got)
	}
	got := strings.Join(notices(input(gm, "/set "+long("s", 4)+" "+long("b", 4))), "|")
	if !strings.Contains(got, `set "Setting 4" to `+long("b", 4)) {
		t.Errorf("/set with state.game full: %q", got)
	}
	if got := say1(t, gm, "get "+long("s", 4)); got != long("b", 4) {
		t.Errorf("after /set: %q", got)
	}
}
