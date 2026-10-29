package game

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
	"github.com/rumblebump/talespeak/internal/ws"
)

// timeStoreWorld is a world of timeRoom with st on c's clock.
func timeStoreWorld(t *testing.T, st store.Worlds, world store.World, c *fakeClock) *World {
	t.Helper()
	p, err := pack.Load(roomsFS(timeRoom, timerRoomB))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithStore(st, world.ID), WithClock(c.Now, c.After))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// timeStore opens a store with Mira as the world's game master and Zed
// as a player.
func timeStore(t *testing.T, path string) (*sqlite.Store, store.World, ws.Who, ws.Who) {
	t.Helper()
	st, world, who := openStore(t, path)
	setRole(t, st, world, who[0], store.MemberGM)
	setRole(t, st, world, who[1], store.MemberPlayer)
	return st, world, who[0], who[1]
}

// A paused world stays paused through a restart: the timers of a player
// who is away wait too, and go on with their time left after /resume.
func TestPauseLastsThroughRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, world, mira, zed := timeStore(t, path)
	c := newClock()
	w := timeStoreWorld(t, st, world, c)
	z, _ := w.NewSession("", zed)
	input(z, "start")
	c.Advance(10 * time.Second)
	z.Close() // away: the timer waits in the store
	m, _ := w.NewSession("", mira)
	input(m, "/pause")
	c.Advance(10 * time.Minute)
	w.Close()
	st.Close()

	st, world, mira, zed = timeStore(t, path)
	clk, err := st.WorldClock(context.Background(), world.ID)
	if err != nil || clk.PausedAt.IsZero() {
		t.Fatalf("the stored clock: %+v, %v", clk, err)
	}
	c.Advance(time.Hour)
	w = timeStoreWorld(t, st, world, c)
	z, first := w.NewSession("", zed)
	defer z.Close()
	if len(first) == 0 || first[0].Type != "pause" || !first[0].Paused {
		t.Fatalf("joining after the restart: %+v", first)
	}
	if got := ms(t, first, "guards"); got != 50000 {
		t.Errorf("the guards' countdown has %d ms", got)
	}
	m, _ = w.NewSession("", mira)
	defer m.Close()
	input(m, "/resume")
	c.Advance(49 * time.Second)
	if got := texts(pending(t, z)); got != "pause | notice | timer" {
		t.Fatalf("before the deadline: %q", got)
	}
	c.Advance(time.Second)
	if got := texts(pending(t, z)); got != "timer | up guards nil" {
		t.Errorf("at the deadline: %q", got)
	}
}

// Game time and game-time timers last: the clock counts only while
// someone plays, and its time is written as the world closes.
func TestGameTimeLasts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, world, _, zed := timeStore(t, path)
	c := newClock()
	w := timeStoreWorld(t, st, world, c)
	z, _ := w.NewSession("", zed)
	input(z, "game")
	c.Advance(20 * time.Second)
	w.Close() // the server stops
	st.Close()

	st, world, _, zed = timeStore(t, path)
	clk, err := st.WorldClock(context.Background(), world.ID)
	if err != nil || clk.Game != 20*time.Second {
		t.Fatalf("the stored game time: %+v, %v", clk, err)
	}
	c.Advance(time.Hour)
	w = timeStoreWorld(t, st, world, c)
	z, first := w.NewSession("", zed)
	defer z.Close()
	if got := ms(t, first, "tick"); got != 40000 {
		t.Errorf("tick has %d ms left", got)
	}
	if got := texts(input(z, "time")); got != "20" {
		t.Errorf("world.time() = %s", got)
	}
	c.Advance(40 * time.Second)
	if got := texts(pending(t, z)); got != "timer | up tick nil" {
		t.Errorf("at the deadline: %q", got)
	}
}

// /extratime is stored with the member and counts when they come back;
// /mintime lasts with the world; a name that is no member is refused.
func TestTimeSettingsLast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, world, mira, zed := timeStore(t, path)
	c := newClock()
	w := timeStoreWorld(t, st, world, c)
	m, _ := w.NewSession("", mira)
	z, _ := w.NewSession("", zed)
	input(m, "/extratime zed 2")
	if got := ms(t, input(z, "start"), "guards"); got != 120000 {
		t.Errorf("the online player's timer runs %d ms", got)
	}
	if got := notices(input(m, "/extratime nobody 2")); len(got) != 1 || got[0] != `server: There is no member "nobody" in this world.` {
		t.Errorf("no member: %v", got)
	}
	input(m, "/mintime 5m")
	z.Close()
	m.Close()
	w.Close()
	st.Close()

	st, world, _, zed = timeStore(t, path)
	if f, err := st.ExtraTime(context.Background(), world.ID, zed.Account); err != nil || f != 2 {
		t.Errorf("stored extra time %v, %v", f, err)
	}
	w = timeStoreWorld(t, st, world, c)
	z, _ = w.NewSession("", zed)
	defer z.Close()
	got := input(z, "ask")
	if len(got) != 1 || got[0].Ms != 300000 {
		t.Errorf("a question after the restart: %+v", got)
	}
}
