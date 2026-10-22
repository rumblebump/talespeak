package hub

import (
	"context"
	"errors"
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

// testPack loads content/test-pack.
func testPack(t *testing.T) *pack.Pack {
	t.Helper()
	p, err := pack.Load(os.DirFS("../../content/test-pack"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// gatedLoader loads worlds of the test pack, each load waiting until the
// test opens the gate; it records the worlds it made.
type gatedLoader struct {
	gate    chan struct{}
	started chan int64 // one value per load that began
	mu      sync.Mutex
	made    []*game.World
	fail    bool
}

func newGatedLoader() *gatedLoader {
	return &gatedLoader{gate: make(chan struct{}), started: make(chan int64, 16)}
}

func (g *gatedLoader) load(w store.World, p *pack.Pack) (*game.World, error) {
	g.started <- w.ID
	<-g.gate
	if g.fail {
		return nil, errors.New("broken script")
	}
	gw, err := game.NewWorld(p, script.DefaultLimits, quiet)
	if err == nil {
		g.mu.Lock()
		g.made = append(g.made, gw)
		g.mu.Unlock()
	}
	return gw, err
}

func (g *gatedLoader) worlds() []*game.World {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*game.World(nil), g.made...)
}

// waitStarted waits until a load of world id began.
func (g *gatedLoader) waitStarted(t *testing.T, id int64) {
	t.Helper()
	select {
	case got := <-g.started:
		if got != id {
			t.Fatalf("load of world %d began, want %d", got, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no load of world %d began", id)
	}
}

func newStore() *worlds {
	return &worlds{m: map[int64]store.World{
		1: {ID: 1, PackID: "test-pack"},
		2: {ID: 2, PackID: "test-pack"},
	}}
}

// joinResult is what a NewSession in another goroutine returned.
type joinResult struct {
	s   ws.Session
	msg string
}

func joinAsync(h *Hub, world int64) chan joinResult {
	c := make(chan joinResult, 1)
	go func() {
		s, ms := h.NewSession("", ws.Who{World: world})
		r := joinResult{s: s}
		if s == nil && len(ms) == 1 {
			r.msg = ms[0].Message
		}
		c <- r
	}()
	return c
}

func recv(t *testing.T, c chan joinResult) joinResult {
	t.Helper()
	select {
	case r := <-c:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("NewSession did not return")
		return joinResult{}
	}
}

// works reports whether the session answers "look" like a live one.
func works(s ws.Session) bool {
	r := s.Handle(ws.Message{Type: "input", Text: "look"})
	if len(r) == 0 {
		return false
	}
	for _, m := range r {
		if m.Type == "error" {
			return false
		}
	}
	return true
}

// New fills in the defaults for a zero Config.
func TestNewDefaults(t *testing.T) {
	h := New(Config{})
	if h.cfg.MaxLoaded != DefaultMaxLoaded || h.cfg.Idle != DefaultIdle || h.cfg.Log == nil || h.cfg.Now == nil {
		t.Errorf("defaults = %+v", h.cfg)
	}
	h = New(Config{MaxLoaded: -1, Idle: -time.Second})
	if h.cfg.MaxLoaded != DefaultMaxLoaded || h.cfg.Idle != DefaultIdle {
		t.Errorf("negative settings kept: %d, %v", h.cfg.MaxLoaded, h.cfg.Idle)
	}
	if h.Loaded() != 0 || h.Online(1) != 0 {
		t.Error("a new hub has worlds")
	}
	h.Reap() // nothing loaded: harmless
	h.Close()
	h.Close() // twice is harmless
}

// A world that closed under the hub (as when Reap unloads it between
// get and NewSession) is loaded again for the player, who never gets a
// dead session.
func TestNewSessionReloadsClosedWorld(t *testing.T) {
	f := newFixture(t, 0)
	a, _ := f.join(t, 1)
	a.Close()
	f.hub.mu.Lock()
	stale := f.hub.loaded[1].w
	f.hub.mu.Unlock()
	stale.Close() // still in the hub's map, but closed
	s, msg := f.join(t, 1)
	if s == nil {
		t.Fatalf("join after the world closed under the hub: %q", msg)
	}
	defer s.Close()
	if !works(s) {
		t.Error("the player got a session of the closed world")
	}
	if f.loads.Load() != 2 || f.hub.Loaded() != 1 || f.hub.Online(1) != 1 {
		t.Errorf("loads %d, loaded %d, online %d; want 2, 1, 1", f.loads.Load(), f.hub.Loaded(), f.hub.Online(1))
	}
	// drop leaves a world that is not the stale one alone.
	f.hub.drop(1, stale)
	if f.hub.Loaded() != 1 || f.hub.Online(1) != 1 {
		t.Error("drop of a stale world unloaded its replacement")
	}
	f.hub.drop(404, stale) // unknown world: harmless
}

// A loader that keeps handing out closed worlds is given up on after a
// retry, with errLoad.
func TestNewSessionGivesUp(t *testing.T) {
	p := testPack(t)
	var loads atomic.Int32
	h := New(Config{Store: newStore(), Packs: catalog(t, p), Log: quiet,
		Load: func(store.World, *pack.Pack) (*game.World, error) {
			loads.Add(1)
			w, err := game.NewWorld(p, script.DefaultLimits, quiet)
			if err == nil {
				w.Close()
			}
			return w, err
		}})
	defer h.Close()
	s, ms := h.NewSession("", ws.Who{World: 1})
	if s != nil || len(ms) != 1 || ms[0].Message != errLoad.Error() {
		t.Errorf("NewSession = %v, %+v; want errLoad", s, ms)
	}
	if loads.Load() != 2 || h.Loaded() != 0 {
		t.Errorf("loads %d, loaded %d; want 2, 0", loads.Load(), h.Loaded())
	}
}

// failingWorlds fails every lookup, like a database that is gone.
type failingWorlds struct{}

func (failingWorlds) World(context.Context, int64) (store.World, error) {
	return store.World{}, errors.New("disk on fire")
}

func (failingWorlds) SetPackVersion(context.Context, int64, string) error {
	return errors.New("disk on fire")
}

// A store that fails is errLoad, not errEnded, and nothing stays loaded.
func TestLoadStoreFailure(t *testing.T) {
	h := New(Config{Store: failingWorlds{}, Packs: catalog(t, testPack(t)), Log: quiet,
		Load: func(store.World, *pack.Pack) (*game.World, error) {
			t.Error("loaded without a world")
			return nil, errors.New("no")
		}})
	defer h.Close()
	if s, ms := h.NewSession("", ws.Who{World: 1}); s != nil || len(ms) != 1 || ms[0].Message != errLoad.Error() {
		t.Errorf("NewSession = %v, %+v; want errLoad", s, ms)
	}
	if h.Loaded() != 0 {
		t.Errorf("%d loaded", h.Loaded())
	}
}

// A world that is loading counts against the cap, cannot be evicted, and
// reports nobody online without waiting for the load.
func TestLoadingWorld(t *testing.T) {
	g := newGatedLoader()
	h := New(Config{Store: newStore(), Packs: catalog(t, testPack(t)), MaxLoaded: 1, Log: quiet, Load: g.load})
	defer h.Close()
	first := joinAsync(h, 1)
	g.waitStarted(t, 1)
	if h.Loaded() != 1 || h.Online(1) != 0 {
		t.Errorf("while loading: loaded %d, online %d", h.Loaded(), h.Online(1))
	}
	h.Reap() // a loading world is not reaped
	if r := recv(t, joinAsync(h, 2)); r.s != nil || r.msg != errFull.Error() {
		t.Errorf("second world while the first loads: %v, %q; want errFull", r.s, r.msg)
	}
	// A second player of the loading world waits for the same load.
	second := joinAsync(h, 1)
	close(g.gate)
	for _, c := range []chan joinResult{first, second} {
		r := recv(t, c)
		if r.s == nil {
			t.Fatalf("refused: %q", r.msg)
		}
		defer r.s.Close()
	}
	if n := len(g.worlds()); n != 1 || h.Online(1) != 2 {
		t.Errorf("%d loads, %d online; want 1, 2", n, h.Online(1))
	}
}

// A load that fails while others wait fails for all of them, and the
// next player tries again.
func TestLoadFailsForWaiters(t *testing.T) {
	g := newGatedLoader()
	g.fail = true
	h := New(Config{Store: newStore(), Packs: catalog(t, testPack(t)), Log: quiet, Load: g.load})
	defer h.Close()
	a := joinAsync(h, 1)
	g.waitStarted(t, 1)
	b := joinAsync(h, 1)
	// b may join the load in progress or, if it is slow, start its own
	// after the first failed; either way it fails.
	close(g.gate)
	for _, c := range []chan joinResult{a, b} {
		if r := recv(t, c); r.s != nil || r.msg != errLoad.Error() {
			t.Errorf("join of a broken world: %v, %q", r.s, r.msg)
		}
	}
	if h.Loaded() != 0 {
		t.Errorf("%d loaded after failed loads", h.Loaded())
	}
}

// Finish while the world loads waits for the load, ends the world it
// made, and the player who triggered the load does not keep playing it.
func TestFinishWhileLoading(t *testing.T) {
	g := newGatedLoader()
	st := newStore()
	h := New(Config{Store: st, Packs: catalog(t, testPack(t)), Log: quiet, Load: g.load})
	defer h.Close()
	joined := joinAsync(h, 1)
	g.waitStarted(t, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- h.Finish(1, func() error { st.finish(1); return nil })
	}()
	// Finish has taken the world out of the map before the load ends.
	deadline := time.Now().Add(5 * time.Second)
	for h.Loaded() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(g.gate)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Finish = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Finish did not return")
	}
	r := recv(t, joined)
	if r.s != nil {
		// It got in before the end: it must have been sent away.
		if works(r.s) {
			t.Error("a player plays a world that ended while it loaded")
		}
		r.s.Close()
	} else if r.msg != errEnded.Error() {
		t.Errorf("joining a world that ended while it loaded: %q", r.msg)
	}
	for _, w := range g.worlds() {
		if !w.Closed() {
			t.Error("the world loaded for an ended world is still open")
		}
	}
	if h.Loaded() != 0 {
		t.Errorf("%d loaded after Finish", h.Loaded())
	}
}

// Finish of a world whose load failed just runs finish.
func TestFinishFailedLoad(t *testing.T) {
	g := newGatedLoader()
	g.fail = true
	h := New(Config{Store: newStore(), Packs: catalog(t, testPack(t)), Log: quiet, Load: g.load})
	defer h.Close()
	joined := joinAsync(h, 1)
	g.waitStarted(t, 1)
	ran := make(chan error, 1)
	go func() { ran <- h.Finish(1, func() error { return nil }) }()
	close(g.gate)
	if err := <-ran; err != nil {
		t.Errorf("Finish = %v", err)
	}
	if r := recv(t, joined); r.s != nil {
		t.Error("joined a world that failed to load")
		r.s.Close()
	}
}

// Close while a world loads waits for it and closes it; the player who
// triggered the load is told the server is shutting down.
func TestCloseWhileLoading(t *testing.T) {
	g := newGatedLoader()
	h := New(Config{Store: newStore(), Packs: catalog(t, testPack(t)), Log: quiet, Load: g.load})
	joined := joinAsync(h, 1)
	g.waitStarted(t, 1)
	closed := make(chan struct{})
	go func() { h.Close(); close(closed) }()
	deadline := time.Now().Add(5 * time.Second)
	for h.Loaded() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(g.gate)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	r := recv(t, joined)
	if r.s != nil {
		if works(r.s) {
			t.Error("a player plays after Close")
		}
		r.s.Close()
	} else if r.msg != errClosed.Error() {
		t.Errorf("join during Close: %q", r.msg)
	}
	for _, w := range g.worlds() {
		if !w.Closed() {
			t.Error("a world loaded during Close is still open")
		}
	}
}

// Players joining and leaving while Reap and eviction run never get a
// dead session: a session NewSession hands out works until it closes.
// The reaper here closes every empty world at once (the clock is an hour
// ahead), so a world can close between loading and its player entering
// it twice in a row: such a player is refused with errLoad, never left in
// a dead world.
func TestReapRacesJoins(t *testing.T) {
	f := newFixture(t, 2)
	f.hub.cfg.Now = func() time.Time { return time.Now().Add(time.Hour) } // every empty world is past the idle time
	stop := make(chan struct{})
	var reaper sync.WaitGroup
	reaper.Add(1)
	go func() {
		defer reaper.Done()
		for {
			select {
			case <-stop:
				return
			default:
				f.hub.Reap()
			}
		}
	}()
	var wg sync.WaitGroup
	var dead, joined, refused atomic.Int32
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 30 {
				world := int64(1 + (i+j)%3) // three worlds, two may be loaded
				s, ms := f.hub.NewSession("", ws.Who{World: world})
				if s == nil {
					if len(ms) != 1 || (ms[0].Message != errFull.Error() && ms[0].Message != errLoad.Error()) {
						t.Errorf("refused: %+v", ms)
					}
					refused.Add(1)
					continue
				}
				joined.Add(1)
				if !works(s) {
					dead.Add(1)
				}
				s.Close()
			}
		}()
	}
	wg.Wait()
	close(stop)
	reaper.Wait()
	if dead.Load() != 0 {
		t.Errorf("%d of %d sessions were dead", dead.Load(), joined.Load())
	}
	if joined.Load() == 0 {
		t.Errorf("nobody joined, %d refused", refused.Load())
	}
	t.Logf("%d joined, %d refused", joined.Load(), refused.Load())
	if n := f.hub.Loaded(); n > 2 {
		t.Errorf("%d loaded, cap 2", n)
	}
}

// Finish and joins at once: after Finish returns the world is gone, and
// every player is either refused or sent away.
func TestFinishRacesJoins(t *testing.T) {
	f := newFixture(t, 0)
	var wg sync.WaitGroup
	sessions := make(chan ws.Session, 64)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, ms := f.hub.NewSession("", ws.Who{World: 1})
			if s == nil {
				if len(ms) != 1 || ms[0].Message != errEnded.Error() {
					t.Errorf("refused with %+v", ms)
				}
				return
			}
			sessions <- s
		}()
	}
	if err := f.hub.Finish(1, func() error { f.st.finish(1); return nil }); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(sessions)
	for s := range sessions {
		if works(s) {
			t.Error("a player still plays the ended world")
		}
		s.Close()
	}
	if f.hub.Loaded() != 0 || f.hub.Online(1) != 0 {
		t.Errorf("after Finish: loaded %d, online %d", f.hub.Loaded(), f.hub.Online(1))
	}
}

