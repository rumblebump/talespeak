package script

import (
	"strings"
	"testing"
	"time"
)

// T-0012 at the script level: game-time timers, world.paused() and
// world.time() against fake hosts, with and without Clock.

// clockHost is the party host with a world clock: game timers are
// recorded with a "game" prefix.
type clockHost struct {
	partyHost
	paused bool
	game   time.Duration
}

func (h *clockHost) Paused() bool { return h.paused }

func (h *clockHost) GameTime() time.Duration { return h.game }

func (h *clockHost) StartGameTimer(name string, d time.Duration, show string, room bool) error {
	h.out = append(h.out, "gametimer:"+name+"="+d.String()+"|"+show+"|room="+map[bool]string{true: "yes", false: "no"}[room])
	return nil
}

func runClock(t *testing.T, h Host, src string) ([]string, error) {
	t.Helper()
	r := New(roomy)
	t.Cleanup(r.Close)
	env := load(t, r, src)
	_, err := r.RunHook(h, env, Hook{Name: "on_enter", Player: "Ada", PlayerID: "p7"})
	switch h := h.(type) {
	case *clockHost:
		return h.out, err
	case *recorder:
		return h.out, err
	}
	return nil, err
}

func TestGameTimers(t *testing.T) {
	src := `function on_enter(p)
  timer.start("tick", 30, {game = true, show = "Tick"})
  timer.start("sand", 5, {game = true, room = true})
  timer.start("real", 5)
  say(tostring(world.paused()) .. " " .. tostring(world.time()))
end`
	h := &clockHost{paused: true, game: 1500 * time.Millisecond}
	out, err := runClock(t, h, src)
	want := "gametimer:tick=30s|Tick|room=no;gametimer:sand=5s||room=yes;timer:real=5s|;say:true 1.5"
	if err != nil || strings.Join(out, ";") != want {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// A host without a clock never pauses and has no game time; a game timer
// is a script error there.
func TestNoClock(t *testing.T) {
	out, err := runClock(t, &recorder{}, `function on_enter(p) say(tostring(world.paused()) .. " " .. tostring(world.time())) end`)
	if err != nil || strings.Join(out, ";") != "say:false 0" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	if _, err := runClock(t, &recorder{}, `function on_enter(p) timer.start("tick", 5, {game = true}) end`); err == nil || !strings.Contains(err.Error(), "no game time") {
		t.Errorf("a game timer without a clock: %v", err)
	}
	if _, err := runClock(t, &clockHost{}, `function on_enter(p) timer.start("tick", 5, {game = "yes"}) end`); err == nil || !strings.Contains(err.Error(), "game must be a boolean") {
		t.Errorf("a bad game option: %v", err)
	}
}
