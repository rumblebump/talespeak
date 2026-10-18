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
	"math"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

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

// DefaultClientRate and DefaultClientBurst share a world's Lua time between
// client addresses: one address may use DefaultClientBurst at once, then
// DefaultClientRate per second. Every typed line and click runs a hook under
// the world's lock, so without this one address with many connections could
// keep the world busy for everyone. A class behind one NAT address stays far
// below it with ordinary packs.
const (
	DefaultClientRate  = 250 * time.Millisecond
	DefaultClientBurst = 2 * time.Second
)

// DefaultClientMemory is how much script state the players of one client
// address may keep in a world together: a quarter of the default world
// limit, so a few addresses holding many connections cannot fill a world
// and keep everyone else out. A class of 30 behind one NAT address fits
// with a 10000-word list in one room; word lists in several rooms cost up
// to MaxRoomInstances times that per player.
const DefaultClientMemory = 64 << 20

// ErrBusy is returned when a client address has used up its Lua time.
var ErrBusy = errors.New("this address used up its script time")

// ErrClientFull is returned when the players of a client address keep their
// share of the world's script state.
var ErrClientFull = errors.New("this address keeps its share of the world's script state")

// maxBudgets bounds the per-client budget map, as in package ws.
const maxBudgets = 10000

// budgetPrefix is how much of an IPv6 address the Lua time budget counts
// by. Connections count by /64 (package ws), but one home or school often
// has a /56 or a /48: counting by /64 would give it hundreds of budgets.
// The budget only shares time, so a wide key costs little.
const budgetPrefix = 48

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

	mu       sync.Mutex // guards rt, budgets, sessions, every session's fields and closed, and every Lua call: gopher-lua states are not goroutine safe
	rt       *script.Runtime
	scripts  map[string]*script.Script // room id -> its compiled script
	sessions map[*session]struct{}
	closed   bool

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

	world       script.State // state.world, shared by every player
	changed     []string     // world keys the running hook changed
	delivering  bool         // a delivery of on_change is due
	lastDeliver time.Time    // when the last delivery started

	players    int                              // players who joined, for player ids
	arrivals   int64                            // rooms entered, for the order of room.players
	roomTimers map[string]map[string]*roomTimer // room id -> name -> its timer
	timerNews  map[roomTimerKey]struct{}        // room timers the running hook changed
	timerDue   map[roomTimerKey]*session        // room timers changed since the last delivery -> who changed them last

	timersDelivering bool      // a delivery of room timers is due
	lastTimers       time.Time // when the last delivery of room timers started
}

// budget is a token bucket of Lua time in seconds. It may go below zero
// after a long call; the client then waits until it is positive again.
type budget struct {
	left float64
	last time.Time
}

