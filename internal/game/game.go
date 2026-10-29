// Package game runs a world: one story pack, its Lua runtime and the players
// connected to it. It turns WebSocket messages into Lua hook calls and the
// hooks' output back into messages.
package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// DefaultPlayerName is the name of players without an account, in a world
// without a store (tests and local tools).
const DefaultPlayerName = "traveler"

// MaxInputBytes bounds one line the player types.
const MaxInputBytes = 500

// Option configures a World.
type Option func(*World)

// WithClientBudget sets how much Lua time one client address may use: burst
// at once, then rate per second.
func WithClientBudget(rate, burst time.Duration) Option {
	return func(w *World) { w.rate, w.burst = rate.Seconds(), burst.Seconds() }
}

// WithClientMemory sets how much script state the players of one client
// address may keep together.
func WithClientMemory(n int64) Option {
	return func(w *World) { w.clientMemory = n }
}

// WithClock sets the world's clock: now tells the time, and after calls f
// once d has passed and returns a function that stops it, like
// time.AfterFunc. Tests use it to run timers without waiting.
func WithClock(now func() time.Time, after func(d time.Duration, f func()) (stop func() bool)) Option {
	return func(w *World) { w.now, w.after = now, after }
}

// WithStore makes the world last: it is world id in st, its players sign
// in, and what scripts keep in state.player and state.world, and where
// each player is, is read from st at start and written after every hook.
func WithStore(st store.Worlds, world int64) Option {
	return func(w *World) { w.store, w.worldID = st, world }
}

// WithAudit writes what game masters do (T-0011) to a, for /audit and
// the admins. Without it, the world keeps the latest entries in memory.
func WithAudit(a store.Audit) Option {
	return func(w *World) { w.auditStore = a }
}

// saveTimeout bounds one write to the store, and loading a player.
const saveTimeout = 5 * time.Second

func afterFunc(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }

// retryBusy is how long a timer or a question's timeout waits to run its
// hook again when the player's address has used up its Lua time.
const retryBusy = 500 * time.Millisecond

// MaxQueued and MaxQueuedBytes bound the pushed messages (a timer's
// output, other players' changes) waiting for one connection. A client
// that falls further behind is disconnected.
const (
	MaxQueued      = 500
	MaxQueuedBytes = 256 << 10
)

// World is one running game. Its Lua state is used by one session at a time.
//
// Every player runs their own instance of each room script, with their own
// globals: nothing a player types or does reaches another player through
// script variables. State that lasts or is shared goes to the database
// (T-0018).
type World struct {
	log  *slog.Logger
	pack *pack.Pack

	ruleset  *pack.Pack     // the ruleset the story runs on (profile.go); nil without one
	profiles store.Profiles // where the players' profiles are, with a ruleset

	mu       sync.Mutex // guards rt, budgets, sessions, every session's fields and closed, and every Lua call: gopher-lua states are not goroutine safe
	rt       *script.Runtime
	scripts  map[string]*script.Script // room id -> its compiled script
	sessions map[*session]struct{}
	closed   bool
	lastLeft time.Time // when the world was last empty: started, or the last player left

	rate, burst float64            // Lua seconds per second and at once, per client
	budgets     map[string]*budget // client -> Lua time left
	now         func() time.Time
	after       func(time.Duration, func()) func() bool // see WithClock

	clientMemory int64                               // script state one client may keep
	clientEnvs   map[string]map[*script.Env]struct{} // client -> its players' script instances

	stateTotal  int64            // what all players keep in state.player, reserved in rt too
	clientState map[string]int64 // client -> what its players keep in state.player

	store     store.Worlds         // nil: nothing lasts, and players need no account
	worldID   int64                // the world in store
	chars     map[int64]*session   // character id -> its session, with a store
	unsaved   []string             // world keys a failed save did not write
	lost      map[int64]store.Save // character -> what a failed save of a player who left did not write
	lostOrder []int64              // the characters of lost, the oldest first

	timerSaves map[timerKey]timerSave // timer changes the next save writes (lasting.go)
	timerSeq   int64                  // changes noted, for the order of timerSaves

	world       script.State // state.world, shared by every player
	changed     []string     // world keys the running hook changed
	delivering  bool         // a delivery of on_change is due
	lastDeliver time.Time    // when the last delivery started

	players    int                              // players who joined, for player ids
	arrivals   int64                            // rooms entered and joins, for the order of room.players and world.players
	roomTimers map[string]map[string]*roomTimer // room id -> name -> its timer
	timerNews  map[roomTimerKey]struct{}        // room timers the running hook changed
	timerDue   map[roomTimerKey]*session        // room timers changed since the last delivery -> who changed them last

	timersDelivering bool      // a delivery of room timers is due
	lastTimers       time.Time // when the last delivery of room timers started

	auditStore store.Audit        // where game masters' actions go; nil keeps them in auditLog
	auditLog   []store.AuditEntry // the latest actions without an audit store, the oldest first

	broadcasts map[string]*budget // game master -> commands that reach other players they may send now

	// The world's time (clock.go, T-0012).
	pausedAt    time.Time          // when a game master paused the world; zero while it runs
	pausedFor   time.Duration      // how long it was paused before: the world clock is this far behind the wall clock
	gameTime    time.Duration      // the game time counted until gameSince
	gameSince   time.Time          // when the game clock last started
	gameRuns    bool               // the game clock runs: the world is not paused and a player is online
	minTime     time.Duration      // the shortest question timeout or shown timer (/mintime); 0 is off
	clockDirty  bool               // the world's time changed since the last save
	stateLimits script.StateLimits // of state.player, state.world and state.game
	game        script.State       // state.game, with the world's settings (gamestate.go)
	gameChanged []string           // state.game keys changed since the last save
	gameUnsaved []string           // state.game keys a failed save did not write
	finish      func(text string)  // ends the world for world.finish; nil: it cannot end from a script
	finishText  string             // what world.finish said, shown when the world ends
	finishing   bool               // world.finish was called: the world ends soon

	// What happened in the rooms (events.go) and what players typed
	// (typed.go).
	runID      string                 // this run's start time in base 36, before every event id
	eventSeq   int64                  // events told, for their ids
	events     map[string][]roomEvent // room id -> its last events, the oldest first
	vocab      map[string]struct{}    // the pack's own words
	typedBy    map[string]*typedWords // player id -> the words they typed lately
	typedOrder []string               // the players of typedBy, the oldest first
	typedCount map[string]int         // word -> how many players of typedBy typed it
	nameWords  map[string]struct{}    // the words of the names of players who joined
}

