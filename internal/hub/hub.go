// Package hub runs the worlds of one server: it loads a world when its
// first player arrives, unloads it when nobody has played it for a while,
// and caps how many are loaded at once and how many one network plays. It
// is the ws.Game every connection plays: the connection's ws.Who names its
// world.
package hub

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Defaults without configuration.
const (
	// DefaultMaxLoaded is how many worlds may be loaded at once. Each one
	// keeps its packs' compiled scripts and its players' script state, up
	// to TALESPEAK_LUA_STATE_MEMORY (docs/admin-limits.md).
	DefaultMaxLoaded = 20
	// DefaultIdle is how long a world stays loaded after its last player
	// left.
	DefaultIdle = 5 * time.Minute
	// DefaultWorldsPerClient is how many worlds besides the main one the
	// players of one address (an IPv6 /48) may play at once. Each world
	// counts their Lua time and memory on its own, so this also bounds
	// what one network can use.
	DefaultWorldsPerClient = 4
)

// Loader makes the running game of a world from its pack.
type Loader func(w store.World, p *pack.Pack) (*game.World, error)

// Config sets up a Hub.
type Config struct {
	// Store finds the worlds players connect to.
	Store interface {
		World(ctx context.Context, world int64) (store.World, error)
		SetPackVersion(ctx context.Context, world int64, version string) error
	}
	// Packs are the installed packs. A world runs the newest version
	// compatible with the one it recorded (pack.Catalog.Pick).
	Packs *pack.Catalog
	Load  Loader
	// Home is the server's main world: it stays loaded once it is, and
	// counts against neither MaxLoaded nor PerClient.
	Home int64
	// MaxLoaded, Idle and PerClient: 0 is DefaultMaxLoaded, DefaultIdle
	// and DefaultWorldsPerClient.
	MaxLoaded int
	Idle      time.Duration
	PerClient int
	Log       *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Hub is the worlds of one server.
type Hub struct {
	cfg Config

	mu        sync.Mutex // guards loaded, finishing, playing and closed; never held while waiting for a world
	loaded    map[int64]*entry
	finishing map[int64]bool           // worlds that are ending: they do not load
	playing   map[string]map[int64]int // client key -> world -> its sessions there, and joins under way
	closed    bool
}

// entry is a loaded world, or one loading.
type entry struct {
	ready chan struct{} // closed once w or err is set
	w     *game.World
	err   error
}

// done reports whether e finished loading.
func (e *entry) done() bool {
	select {
	case <-e.ready:
		return true
	default:
		return false
	}
}

// New returns a Hub.
func New(cfg Config) *Hub {
	if cfg.MaxLoaded <= 0 {
		cfg.MaxLoaded = DefaultMaxLoaded
	}
	if cfg.Idle <= 0 {
		cfg.Idle = DefaultIdle
	}
	if cfg.PerClient <= 0 {
		cfg.PerClient = DefaultWorldsPerClient
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Hub{cfg: cfg, loaded: map[int64]*entry{}, finishing: map[int64]bool{}, playing: map[string]map[int64]int{}}
}

// What a player who cannot join is told.
var (
	errEnded  = errors.New("This world has ended. Go back to the lobby.")
	errNoPack = errors.New("This world's pack is not installed on this server.")
	errNoVer  = errors.New("This world needs another version of its pack than this server has.")
	errFull   = errors.New("The server has too many worlds open right now. Try again in a few minutes.")
	errLoad   = errors.New("This world could not be loaded. Try again later.")
	errClosed = errors.New("The server is shutting down.")
	errBusy   = errors.New("Your network plays too many worlds at once. Leave one of them first.")
)

// EndedMessage is what the players of a world that ends are told.
const EndedMessage = "This world has ended. Go back to the lobby to pick another one."

// NewSession implements ws.Game: it places the player in the world who
// names, loading it first when needed.
func (h *Hub) NewSession(client string, who ws.Who) (ws.Session, []ws.Message) {
	key := game.ClientKey(client)
	if err := h.reserve(key, who.World); err != nil {
		return nil, []ws.Message{{Type: "error", Message: err.Error()}}
	}
	release := func() { h.release(key, who.World) }
	for range 2 {
		w, err := h.get(who.World)
		if err != nil {
			release()
			return nil, []ws.Message{{Type: "error", Message: err.Error()}}
		}
		s, ms := w.NewSession(client, who)
		if !w.Closed() {
			if s == nil {
				release()
				return s, ms
			}
			gs, ok := s.(session)
			if !ok {
				release()
				return s, ms
			}
			return &tracked{session: gs, release: release}, ms
		}
		// Unloaded since get (a closed world refuses players, or hands out
		// sessions that do nothing): load it again.
		if s != nil {
			s.Close()
		}
		h.drop(who.World, w)
	}
	release()
	return nil, []ws.Message{{Type: "error", Message: errLoad.Error()}}
}

// session is what package game hands out.
type session interface {
	ws.Session
	ws.Pusher
	ws.Replacer
}

// tracked is a session the hub counts for its client until it closes.
type tracked struct {
	session
	once    sync.Once
	release func()
}

func (t *tracked) Close() {
	t.session.Close()
	t.once.Do(t.release)
}

// reserve counts a join of world by client key, or refuses it when the
// client plays as many other worlds as it may. The main world and players
// without an address ("", tests and local tools) are not limited.
func (h *Hub) reserve(key string, world int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	worlds := h.playing[key]
	if worlds[world] == 0 && key != "" && world != h.cfg.Home {
		n := len(worlds)
		if worlds[h.cfg.Home] > 0 {
			n--
		}
		if n >= h.cfg.PerClient {
			return errBusy
		}
	}
	if worlds == nil {
		worlds = map[int64]int{}
		h.playing[key] = worlds
	}
	worlds[world]++
	return nil
}

// release undoes one reserve.
func (h *Hub) release(key string, world int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	worlds := h.playing[key]
	if worlds[world]--; worlds[world] <= 0 {
		delete(worlds, world)
	}
	if len(worlds) == 0 {
		delete(h.playing, key)
	}
}

// get returns the running world id, loading it when it is not loaded.
func (h *Hub) get(id int64) (*game.World, error) {
	for try := 0; ; try++ {
		h.mu.Lock()
		switch {
		case h.closed:
			h.mu.Unlock()
			return nil, errClosed
		case h.finishing[id]:
			h.mu.Unlock()
			return nil, errEnded
		}
		e := h.loaded[id]
		if e != nil {
			h.mu.Unlock()
			<-e.ready
			return e.w, e.err
		}
		if id != h.cfg.Home && h.countLocked() >= h.cfg.MaxLoaded {
			idle := h.snapshotLocked()
			h.mu.Unlock()
			if try >= 2 || !h.evict(idle) {
				return nil, errFull
			}
			continue
		}
		e = &entry{ready: make(chan struct{})}
		h.loaded[id] = e
		h.mu.Unlock()
		e.w, e.err = h.load(id)
		close(e.ready)
		if e.err != nil {
			h.mu.Lock()
			if h.loaded[id] == e {
				delete(h.loaded, id)
			}
			h.mu.Unlock()
		}
		return e.w, e.err
	}
}

// countLocked is how many worlds count against MaxLoaded. The caller
// holds h.mu.
func (h *Hub) countLocked() int {
	n := len(h.loaded)
	if h.loaded[h.cfg.Home] != nil {
		n--
	}
	return n
}

// loadedWorld is a loaded world with its id.
type loadedWorld struct {
	id int64
	w  *game.World
}

// snapshotLocked lists the loaded worlds that may unload: all but the
// main world, the ones still loading and the ones a player is joining (a
// world just loaded for its first player is empty until they enter). The
// caller holds h.mu.
func (h *Hub) snapshotLocked() []loadedWorld {
	joining := map[int64]bool{}
	for _, worlds := range h.playing {
		for id := range worlds {
			joining[id] = true
		}
	}
	var list []loadedWorld
	for id, e := range h.loaded {
		if id != h.cfg.Home && e.done() && e.w != nil && !joining[id] {
			list = append(list, loadedWorld{id, e.w})
		}
	}
	return list
}

// load makes the running game of world id.
func (h *Hub) load(id int64) (*game.World, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wd, err := h.cfg.Store.World(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && wd.Finished) {
		return nil, errEnded
	}
	if err != nil {
		h.cfg.Log.Error("world lookup failed", "world", id, "err", err)
		return nil, errLoad
	}
	p, err := h.Pack(wd)
	if err != nil {
		h.cfg.Log.Warn("world has no pack to run", "world", id, "pack", wd.PackID, "version", wd.PackVersion, "err", err)
		return nil, err
	}
	if p.Version != wd.PackVersion {
		// Recorded before the world runs it, so a later downgrade of the
		// pack does not run state a newer version wrote.
		if err := h.cfg.Store.SetPackVersion(ctx, id, p.Version); err != nil {
			h.cfg.Log.Error("pack version not recorded", "world", id, "err", err)
			return nil, errLoad
		}
		h.cfg.Log.Info("world moves to a new pack version", "world", id, "pack", p.ID, "from", wd.PackVersion, "to", p.Version)
		wd.PackVersion = p.Version
	}
	w, err := h.cfg.Load(wd, p)
	if err != nil {
		h.cfg.Log.Error("world could not load", "world", id, "pack", p.Name(), "err", err)
		return nil, errLoad
	}
	h.cfg.Log.Info("world loaded", "world", id, "pack", p.ID, "version", p.Version)
	return w, nil
}

// Pack returns the pack version world w runs when it loads next, or the
// error its players are told when no installed version can run it.
func (h *Hub) Pack(w store.World) (*pack.Pack, error) {
	p, err := h.cfg.Packs.Pick(w.PackID, w.PackVersion)
	var nv *pack.NoVersionError
	switch {
	case errors.As(err, &nv):
		return nil, errNoVer
	case err != nil:
		return nil, errNoPack
	}
	return p, nil
}

// drop forgets world id when it is still w.
func (h *Hub) drop(id int64, w *game.World) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e := h.loaded[id]; e != nil && e.done() && e.w == w {
		delete(h.loaded, id)
	}
}

// evict unloads the world of list that has been empty longest, and
// reports whether there was one.
func (h *Hub) evict(list []loadedWorld) bool {
	type idle struct {
		loadedWorld
		since time.Time
	}
	var empty []idle
	for _, lw := range list {
		if t, ok := lw.w.IdleSince(); ok {
			empty = append(empty, idle{lw, t})
		}
	}
	slices.SortFunc(empty, func(a, b idle) int { return a.since.Compare(b.since) })
	for _, e := range empty {
		if e.w.CloseIfEmpty() { // a player may have come meanwhile
			h.drop(e.id, e.w)
			h.cfg.Log.Info("world unloaded for another", "world", e.id)
			return true
		}
	}
	return false
}

// Reap unloads the worlds nobody has played for the idle time. The main
// world stays.
func (h *Hub) Reap() {
	h.mu.Lock()
	list := h.snapshotLocked()
	h.mu.Unlock()
	cutoff := h.cfg.Now().Add(-h.cfg.Idle)
	for _, lw := range list {
		if lw.w.CloseIfIdle(cutoff) {
			h.drop(lw.id, lw.w)
			h.cfg.Log.Info("world unloaded", "world", lw.id)
		}
	}
}

// Run reaps idle worlds until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(max(h.cfg.Idle/4, time.Second))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Reap()
		}
	}
}