// NewWorld compiles every room script of p and runs each top level once, so
// a script that fails fails the world at start, with the room's file in the
// error.
func NewWorld(p *pack.Pack, limits script.Limits, log *slog.Logger, opts ...Option) (*World, error) {
	w := &World{log: log, pack: p, rt: script.New(limits), scripts: map[string]*script.Script{}, sessions: map[*session]struct{}{},
		budgets: map[string]*budget{}, now: time.Now, after: afterFunc, clientMemory: DefaultClientMemory, clientEnvs: map[string]map[*script.Env]struct{}{},
		clientState: map[string]int64{}, roomTimers: map[string]map[string]*roomTimer{}, timerNews: map[roomTimerKey]struct{}{},
		timerDue: map[roomTimerKey]*session{}, chars: map[int64]*session{}, lost: map[int64]store.Save{}}
	w.world = script.State{World: true, Reserve: w.reserveWorld, Changed: w.change}
	WithClientBudget(DefaultClientRate, DefaultClientBurst)(w)
	for _, o := range opts {
		o(w)
	}
	if w.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
		vals, err := w.store.WorldState(ctx, w.worldID)
		cancel()
		if err == nil {
			err = w.world.Load(vals)
		}
		if err != nil {
			w.rt.Close()
			return nil, fmt.Errorf("world state: %w", err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(p.Modules)) {
		sc, err := w.rt.Compile("lib/"+name+".lua", p.Modules[name])
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
	return w, nil
}

// Close ends every paused hook and frees the world's Lua state. Sessions
// still connected get an error for anything they send.
func (w *World) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
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
	s := &session{w: w, client: budgetKey(client)}
	s.state.Reserve = s.reserve
	w.mu.Lock()
	defer w.mu.Unlock()
	room := w.pack.Start
	if w.store == nil {
		w.players++
		s.id = "p" + strconv.Itoa(w.players)
		s.name = w.freeName()
	} else {
		var err error
		if room, err = s.load(who); err != nil {
			if !errors.Is(err, errNoAccount) {
				w.log.Warn("player could not join", "account", who.Account, "err", err)
			}
			return nil, []ws.Message{{Type: "error", Message: joinError(err)}}
		}
	}
	if !w.closed {
		w.sessions[s] = struct{}{}
		if s.char != 0 {
			w.chars[s.char] = s
		}
	}
	return s, s.enterLocked(room, false)
}

// budgetKey widens an IPv6 key to budgetPrefix bits.
func budgetKey(client string) string {
	p, err := netip.ParsePrefix(client)
	if err != nil || !p.Addr().Is6() || p.Bits() <= budgetPrefix {
		return client
	}
	wide, err := p.Addr().Prefix(budgetPrefix)
	if err != nil {
		return client
	}
	return wide.String()
}

// timed runs f, a call into Lua, and charges its time to client. It
// refuses with ErrBusy while the client's budget is used up. The caller
// holds w.mu.
func (w *World) timed(client string, f func() error) error {
	if client == "" {
		return f()
	}
	now := w.now()
	b := w.budgets[client]
	if b == nil {
		b = &budget{left: w.burst, last: now}
		if !w.pruneBudgets(now) {
			b.left = min(w.burst, w.rate) // a full map: start with one second's share
		}
		w.budgets[client] = b
	}
	b.left = min(w.burst, b.left+now.Sub(b.last).Seconds()*w.rate)
	b.last = now
	if b.left <= 0 {
		return ErrBusy
	}
	return w.charge(b, now, f)
}

// charged runs f like timed, but also when client's budget is used up,
// which then goes further below zero: for what must run, such as a player
// leaving. The caller holds w.mu.
func (w *World) charged(client string, f func() error) error {
	b := w.budgets[client]
	if b == nil {
		return f() // a full budget, or none at all ("")
	}
	now := w.now()
	b.left = min(w.burst, b.left+now.Sub(b.last).Seconds()*w.rate)
	b.last = now
	return w.charge(b, now, f)
}

// charge runs f, which starts at now, and takes its time from b.
func (w *World) charge(b *budget, now time.Time, f func() error) error {
	err := f()
	end := w.now()
	b.left -= end.Sub(now).Seconds()
	b.last = end
	return err
}

// pruneBudgets makes room for a new budget: past maxBudgets, budgets that
// have refilled are dropped. When none has, it drops the one closest to
// full and reports false: the new budget then starts small, so a client
// cannot push its own used-up budget out of the map to get a full one. The
// caller holds w.mu.
func (w *World) pruneBudgets(now time.Time) bool {
	if len(w.budgets) < maxBudgets {
		return true
	}
	fullest, most := "", math.Inf(-1)
	for k, b := range w.budgets {
		left := b.left + now.Sub(b.last).Seconds()*w.rate
		if left >= w.burst {
			delete(w.budgets, k)
		} else if left > most {
			fullest, most = k, left
		}
	}
	if len(w.budgets) < maxBudgets {
		return true
	}
	delete(w.budgets, fullest)
	return false
}

// clientKept is at least what the players of client keep in script
// instances and state.player. The caller holds w.mu.
func (w *World) clientKept(client string) int64 {
	n := w.clientState[client]
	for env := range w.clientEnvs[client] {
		n += w.rt.Kept(env)
	}
	return n
}

// kept is what env keeps; 0 for nil. The caller holds w.mu.
func (w *World) kept(env *script.Env) int64 {
	if env == nil {
		return 0
	}
	return w.rt.Kept(env)
}

// release frees one of client's script instances. The caller holds w.mu.
func (w *World) release(client string, env *script.Env) {
	if !w.closed {
		w.rt.Release(env)
	}
	if envs := w.clientEnvs[client]; envs != nil {
		delete(envs, env)
		if len(envs) == 0 {
			delete(w.clientEnvs, client)
		}
	}
}

// session is one player. Handle and Close are called from the
// connection's goroutine; timers call in from their own. w.mu guards every
// field.
type session struct {
	w       *World
	id      string // player.id: unique in the world while it runs, and for ever with a store
	char    int64  // the character in the store; 0 without one
	name    string
	client  string
	room    string
	arrived int64 // when the player came into room, for room.players
	closed  bool
	leaving bool // on_leave runs: the player is no longer among room.players

	envs     map[string]*script.Env // room id -> this player's instance of its script
	pending  *script.Thread         // the hook waiting in ui.dialog, if any
	dialogID string
	dialogs  int           // dialogs shown, for unique ids
	items    []script.Item // what the player can click now
	state    script.State  // state.player, shared by the player's rooms
	dirty    []string      // state.player keys changed since the last save
	moved    bool          // room changed since the last save
	replaced bool          // the player connected again elsewhere: this session ended
	changed  []string      // world keys others changed that on_change has not seen yet
	next     string        // the room room.go moves the player to when the hook ends
	visited  []string      // rooms entered, the latest last
	moves    int           // room.go moves done for the current message

	timers      map[string]*timer // the player's timers, by name
	dialogTimer func() bool       // stops the open question's timeout, if it has one

	out        []ws.Message // collects output during one hook call
	queue      []ws.Message // pushed output waiting for Pending
	queueBytes int          // about what queue takes on the wire
	notify     func()       // tells the connection that queue has messages; nil until SetNotify
	lagged     bool         // queue overflowed: the connection should close

	// readOnlyWorld is set while on_change runs, and for everything it
	// starts (room.go, timers): none of it can write state.world, so a
	// change never bounces between players by itself.
	readOnlyWorld bool
}

// timer is one of the player's timers.
type timer struct {
	deadline time.Time
	stop     func() bool
	show     string // the label the player sees, "" for a secret timer
	readOnly bool   // started under on_change: on_timer cannot write state.world
}

// MaxMoves caps the rooms one player message can move through, so rooms
// whose on_enter calls room.go cannot send a player round in circles, and
// one message cannot hold the world for many hooks' time.
const MaxMoves = 4

// Handle answers m. Pushed messages still waiting go first, so a reply
// never overtakes what was pushed before it.
func (s *session) Handle(m ws.Message) []ws.Message {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.closed {
		return []ws.Message{{Type: "error", Message: endedMessage}}
	}
	out := s.handle(m)
	if len(s.queue) > 0 {
		out = append(s.queue, out...)
		s.queue, s.queueBytes = nil, 0
	}
	return out
}

func (s *session) handle(m ws.Message) []ws.Message {
	s.moves = 0
	switch m.Type {
	case "choose":
		return s.choose(m)
	case "input":
		return s.input(m)
	case "click":
		return s.click(m)
	case "answer":
		return s.answer(m)
	default:
		return []ws.Message{{Type: "error", Message: "unknown message type"}}
	}
}

// Close ends the hook waiting on this player, if any, runs on_leave, stops
// their timers and frees the player's script instances.
func (s *session) Close() {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if !s.closed {
		s.end()
	}
}

// end is Close. The caller holds w.mu.
func (s *session) end() {
	s.leave()
	s.closed = true
	delete(s.w.sessions, s)
	if s.char != 0 && s.w.chars[s.char] == s {
		delete(s.w.chars, s.char)
	}
	s.stopTimers()
	s.drop()
	for _, env := range s.envs {
		s.w.release(s.client, env)
	}
	s.envs = nil
	s.out, s.queue, s.queueBytes = nil, nil, 0 // a room timer may keep s until it runs out
	_ = s.reserve(-s.state.Size())
	s.state = script.State{}
}

// env returns the player's instance of room's script, starting it on first
// use; nil when the room has no script. When the player keeps
// MaxRoomInstances already, the room entered longest ago gives way. The
// caller holds w.mu.
func (s *session) env(room string) (*script.Env, error) {
	env, victim, _, err := s.start(room)
	if victim != "" {
		s.free(victim)
	}
	return env, err
}

// start is env without freeing: it returns the room whose instance should
// give way, if any, and whether it started a new instance, so a move can
// free the one or the other once it knows whether it happens. The caller
// holds w.mu.
func (s *session) start(room string) (env *script.Env, victim string, started bool, err error) {
	sc := s.w.scripts[room]
	if sc == nil {
		return nil, "", false, nil
	}
	if env := s.envs[room]; env != nil {
		return env, "", false, nil
	}
	if s.w.closed {
		return nil, "", false, script.ErrClosed
	}
	victim = s.victim(room)
	if s.client != "" && s.w.clientKept(s.client)-s.w.kept(s.envs[victim]) >= s.w.clientMemory {
		return nil, "", false, ErrClientFull
	}
	err = s.w.timed(s.client, func() (err error) {
		env, err = s.w.rt.Instance(sc)
		return err
	})
	if err != nil {
		return nil, "", false, err
	}
	if s.envs == nil {
		s.envs = map[string]*script.Env{}
	}
	s.envs[room] = env
	if s.client != "" {
		if s.w.clientEnvs[s.client] == nil {
			s.w.clientEnvs[s.client] = map[*script.Env]struct{}{}
		}
		s.w.clientEnvs[s.client][env] = struct{}{}
	}
	return env, victim, true, nil
}

// free releases the player's instance of room's script. The caller holds
// w.mu.
func (s *session) free(room string) {
	if env := s.envs[room]; env != nil {
		s.w.release(s.client, env)
		delete(s.envs, room)
	}
}

// MaxRoomInstances is how many room script instances one player keeps:
// coming back to one of the last rooms finds its globals, older rooms
// start over. State that must last goes into state.player (T-0023).
const MaxRoomInstances = 4

// victim is the room entered longest ago, other than room and the current
// one, whose instance gives way when the player already keeps
// MaxRoomInstances; "" when none needs to. The caller holds w.mu.
func (s *session) victim(room string) string {
	if len(s.envs) < MaxRoomInstances {
		return ""
	}
	for _, old := range s.visited {
		if s.envs[old] != nil && old != room && old != s.room {
			return old
		}
	}
	for old := range s.envs { // not in visited: should not happen, but never exceed the cap
		if old != room && old != s.room {
			return old
		}
	}
	return ""
}

// reset frees the player's instance of room's script, which keeps too much
// to run again; the next call starts a new one. The caller holds w.mu.
func (s *session) reset(room string) {
	if env := s.envs[room]; env != nil {
		s.drop()
		s.w.release(s.client, env)
		delete(s.envs, room)
	}
}

// drop cancels the pending hook and tells the client its question is
// gone. The caller holds w.mu.
func (s *session) drop() {
	if s.pending == nil {
		return
	}
	if !s.w.closed {
		s.w.rt.Cancel(s.pending)
	}
	s.out = append(s.out, ws.Message{Type: "close", ID: s.dialogID})
	s.unpend()
}

// unpend forgets the pending hook and stops its question's timeout. The
// caller holds w.mu.
func (s *session) unpend() {
	if s.dialogTimer != nil {
		s.dialogTimer()
		s.dialogTimer = nil
	}
	s.pending = nil
}

// enterLocked moves the player into room and runs its on_enter hook; without
// moved, the player has just joined. The caller holds w.mu. After room.go
// (moved), the output so far stays in front of the new room's, and the client is
// told the new room's items first, since its on_enter may not describe it.
// A move whose room cannot start, or whose on_enter cannot run yet because
// the player's address used up its Lua time, leaves the player where they
// were.
func (s *session) enterLocked(room string, moved bool) []ws.Message {
	s.drop()
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	if !moved {
		s.place(room)
		if room != s.w.pack.Start {
			// A player who continues elsewhere learns the room's items as
			// after a move: its on_enter may not describe it.
			s.out = append(s.out, ws.Message{Type: "items", Items: wsItems(s.items)})
		}
		env, err := s.env(room)
		if err != nil {
			return s.after(nil, err)
		}
		if env == nil {
			s.DescribeRoom()
			return s.flush()
		}
		return s.after(s.run1(func() (*script.Thread, error) { return s.w.rt.RunHook(s, env, s.onEnter()) }))
	}
	env, victim, started, err := s.start(room)
	if err != nil {
		return s.refuseMove(room, err)
	}
	oldRoom, oldItems, oldVisited, oldArrived, mark := s.room, s.items, slices.Clone(s.visited), s.arrived, len(s.out)
	s.place(room)
	s.out = append(s.out, ws.Message{Type: "items", Items: wsItems(s.items)})
	if env == nil {
		s.DescribeRoom()
		return s.flush()
	}
	th, err := s.run1(func() (*script.Thread, error) { return s.w.rt.RunHook(s, env, s.onEnter()) })
	if errors.Is(err, ErrBusy) { // nothing ran: undo the move, and keep the old instances
		s.room, s.items, s.visited, s.arrived, s.out = oldRoom, oldItems, oldVisited, oldArrived, s.out[:mark]
		if started {
			s.free(room)
		}
		return s.after(th, err)
	}
	if victim != "" {
		s.free(victim)
	}
	return s.after(th, err)
}

// onEnter is the on_enter call for the player.
func (s *session) onEnter() script.Hook {
	return script.Hook{Name: "on_enter", Player: s.name, PlayerID: s.id, ReadOnlyWorld: s.readOnlyWorld}
}

// place puts the player in room with its YAML items, and shows them the
// room's timers instead of the old room's.
func (s *session) place(room string) {
	if room != s.room {
		s.moved = true
		s.showRoomTimers(s.room, room)
		s.w.arrivals++
		s.arrived = s.w.arrivals
	}
	s.room = room
	s.items = nil
	for _, it := range s.w.pack.Rooms[room].Items {
		s.items = append(s.items, script.Item{ID: it.ID, Label: it.Label})
	}
	s.visited = append(slices.DeleteFunc(s.visited, func(r string) bool { return r == room }), room)
}

// refuseMove tells the player a move to room did not happen. The caller
// holds w.mu.
func (s *session) refuseMove(room string, err error) []ws.Message {
	if errors.Is(err, ErrBusy) {
		s.out = append(s.out, ws.Message{Type: "error", Message: busyMessage})
		return s.flush()
	}
	s.w.log.Warn("room script failed", "room", room, "err", logText(err))
	s.out = append(s.out, ws.Message{Type: "error", Message: playerError(err)})
	return s.flush()
}

// choose answers the open dialog: option for ui.dialog, choices for
// ui.select.
func (s *session) choose(m ws.Message) []ws.Message {
	if s.pending == nil || m.ID != s.dialogID {
		return []ws.Message{{Type: "error", Message: "no such dialog"}}
	}
	if s.pending.Dialog.Ask {
		return []ws.Message{{Type: "error", Message: "this question wants a typed answer"}}
	}
	choices := m.Choices
	if !s.pending.Dialog.Multiple {
		choices = []int{m.Option}
	}
	if _, err := s.pending.Dialog.Check(choices); err != nil {
		return s.refused("no such option: " + err.Error())
	}
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	return s.resumed(s.run1(func() (*script.Thread, error) { return s.w.rt.Resume(s, s.pending, choices...) }))
}

// answer answers the open ui.ask with a typed line.
func (s *session) answer(m ws.Message) []ws.Message {
	if s.pending == nil || m.ID != s.dialogID || !s.pending.Dialog.Ask {
		return []ws.Message{{Type: "error", Message: "no such question"}}
	}
	if len(m.Text) > MaxInputBytes {
		return s.refused("That is too long: at most " + strconv.Itoa(MaxInputBytes) + " bytes.")
	}
	text := cleanInput(m.Text)
	if text == "" {
		return s.refused("Type an answer.")
	}
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	return s.resumed(s.run1(func() (*script.Thread, error) { return s.w.rt.Answer(s, s.pending, text) }))
}

// run1 runs f, a hook call, within the player's Lua time budget. The
// caller holds w.mu.
func (s *session) run1(f func() (*script.Thread, error)) (*script.Thread, error) {
	var th *script.Thread
	err := s.w.timed(s.client, func() (err error) {
		th, err = f()
		return err
	})
	return th, err
}

// resumed handles the end of Resume or Answer. An answer the runtime did
// not accept leaves the hook paused and the question open; after anything
// else the old hook is done. The caller holds w.mu.
func (s *session) resumed(th *script.Thread, err error) []ws.Message {
	if errors.Is(err, ErrBusy) { // nothing ran: the question stays open
		return s.refused(busyMessage)
	}
	if errors.Is(err, script.ErrAnswer) {
		if s.pending.Dialog.Ask {
			return s.refused("Type an answer.")
		}
		return s.refused("no such option")
	}
	s.unpend()
	return s.after(th, err)
}

// refused answers an answer to the open question that was not taken: the
// error carries the question's id, which tells the client the question is
// still open and can be answered again.
func (s *session) refused(msg string) []ws.Message {
	return []ws.Message{{Type: "error", ID: s.dialogID, Message: msg}}
}

// input runs the room's on_input(player, text) hook with a typed line.
func (s *session) input(m ws.Message) []ws.Message {
	if len(m.Text) > MaxInputBytes {
		return []ws.Message{{Type: "error", Message: "That is too long: at most " + strconv.Itoa(MaxInputBytes) + " bytes."}}
	}
	text := cleanInput(m.Text)
	if text == "" {
		return nil
	}
	return s.run("on_input", text)
}

// cleanInput turns control characters, newlines included, into spaces and
// trims the line, so a script only ever sees one line of text.
func cleanInput(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(text, "\uFFFD"))
	return strings.TrimSpace(text)
}