// NewWorld compiles every room script of p and runs each top level once, so
// a script that fails fails the world at start, with the room's file in the
// error.
func NewWorld(p *pack.Pack, limits script.Limits, log *slog.Logger, opts ...Option) (*World, error) {
	w := &World{log: log, pack: p, rt: script.New(limits), scripts: map[string]*script.Script{}, sessions: map[*session]struct{}{},
		budgets: map[string]*budget{}, now: time.Now, after: afterFunc, clientMemory: DefaultClientMemory, clientEnvs: map[string]map[*script.Env]struct{}{},
		clientState: map[string]int64{}, roomTimers: map[string]map[string]*roomTimer{}, timerNews: map[roomTimerKey]struct{}{},
		timerDue: map[roomTimerKey]*session{}, chars: map[int64]*session{}, lost: map[int64]store.Save{}, timerSaves: map[timerKey]timerSave{}}
	w.stateLimits = limits.State.OrDefault()
	w.world = script.State{World: true, Reserve: w.reserveWorld, Changed: w.change, Limits: w.stateLimits}
	w.game = script.State{Game: true, Reserve: w.reserveWorld, Changed: w.changeGame, Limits: w.stateLimits, Choices: p.SettingChoices()}
	WithClientBudget(DefaultClientRate, DefaultClientBurst)(w)
	for _, o := range opts {
		o(w)
	}
	w.lastLeft = w.now()
	w.runID = strconv.FormatInt(w.now().UnixMilli(), 36)
	w.typedBy, w.typedCount, w.nameWords = map[string]*typedWords{}, map[string]int{}, map[string]struct{}{}
	w.vocab = w.packWords()
	w.world.Check = w.shareable
	var timers []store.Timer // the room timers, restored once the scripts compiled
	if w.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
		vals, err := w.store.WorldState(ctx, w.worldID)
		if err == nil {
			err = w.world.Load(vals)
		}
		if err == nil {
			err = w.loadGame(ctx)
		}
		if err == nil {
			timers, err = w.store.RoomTimers(ctx, w.worldID)
		}
		if err == nil {
			err = w.loadClock(ctx)
		}
		cancel()
		if err != nil {
			w.rt.Close()
			return nil, fmt.Errorf("world state: %w", err)
		}
	}
	w.rt.SetPack(w.packInfo())
	w.applySettings()
	modules := w.modules()
	for _, name := range slices.Sorted(maps.Keys(modules)) {
		sc, err := w.rt.Compile("lib/"+name+".lua", modules[name])
		if err == nil {
			err = w.rt.AddModule(name, sc)
		}
		if err != nil {
			w.rt.Close()
			return nil, fmt.Errorf("lib/%s.lua: %w", name, err)
		}
	}
	for id, room := range p.Rooms {
		if room.Source == "" {
			continue
		}
		sc, err := w.rt.Compile("scripts/"+room.Script, room.Source)
		if err == nil {
			var env *script.Env
			if env, err = w.rt.Instance(sc); err == nil {
				w.rt.Release(env)
			}
		}
		if err != nil {
			w.rt.Close()
			return nil, fmt.Errorf("%s: %w", room.File, err)
		}
		w.scripts[id] = sc
	}
	// Last, under w.mu, so a failed script leaves no timer behind: an
	// overdue timer fires at once, on its own goroutine.
	w.mu.Lock()
	w.restoreRoomTimers(timers)
	w.mu.Unlock()
	return w, nil
}