// With every loaded world played, eviction finds nothing, and Reap keeps
// played worlds however old the clock says they are.
func TestEvictNothingIdle(t *testing.T) {
	f := newFixture(t, 1)
	a, _ := f.join(t, 1)
	defer a.Close()
	f.now = f.now.Add(24 * time.Hour)
	f.hub.Reap()
	if f.hub.Loaded() != 1 || f.hub.Online(1) != 1 {
		t.Fatalf("a played world was reaped: loaded %d", f.hub.Loaded())
	}
	f.hub.mu.Lock()
	idle := f.hub.snapshotLocked()
	f.hub.mu.Unlock()
	if f.hub.evict(idle) {
		t.Error("evicted a played world")
	}
	if !works(a) {
		t.Error("the player was sent away")
	}
}

// homeFixture is a hub of ten test-pack worlds whose main world is 1.
func homeFixture(t *testing.T, cfg Config) (*Hub, *atomic.Int32, *time.Time) {
	t.Helper()
	p := testPack(t)
	st := &worlds{m: map[int64]store.World{}}
	for id := int64(1); id <= 10; id++ {
		st.m[id] = store.World{ID: id, PackID: "test-pack"}
	}
	loads := new(atomic.Int32)
	now := time.Now()
	cfg.Store, cfg.Packs, cfg.Log, cfg.Home = st, catalog(t, p), quiet, 1
	cfg.Now = func() time.Time { return now }
	cfg.Load = func(store.World, *pack.Pack) (*game.World, error) {
		loads.Add(1)
		return game.NewWorld(p, script.DefaultLimits, quiet)
	}
	h := New(cfg)
	t.Cleanup(h.Close)
	return h, loads, &now
}