// click runs the room's on_click(player, id) hook for an item the player
// can see.
func (s *session) click(m ws.Message) []ws.Message {
	for _, it := range s.items {
		if it.ID == m.Item {
			return s.run("on_click", it.ID)
		}
	}
	return []ws.Message{{Type: "error", Message: "no such item"}}
}

// run calls hook(player, arg) of the current room. While a dialog is open,
// the player answers it first.
func (s *session) run(hook, arg string) []ws.Message {
	if s.pending != nil {
		return []ws.Message{{Type: "error", Message: "Answer the open question first."}}
	}
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	env, err := s.env(s.room)
	if err != nil {
		return s.after(nil, err)
	}
	if env == nil || !env.HasHook(hook) {
		return []ws.Message{{Type: "text", Text: "Nothing happens."}}
	}
	h := script.Hook{Name: hook, Player: s.name, PlayerID: s.id, Args: []string{arg}}
	return s.after(s.run1(func() (*script.Thread, error) { return s.w.rt.RunHook(s, env, h) }))
}

// after turns the end of a hook call into messages: the output so far, then
// the dialog it waits on, or an error the player can read.
func (s *session) after(th *script.Thread, err error) []ws.Message {
	if errors.Is(err, ErrBusy) {
		s.out = append(s.out, ws.Message{Type: "error", Message: busyMessage})
		return s.flush()
	}
	if err != nil {
		s.next = "" // a failed hook moves no one
		s.w.log.Warn("room script failed", "room", s.room, "err", logText(err))
		if errors.Is(err, script.ErrStateMemory) {
			s.reset(s.room)
		}
		s.out = append(s.out, ws.Message{Type: "error", Message: playerError(err)})
		return s.flush()
	}
	if th != nil {
		s.dialogs++
		s.pending = th
		s.dialogID = "d" + strconv.Itoa(s.dialogs)
		d := th.Dialog
		m := ws.Message{Type: "dialog", ID: s.dialogID, Title: d.Title, Options: d.Options, Speak: d.Voice.Speak, Hide: d.Voice.Hide, From: d.Voice.From}
		switch {
		case d.Multiple:
			m.Type, m.Min, m.Max = "select", d.Min, d.Max
		case d.Ask:
			m.Type, m.Strict = "ask", d.Strict
		}
		if d.Timeout > 0 {
			m.Ms = d.Timeout.Milliseconds()
			s.expireAfter(d.Timeout)
		}
		s.out = append(s.out, m)
	} else if s.next != "" {
		room := s.next
		s.next = ""
		if s.moves++; s.moves > MaxMoves {
			s.w.log.Warn("room script moved a player too often", "room", s.room, "to", room)
			s.out = append(s.out, ws.Message{Type: "error", Message: "This room's script moved you too often at once. Tell the game master."})
			return s.flush()
		}
		return s.enterLocked(room, true)
	}
	return s.flush()
}