// Close ends every paused hook and frees the world's Lua state. Sessions
// still connected get an error for anything they send.
func (w *World) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeLocked()
}

// closeLocked is Close. The caller holds w.mu.
func (w *World) closeLocked() {
	if w.closed {
		return
	}
	w.closed = true
	w.checkGame() // stops the game clock
	w.writeClock()
	for s := range w.sessions {
		s.stopTimers()
	}
	w.stopRoomTimers()
	w.rt.Close()
}

// NewSession places a player in the world. client is the player's address
// key from package ws, which the Lua time budget counts by; "" has no
// budget (tests and local tools).
//
// In a world with a store, who must be signed in: the account's character
// comes back where it left, with its state.player, and its id as
// player.id. A session the character still has elsewhere ends first. A
// world without a store places a new player without an account in the
// start room.
func (w *World) NewSession(client string, who ws.Who) (ws.Session, []ws.Message) {
	s := &session{w: w, client: budgetKey(client), member: who.GM, gm: who.GM && !who.AsPlayer, asPlayer: who.AsPlayer, account: who.Account}
	s.state.Reserve, s.state.Limits = s.reserve, w.stateLimits
	w.mu.Lock()
	defer w.mu.Unlock()
	room := w.pack.Start
	if w.store == nil {
		w.players++
		s.id = "p" + strconv.Itoa(w.players)
		s.name = w.freeName()
	} else {
		if w.closed {
			// Ended or unloaded: loading the character would write to a
			// world that may be finished.
			return nil, []ws.Message{{Type: "error", Message: ErrClosedMessage}}
		}
		var err error
		if room, err = s.load(who); err != nil {
			if !errors.Is(err, errNoAccount) {
				w.log.Warn("player could not join", "account", who.Account, "err", err)
			}
			return nil, []ws.Message{{Type: "error", Message: joinError(err)}}
		}
	}
	if !w.closed {
		w.joinedName(s.name)
		w.arrivals++
		s.joined = w.arrivals
		w.sessions[s] = struct{}{}
		if s.char != 0 {
			w.chars[s.char] = s
		}
		w.checkGame()
	}
	if !w.pausedAt.IsZero() {
		s.out = append([]ws.Message{pauseMessage(true)}, s.out...) // before the countdowns, which stand still
	}
	s.out = append(s.out, w.replay(room)...) // what happened here while the player was away
	return s, s.enterLocked(room, false)
}

// Online is how many players are in the world now.
func (w *World) Online() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.sessions)
}

// Closed reports whether the world was closed.
func (w *World) Closed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

// IdleSince reports since when the world has had no players; ok is false
// while someone plays, or once it closed.
func (w *World) IdleSince() (since time.Time, ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastLeft, !w.closed && len(w.sessions) == 0
}

// CloseIfIdle closes the world when nobody has played it since before
// (or exactly at) cutoff, and reports whether it did. A player joining at
// the same time either comes first, and the world stays, or finds the
// world closed.
func (w *World) CloseIfIdle(cutoff time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || len(w.sessions) > 0 || w.lastLeft.After(cutoff) {
		return false
	}
	w.closeLocked()
	return true
}

// CloseIfEmpty closes the world when nobody plays it, and reports whether
// it did.
func (w *World) CloseIfEmpty() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || len(w.sessions) > 0 {
		return false
	}
	w.closeLocked()
	return true
}

// End sends every player away with msg, as if they left, and closes the
// world: their connections close after msg. A world that world.finish
// ends shows its text first.
func (w *World) End(msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	last := []ws.Message{{Type: "error", Message: msg}}
	if w.finishText != "" {
		last = append([]ws.Message{{Type: "text", Text: w.finishText}}, last...)
	}
	for s := range w.sessions {
		s.end()
		s.lagged = true // Pending reports the session over: the connection closes
		s.queue, s.queueBytes = last, 0
		if s.notify != nil {
			s.notify()
		}
	}
	w.closeLocked()
}