// joinFrom starts a session of client in world, or returns the refusal.
func joinFrom(h *Hub, client string, world int64) (ws.Session, string) {
	s, ms := h.NewSession(client, ws.Who{World: world})
	if s == nil {
		if len(ms) == 1 {
			return nil, ms[0].Message
		}
		return nil, "?"
	}
	return s, ""
}

// The main world is never the one evicted for another, and is not
// reaped however long it was empty.
func TestHomeNotEvicted(t *testing.T) {
	h, loads, now := homeFixture(t, Config{MaxLoaded: 1})
	home, _ := joinFrom(h, "", 1)
	home.Close()
	a, msg := joinFrom(h, "", 2)
	if a == nil {
		t.Fatalf("a world besides the main one with a cap of 1: %q", msg)
	}
	a.Close()
	b, msg := joinFrom(h, "", 3) // evicts 2, not the main world
	if b == nil {
		t.Fatalf("world 3: %q", msg)
	}
	b.Close()
	if h.Loaded() != 2 {
		t.Errorf("%d loaded, want the main world and world 3", h.Loaded())
	}
	*now = now.Add(24 * time.Hour)
	h.Reap()
	if h.Loaded() != 1 {
		t.Errorf("after a day: %d loaded, want the main world", h.Loaded())
	}
	before := loads.Load()
	home, _ = joinFrom(h, "", 1)
	home.Close()
	if loads.Load() != before {
		t.Error("the main world was unloaded")
	}
}