// busyMessage answers a player whose address used up its Lua time.
const busyMessage = "Too much at once: wait a moment, then try again."

// playerError says what went wrong without script details; the log has them.
func playerError(err error) string {
	switch {
	case errors.Is(err, script.ErrClosed):
		return "This world has closed."
	case errors.Is(err, script.ErrFull):
		return "This world is full right now. Try again later."
	case errors.Is(err, ErrClientFull):
		return "Too many players from your network are in this world right now. Try again later."
	case errors.Is(err, script.ErrTimeout), errors.Is(err, script.ErrMemory), errors.Is(err, script.ErrStateMemory):
		return "This room's script hit its limits. Tell the game master."
	default:
		return "This room's script failed. Tell the game master."
	}
}

// maxLogBytes caps a script error in the log: error() messages are chosen
// by the script and could be megabytes, or contain fake log lines.
const maxLogBytes = 500

// logText is err's message cut to maxLogBytes and quoted, so newlines and
// control characters show as escapes.
func logText(err error) string {
	msg := err.Error()
	if len(msg) > maxLogBytes {
		msg = msg[:maxLogBytes] + "..."
	}
	return strconv.Quote(msg)
}

// flush returns the output of the call, and tells the other players what
// the call changed in state.world. The caller holds w.mu.
func (s *session) flush() []ws.Message {
	s.w.save(s)
	if len(s.w.changed) > 0 {
		s.w.announce(s, s.w.changed)
		s.w.changed = nil
	}
	s.w.announceTimers(s)
	out := s.out
	s.out = nil
	return out
}

