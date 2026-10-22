// Package hub runs the worlds of one server: it loads a world when its
// first player arrives, unloads it when nobody has played it for a while,
// and caps how many are loaded at once. It is the ws.Game every connection
// plays: the connection's ws.Who names its world.
package hub

import (
	"context"
	"errors"
	"log/slog"
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
)

// Loader makes the running game of a world from its pack.
type Loader func(w store.World, p *pack.Pack) (*game.World, error)

// Config sets up a Hub.
type Config struct {
	// Store finds the worlds players connect to.
	Store interface {
		World(ctx context.Context, world int64) (store.World, error)
	}
	// Packs are the installed packs by id.
	Packs map[string]*pack.Pack
	Load  Loader
	// MaxLoaded and Idle: 0 is DefaultMaxLoaded and DefaultIdle.
	MaxLoaded int
	Idle      time.Duration
	Log       *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Hub is the worlds of one server.
type Hub struct {
	cfg Config

	mu        sync.Mutex // guards loaded and finishing; held before a world's own lock, never after
	loaded    map[int64]*entry
	finishing map[int64]bool // worlds that are ending: they do not load
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
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Hub{cfg: cfg, loaded: map[int64]*entry{}, finishing: map[int64]bool{}}
}

// What a player who cannot join is told.
var (
	errEnded  = errors.New("This world has ended. Go back to the lobby.")
	errNoPack = errors.New("This world's pack is not installed on this server.")
	errFull   = errors.New("The server has too many worlds open right now. Try again in a few minutes.")
	errLoad   = errors.New("This world could not be loaded. Try again later.")
	errClosed = errors.New("The server is shutting down.")
)

// EndedMessage is what the players of a world that ends are told.
const EndedMessage = "This world has ended. Go back to the lobby to pick another one."

// NewSession implements ws.Game: it places the player in the world who
// names, loading it first when needed.
func (h *Hub) NewSession(client string, who ws.Who) (ws.Session, []ws.Message) {
	for range 2 {
		w, err := h.get(who.World)
		if err != nil {
			return nil, []ws.Message{{Type: "error", Message: err.Error()}}
		}
		s, ms := w.NewSession(client, who)
		if !w.Closed() {
			return s, ms
		}
		// Unloaded since get (a closed world hands out sessions that do
		// nothing): load it again.
		if s != nil {
			s.Close()
		}
		h.drop(who.World, w)
	}
	return nil, []ws.Message{{Type: "error", Message: errLoad.Error()}}
}

// get returns the running world id, loading it when it is not loaded.
func (h *Hub) get(id int64) (*game.World, error) {
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
	if e == nil {
		if len(h.loaded) >= h.cfg.MaxLoaded && !h.evictLocked() {
			h.mu.Unlock()
			return nil, errFull
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
	} else {
		h.mu.Unlock()
	}
	<-e.ready
	return e.w, e.err
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
	p := h.cfg.Packs[wd.PackID]
	if p == nil {
		return nil, errNoPack
	}
	w, err := h.cfg.Load(wd, p)
	if err != nil {
		h.cfg.Log.Error("world could not load", "world", id, "pack", p.ID, "err", err)
		return nil, errLoad
	}
	h.cfg.Log.Info("world loaded", "world", id, "pack", p.ID)
	return w, nil
}

// drop forgets world id when it is still w.
func (h *Hub) drop(id int64, w *game.World) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e := h.loaded[id]; e != nil && e.done() && e.w == w {
		delete(h.loaded, id)
	}
}

// evictLocked unloads the world that has been empty longest, and reports
// whether there was one. The caller holds h.mu.
func (h *Hub) evictLocked() bool {
	var oldest int64
	var since time.Time
	for id, e := range h.loaded {
		if !e.done() || e.w == nil {
			continue
		}
		if t, ok := e.w.IdleSince(); ok && (oldest == 0 || t.Before(since)) {
			oldest, since = id, t
		}
	}
	if oldest == 0 || !h.loaded[oldest].w.CloseIfEmpty() {
		return false
	}
	delete(h.loaded, oldest)
	h.cfg.Log.Info("world unloaded for another", "world", oldest)
	return true
}

// Reap unloads the worlds nobody has played for the idle time.
func (h *Hub) Reap() {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := h.cfg.Now().Add(-h.cfg.Idle)
	for id, e := range h.loaded {
		if e.done() && e.w != nil && e.w.CloseIfIdle(cutoff) {
			delete(h.loaded, id)
			h.cfg.Log.Info("world unloaded", "world", id)
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
