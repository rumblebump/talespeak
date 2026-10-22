package hub

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// worlds is a store of worlds by id.
type worlds struct {
	mu sync.Mutex
	m  map[int64]store.World
}

func (ws *worlds) World(_ context.Context, id int64) (store.World, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	w, ok := ws.m[id]
	if !ok {
		return store.World{}, store.ErrNotFound
	}
	return w, nil
}

func (ws *worlds) finish(id int64) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	w := ws.m[id]
	w.Finished = true
	ws.m[id] = w
}

type fixture struct {
	hub   *Hub
	st    *worlds
	loads atomic.Int32
	fail  atomic.Bool // the loader fails
	now   time.Time
}

func newFixture(t *testing.T, maxLoaded int) *fixture {
	t.Helper()
	p, err := pack.Load(os.DirFS("../../content/test-pack"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{now: time.Now()}
	f.st = &worlds{m: map[int64]store.World{
		1: {ID: 1, PackID: "test-pack"},
		2: {ID: 2, PackID: "test-pack"},
		3: {ID: 3, PackID: "test-pack"},
		9: {ID: 9, PackID: "removed"},
	}}
	f.hub = New(Config{Store: f.st, Packs: map[string]*pack.Pack{"test-pack": p}, MaxLoaded: maxLoaded, Idle: time.Minute, Log: quiet,
		Now: func() time.Time { return f.now },
		Load: func(w store.World, p *pack.Pack) (*game.World, error) {
			f.loads.Add(1)
			if f.fail.Load() {
				return nil, errors.New("broken script")
			}
			return game.NewWorld(p, script.DefaultLimits, quiet)
		}})
	t.Cleanup(f.hub.Close)
	return f
}

// join starts a session in world, or returns the refusal's message.
func (f *fixture) join(t *testing.T, world int64) (ws.Session, string) {
	t.Helper()
	s, ms := f.hub.NewSession("", ws.Who{World: world})
	if s == nil {
		if len(ms) != 1 || ms[0].Type != "error" {
			t.Fatalf("refusal = %+v", ms)
		}
		return nil, ms[0].Message
	}
	if len(ms) == 0 || ms[0].Type != "room" || ms[0].ID != "hall" {
		t.Fatalf("first messages = %+v", ms)
	}
	return s, ""
}

// A world loads on its first player, and every later player of it plays
// the same one.
func TestLoadsOnce(t *testing.T) {
	f := newFixture(t, 0)
	if f.hub.Loaded() != 0 || f.hub.Online(1) != 0 {
		t.Fatal("a world is loaded before anyone plays")
	}
	a, _ := f.join(t, 1)
	b, _ := f.join(t, 1)
	if f.loads.Load() != 1 || f.hub.Loaded() != 1 || f.hub.Online(1) != 2 || f.hub.Online(2) != 0 {
		t.Errorf("loads %d, loaded %d, online %d", f.loads.Load(), f.hub.Loaded(), f.hub.Online(1))
	}
	a.Close()
	b.Close()
	if f.hub.Online(1) != 0 || f.hub.Loaded() != 1 {
		t.Errorf("after both left: online %d, loaded %d", f.hub.Online(1), f.hub.Loaded())
	}
}

// Players arriving at once at a world that is not loaded wait for one
// load.
func TestLoadsOnceConcurrently(t *testing.T) {
	f := newFixture(t, 0)
	var wg sync.WaitGroup
	sessions := make(chan ws.Session, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _ := f.hub.NewSession("", ws.Who{World: 2})
			sessions <- s
		}()
	}
	wg.Wait()
	close(sessions)
	for s := range sessions {
		if s == nil {
			t.Error("a player was refused")
			continue
		}
		s.Close()
	}
	if f.loads.Load() != 1 {
		t.Errorf("%d loads, want 1", f.loads.Load())
	}
}

// Worlds that do not exist, have ended, have no pack or do not load are
// refused with a message, and nothing stays loaded.
func TestRefuses(t *testing.T) {
	f := newFixture(t, 0)
	f.st.finish(3)
	for _, tt := range []struct {
		world int64
		want  error
	}{
		{404, errEnded},
		{0, errEnded},
		{3, errEnded},
		{9, errNoPack},
	} {
		if s, msg := f.join(t, tt.world); s != nil || msg != tt.want.Error() {
			t.Errorf("world %d: %q, want %q", tt.world, msg, tt.want)
		}
	}
	f.fail.Store(true)
	if _, msg := f.join(t, 1); msg != errLoad.Error() {
		t.Errorf("broken world: %q", msg)
	}
	if f.hub.Loaded() != 0 {
		t.Errorf("%d worlds loaded after refusals", f.hub.Loaded())
	}
	// A failed load is tried again by the next player.
	f.fail.Store(false)
	if s, msg := f.join(t, 1); s == nil {
		t.Errorf("after the pack was fixed: %q", msg)
	}
}

// Reap unloads a world nobody has played for the idle time, and keeps the
// others.
func TestReap(t *testing.T) {
	f := newFixture(t, 0)
	a, _ := f.join(t, 1)
	b, _ := f.join(t, 2)
	a.Close()
	f.hub.Reap()
	if f.hub.Loaded() != 2 {
		t.Fatalf("a world left a moment ago unloaded: %d loaded", f.hub.Loaded())
	}
	f.now = f.now.Add(2 * time.Minute)
	f.hub.Reap()
	if f.hub.Loaded() != 1 || f.hub.Online(2) != 1 {
		t.Errorf("after the idle time: %d loaded, %d online in world 2; want world 1 gone", f.hub.Loaded(), f.hub.Online(2))
	}
	// The next player of world 1 loads it again.
	c, _ := f.join(t, 1)
	if f.loads.Load() != 3 {
		t.Errorf("%d loads, want 3", f.loads.Load())
	}
	b.Close()
	c.Close()
}

// Past the cap, the world empty the longest unloads for a new one; with
// every loaded world played, the new one is refused.
func TestMaxLoaded(t *testing.T) {
	f := newFixture(t, 2)
	a, _ := f.join(t, 1)
	b, _ := f.join(t, 2)
	if _, msg := f.join(t, 3); msg != errFull.Error() {
		t.Fatalf("third world with two played: %q", msg)
	}
	a.Close()
	time.Sleep(2 * time.Millisecond) // world 1 empty longer than world 2
	b.Close()
	c, msg := f.join(t, 3)
	if c == nil {
		t.Fatalf("third world after both left: %q", msg)
	}
	if f.hub.Loaded() != 2 {
		t.Errorf("%d loaded, want 2", f.hub.Loaded())
	}
	// World 2 is still loaded, world 1 went for world 3.
	loads := f.loads.Load()
	d, _ := f.join(t, 2)
	if f.loads.Load() != loads {
		t.Error("world 2 was unloaded instead of world 1")
	}
	c.Close()
	d.Close()
}

// Finish sends the players away, unloads the world and keeps it from
// loading while the store marks it finished.
func TestFinish(t *testing.T) {
	f := newFixture(t, 0)
	s, _ := f.join(t, 1)
	p := s.(ws.Pusher)
	kicked := make(chan struct{}, 1)
	p.SetNotify(func() {
		select {
		case kicked <- struct{}{}:
		default:
		}
	})
	ran := false
	err := f.hub.Finish(1, func() error {
		ran = true
		if _, msg := f.join(t, 1); msg != errEnded.Error() {
			t.Errorf("joining while it ends: %q", msg)
		}
		if err := f.hub.Finish(1, func() error { return nil }); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ending it twice at once = %v", err)
		}
		f.st.finish(1)
		return nil
	})
	if err != nil || !ran {
		t.Fatalf("Finish = %v, ran %v", err, ran)
	}
	select {
	case <-kicked:
	case <-time.After(time.Second):
		t.Fatal("the player was not told")
	}
	ms, ok := p.Pending()
	if ok || len(ms) != 1 || ms[0].Message != EndedMessage {
		t.Errorf("Pending = %+v, %v; want the ended message and the end", ms, ok)
	}
	if f.hub.Loaded() != 0 {
		t.Errorf("%d loaded after Finish", f.hub.Loaded())
	}
	if _, msg := f.join(t, 1); msg != errEnded.Error() {
		t.Errorf("joining after it ended: %q", msg)
	}
	s.Close() // the connection closing after the end is harmless
	// A world that is not loaded just runs finish; its error comes back.
	boom := errors.New("disk")
	if err := f.hub.Finish(2, func() error { return boom }); !errors.Is(err, boom) {
		t.Errorf("Finish of an unloaded world = %v", err)
	}
}