// Online is how many players play world id now.
func (h *Hub) Online(id int64) int {
	h.mu.Lock()
	var w *game.World
	if e := h.loaded[id]; e != nil && e.done() {
		w = e.w
	}
	h.mu.Unlock()
	if w == nil {
		return 0
	}
	return w.Online() // outside h.mu: it may wait for the world's running hook
}

// RoleChanged tells world id, when it is loaded, that the account's role
// in it is role now ("" for no member): game.World.RoleChanged.
func (h *Hub) RoleChanged(id, account int64, role string) {
	h.mu.Lock()
	var w *game.World
	if e := h.loaded[id]; e != nil && e.done() {
		w = e.w
	}
	h.mu.Unlock()
	if w != nil {
		w.RoleChanged(account, role) // outside h.mu, as Online
	}
}

// Loaded is how many worlds are loaded or loading.
func (h *Hub) Loaded() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.loaded)
}

// Finish ends world id: its players are sent away and it unloads, then
// finish runs (it marks the world finished in the store). The world
// cannot load meanwhile.
func (h *Hub) Finish(id int64, finish func() error) error {
	h.mu.Lock()
	if h.finishing[id] {
		h.mu.Unlock()
		return store.ErrNotFound // someone else is ending it
	}
	h.finishing[id] = true
	e := h.loaded[id]
	delete(h.loaded, id)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.finishing, id)
		h.mu.Unlock()
	}()
	if e != nil {
		<-e.ready
		if e.w != nil {
			e.w.End(EndedMessage) // its players save what they changed last, before the state goes
		}
	}
	return finish()
}

// Close closes every loaded world; a world asked for after it does not
// load.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	loaded := h.loaded
	h.loaded = map[int64]*entry{}
	h.mu.Unlock()
	for _, e := range loaded {
		<-e.ready
		if e.w != nil {
			e.w.Close()
		}
	}
}