// Many joins at once from one network never play more than PerClient
// worlds at once.
func TestPerClientConcurrent(t *testing.T) {
	h, _, _ := homeFixture(t, Config{PerClient: 2})
	var wg sync.WaitGroup
	got := make(chan ws.Session, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, _ := joinFrom(h, "192.0.2.1", int64(2+i%8)); s != nil {
				got <- s
			}
		}()
	}
	wg.Wait()
	close(got)
	var all []ws.Session
	for s := range got {
		all = append(all, s)
	}
	h.mu.Lock()
	n := len(h.playing["192.0.2.1"])
	h.mu.Unlock()
	if n > 2 || n == 0 {
		t.Errorf("one network plays %d worlds at once, limit 2", n)
	}
	for _, s := range all {
		s.Close()
	}
	h.mu.Lock()
	left := len(h.playing)
	h.mu.Unlock()
	if left != 0 {
		t.Errorf("%d clients counted after all left", left)
	}
}

// A session closed twice frees its world once: while another session of
// the same network is still in that world, it still counts.
func TestPerClientCloseTwice(t *testing.T) {
	h, _, _ := homeFixture(t, Config{PerClient: 1})
	a1, _ := joinFrom(h, "192.0.2.1", 2)
	a2, _ := joinFrom(h, "192.0.2.1", 2)
	if a1 == nil || a2 == nil {
		t.Fatal("refused within the limit")
	}
	a1.Close()
	a1.Close()
	if s, msg := joinFrom(h, "192.0.2.1", 3); s != nil || msg != errBusy.Error() {
		t.Errorf("a session closed twice freed its world: %v, %q", s, msg)
		if s != nil {
			s.Close()
		}
	}
	a2.Close()
	if s, msg := joinFrom(h, "192.0.2.1", 3); s == nil {
		t.Errorf("after both left: %q", msg)
	} else {
		s.Close()
	}
}