// After Close nothing loads.
func TestClose(t *testing.T) {
	f := newFixture(t, 0)
	s, _ := f.join(t, 1)
	f.hub.Close()
	if r := s.Handle(ws.Message{Type: "input", Text: "look"}); len(r) == 0 || r[0].Type != "error" {
		t.Errorf("a session of a closed world answers %+v", r)
	}
	if _, msg := f.join(t, 2); msg != errClosed.Error() {
		t.Errorf("after Close: %q", msg)
	}
	s.Close()
}

// Run reaps until its context ends.
func TestRun(t *testing.T) {
	f := newFixture(t, 0)
	f.hub.cfg.Idle = 10 * time.Millisecond // ticks every second
	s, _ := f.join(t, 1)
	s.Close()
	f.now = f.now.Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.hub.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.hub.Loaded() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if f.hub.Loaded() != 0 {
		t.Error("Run did not reap")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// The main world stays loaded and does not count against the cap.
func TestHomePinned(t *testing.T) {
	f := newFixture(t, 1)
	f.hub.cfg.Home = 1
	a, _ := f.join(t, 1)
	b, msg := f.join(t, 2)
	if b == nil {
		t.Fatalf("one other world with the main one loaded: %q", msg)
	}
	a.Close()
	b.Close()
	f.now = f.now.Add(time.Hour)
	f.hub.Reap()
	if f.hub.Loaded() != 1 || f.loads.Load() != 2 {
		t.Fatalf("after the idle time: %d loaded", f.hub.Loaded())
	}
	c, _ := f.join(t, 3)
	loads := f.loads.Load()
	d, _ := f.join(t, 1)
	if f.loads.Load() != loads {
		t.Error("the main world was unloaded")
	}
	c.Close()
	d.Close()
}

// One address plays at most PerClient worlds besides the main one at
// once; closing a session gives its world back.
func TestPerClient(t *testing.T) {
	f := newFixture(t, 0)
	f.hub.cfg.Home = 1
	f.hub.cfg.PerClient = 1
	f.st.m[4] = store.World{ID: 4, PackID: "test-pack"}
	join := func(client string, world int64) (ws.Session, []ws.Message) {
		return f.hub.NewSession(client, ws.Who{World: world})
	}
	a, _ := join("192.0.2.1", 2)
	b, _ := join("192.0.2.1", 2) // the same world again is fine
	h, _ := join("192.0.2.1", 1) // and the main world
	if a == nil || b == nil || h == nil {
		t.Fatal("refused within the limit")
	}
	if s, ms := join("192.0.2.1", 3); s != nil || ms[0].Message != errBusy.Error() {
		t.Errorf("second world: %+v", ms)
	}
	if s, _ := join("2001:db8:1:2::/64", 3); s == nil {
		t.Error("another address was refused")
	} else if s2, ms := join("2001:db8:1:3::/64", 4); s2 != nil {
		t.Errorf("the same /48 played a second world: %+v", ms)
	} else {
		s.Close()
	}
	a.Close()
	if s, _ := join("192.0.2.1", 3); s != nil {
		t.Error("refused while the other world still had a session open")
	}
	b.Close()
	b.Close() // twice is harmless
	c, ms := join("192.0.2.1", 3)
	if c == nil {
		t.Fatalf("after leaving the other world: %+v", ms)
	}
	c.Close()
	h.Close()
	f.hub.mu.Lock()
	n := len(f.hub.playing)
	f.hub.mu.Unlock()
	if n != 0 {
		t.Errorf("%d clients still counted after every session closed", n)
	}
	// A refused join counts nothing.
	if s, _ := join("192.0.2.1", 404); s != nil {
		t.Fatal("joined a world that does not exist")
	}
	if s, _ := join("192.0.2.1", 4); s == nil {
		t.Error("a refused join still counted")
	} else {
		s.Close()
	}
}
