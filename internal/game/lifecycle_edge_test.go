package game

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// demoWorldClock is demoWorld on the clock c.
func demoWorldClock(t *testing.T, c *fakeClock) *World {
	t.Helper()
	p, err := pack.Load(os.DirFS("testdata/dialog"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithClock(c.Now, c.After))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// CloseIfEmpty closes a world nobody plays, whatever the time, and a
// player of a world closed that way gets a session that does nothing.
func TestCloseIfEmpty(t *testing.T) {
	w := demoWorld(t)
	a, _ := w.NewSession("", ws.Who{})
	if w.CloseIfEmpty() {
		t.Fatal("closed with a player")
	}
	a.Close()
	if !w.CloseIfEmpty() || !w.Closed() {
		t.Fatal("an empty world did not close")
	}
	if w.CloseIfEmpty() {
		t.Error("closed twice")
	}
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if r := s.Handle(ws.Message{Type: "input", Text: "look"}); !isError(r) {
		t.Errorf("session of a closed world answers %+v", r)
	}
}

// End on a world nobody plays closes it; End after Close and End twice
// are harmless.
func TestEndEmptyAndTwice(t *testing.T) {
	w := demoWorld(t)
	w.End("bye")
	if !w.Closed() || w.Online() != 0 {
		t.Errorf("after End: closed %v, online %d", w.Closed(), w.Online())
	}
	w.End("bye again")
	w.Close()

	v := demoWorld(t)
	s, _ := v.NewSession("", ws.Who{})
	v.Close()
	v.End("late") // the session left with Close: nothing more to tell
	if ms, ok := s.(ws.Pusher).Pending(); ok && len(ms) > 0 && ms[0].Message == "late" {
		t.Errorf("End after Close told a session: %+v", ms)
	}
	s.Close()
}

// The last player leaving restarts the idle time; one of several leaving
// does not.
func TestIdleSinceLastLeaver(t *testing.T) {
	c := newClock()
	w := demoWorldClock(t, c)
	a, _ := w.NewSession("", ws.Who{})
	c.Advance(time.Minute)
	a.Close()
	first := c.Now()
	c.Advance(time.Minute)
	b, _ := w.NewSession("", ws.Who{})
	b2, _ := w.NewSession("", ws.Who{})
	c.Advance(time.Minute)
	b.Close()
	if _, ok := w.IdleSince(); ok {
		t.Fatal("idle with a player in")
	}
	c.Advance(time.Minute)
	b2.Close()
	if since, ok := w.IdleSince(); !ok || !since.Equal(c.Now()) || since.Equal(first) {
		t.Errorf("IdleSince = %v, %v; want %v", since, ok, c.Now())
	}
	// A session closed twice does not move the time again.
	at := c.Now()
	c.Advance(time.Minute)
	b2.Close()
	if since, _ := w.IdleSince(); !since.Equal(at) {
		t.Errorf("IdleSince moved to %v on a second Close", since)
	}
}

// Players joining while CloseIfEmpty and CloseIfIdle run: a session
// from NewSession, taken while the world was open, keeps working until it
// closes, since a world with a player never closes that way.
func TestCloseIfEmptyRacesJoins(t *testing.T) {
	w := demoWorld(t)
	anchor, _ := w.NewSession("", ws.Who{}) // keeps the world open for the first half
	var turns atomic.Int32
	var dropAnchor sync.Once
	stop := make(chan struct{})
	var closer sync.WaitGroup
	closer.Add(1)
	go func() {
		defer closer.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if w.CloseIfIdle(time.Now().Add(time.Hour)) {
					return
				}
				if w.CloseIfEmpty() {
					return
				}
			}
		}
	}()
	var wg sync.WaitGroup
	var dead atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				s, _ := w.NewSession("", ws.Who{})
				// Closing is for good and leaves nobody in, so a player
				// counted now means the world was open when s came: s is
				// in it and must work.
				if w.Online() > 0 {
					if r := s.Handle(ws.Message{Type: "input", Text: "look"}); isError(r) && r[0].Message == playerError(script.ErrClosed) {
						dead.Add(1)
					}
				}
				s.Close()
				if turns.Add(1) == 80 {
					dropAnchor.Do(anchor.Close)
				}
			}
		}()
	}
	wg.Wait()
	dropAnchor.Do(anchor.Close)
	close(stop)
	closer.Wait()
	if dead.Load() != 0 {
		t.Errorf("%d sessions of an open world did not work", dead.Load())
	}
	if !w.Closed() && !w.CloseIfEmpty() {
		t.Error("the world did not close once everyone left")
	}
}

// A world with a store that closed (ended, or unloaded by the hub)
// refuses a player instead of loading their character, so nothing is
// written for a world that may be finished.
func TestClosedStoreWorldRefuses(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	for _, end := range []func(*World){(*World).Close, func(w *World) { w.End("bye") }} {
		w := storeWorld(t, st, world)
		end(w)
		s, ms := w.NewSession("192.0.2.1", who[0])
		if s != nil || len(ms) != 1 || ms[0].Type != "error" || ms[0].Message != ErrClosedMessage {
			t.Errorf("join of a closed world = %v, %+v", s, ms)
		}
		if w.Online() != 0 {
			t.Errorf("online %d", w.Online())
		}
	}
	list, err := st.ListWorlds(context.Background(), who[0].Account, true, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, wi := range list {
		if wi.ID == world.ID && wi.Players != 0 {
			t.Errorf("a character was made in a closed world: %d", wi.Players)
		}
	}
}