// Say, DescribeRoom, Items, Image, Go, Room and PlayerState implement
// script.Host.
func (s *session) Say(text string, how script.Voice) {
	s.out = append(s.out, ws.Message{Type: "text", Text: text, Speak: how.Speak, Hide: how.Hide, From: how.From})
}

func (s *session) DescribeRoom() {
	r := s.w.pack.Rooms[s.room]
	s.out = append(s.out, ws.Message{Type: "room", ID: r.ID, Title: r.Title, Text: r.Description, Items: wsItems(s.items)})
}

func (s *session) Items(items []script.Item) {
	s.items = items
	s.out = append(s.out, ws.Message{Type: "items", Items: wsItems(items)})
}

func (s *session) Go(room string) bool {
	if s.w.pack.Rooms[room] == nil {
		return false
	}
	s.next = room
	return true
}

func (s *session) Room() string { return s.room }

func (s *session) PlayerState() *script.State { return &s.state }

func (s *session) WorldState() *script.State { return &s.w.world }

// reserve accounts for state.player growing by delta bytes: the world and
// the player's client address share the limits of script memory. The
// caller holds w.mu.
func (s *session) reserve(delta int64) error {
	w := s.w
	if delta > 0 && s.client != "" && w.clientKept(s.client)+delta > w.clientMemory {
		return errors.New("too many players from this network keep state right now")
	}
	if err := w.rt.Reserve(delta); err != nil {
		return errors.New("this world is full right now")
	}
	w.stateTotal += delta
	if s.client != "" {
		if w.clientState[s.client] += delta; w.clientState[s.client] <= 0 {
			delete(w.clientState, s.client)
		}
	}
	return nil
}

func (s *session) Image(name, caption string) bool {
	url, ok := s.w.pack.Image(name)
	if ok {
		s.out = append(s.out, ws.Message{Type: "image", URL: url, Text: caption})
	}
	return ok
}

func wsItems(items []script.Item) []ws.Item {
	out := make([]ws.Item, len(items))
	for i, it := range items {
		out[i] = ws.Item{ID: it.ID, Label: it.Label}
	}
	return out
}
