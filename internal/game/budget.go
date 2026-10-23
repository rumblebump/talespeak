package game

import (
	"errors"
	"math"
	"net/netip"
	"time"

	"github.com/rumblebump/talespeak/internal/script"
)

// Lua time and script memory per client address: a token bucket of Lua
// time for each address (or IPv6 /48), and what its players' script
// instances keep, so one address with many connections cannot hold a
// world for everyone.

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

// budget is a token bucket of Lua time in seconds. It may go below zero
// after a long call; the client then waits until it is positive again.
type budget struct {
	left float64
	last time.Time
}

// ClientKey is the key the Lua time and memory budgets count client by:
// its address, or for IPv6 its /48.
func ClientKey(client string) string { return budgetKey(client) }

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
