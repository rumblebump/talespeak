// Package script runs story and rule pack Lua in a sandbox: no files, no
// network, no OS access, and every call into Lua has a time and a memory
// budget. See docs/lua-sandbox.md.
//
// Each hook runs on its own goroutine with its own Lua thread, and
// ui.dialog blocks that goroutine until the player answers. Blocking, rather
// than yielding a coroutine, lets a dialog wait anywhere: inside pcall, a
// sort comparator or a metamethod. Lua code only ever runs while the caller
// of Load, Run, Resume or Cancel waits for it, so the world's lock around
// those calls is the only lock the Lua state needs.
package script

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

// Limits bound what one world's Lua may use.
type Limits struct {
	// Time is the longest one call into Lua may run: loading a script, or
	// running a hook until it ends or waits in ui.dialog.
	Time time.Duration
	// CallMemory is how many bytes one call may allocate.
	CallMemory int64
	// PlayerMemory is how many bytes one Instance may keep, with what its
	// paused hooks hold: one player's state in one room. 0 means
	// StateMemory.
	PlayerMemory int64
	// StateMemory is how many bytes all Instances of the world together
	// may keep.
	StateMemory int64
	// State bounds state.player, state.world and state.game; zero fields
	// are DefaultStateLimits. The game gives its States the same limits.
	State StateLimits
}

// DefaultLimits suit story scripts: they describe, branch and wait, so
// anything near these limits is a bug or an attack. A word list of 10000
// entries keeps about 1.5 MiB.
var DefaultLimits = Limits{Time: 100 * time.Millisecond, CallMemory: 32 << 20, PlayerMemory: 4 << 20, StateMemory: 256 << 20, State: DefaultStateLimits}

const (
	// MaxScriptBytes is the largest script Load accepts.
	MaxScriptBytes = 256 << 10
	// MaxStringBytes is the longest string a script can build, with ..,
	// string.rep, string.format or table.concat.
	MaxStringBytes = 1 << 20
	// MaxOutputs caps the say and room.describe calls in one call.
	MaxOutputs = 100
	// MaxTextBytes caps one say() text.
	MaxTextBytes = 4000
	// MaxDialogOptions, MaxTitleBytes and MaxOptionBytes bound ui.dialog
	// and ui.select.
	MaxDialogOptions = 9
	MaxTitleBytes    = 200
	MaxOptionBytes   = 200
	// MaxItems, MaxItemIDBytes and MaxLabelBytes bound ui.items.
	MaxItems       = 20
	MaxItemIDBytes = 64
	MaxLabelBytes  = 100
	// MaxCaptionBytes bounds the caption of ui.image.
	MaxCaptionBytes = 200
	// MaxAnswerBytes bounds a typed answer to ui.ask.
	MaxAnswerBytes = 500
)

// Errors a call can end with. Script errors wrap ErrScript.
var (
	ErrTimeout = errors.New("script ran too long")
	ErrMemory  = errors.New("script used too much memory")
	// ErrStateMemory is an Instance keeping more than PlayerMemory. Every
	// later call on it fails too; Release it and start a new one.
	ErrStateMemory = errors.New("script keeps too much memory")
	// ErrFull is a world whose Instances together keep more than
	// StateMemory: the call fails, and new Instances are refused until
	// others are released.
	ErrFull   = errors.New("world scripts keep too much memory")
	ErrScript = errors.New("script error")
	ErrClosed = errors.New("world closed")
	// ErrAnswer is an answer the question does not accept; the hook stays
	// paused and can be answered again.
	ErrAnswer = errors.New("answer not accepted")
)

// errLeft ends a paused hook whose player left.
const errLeft = "the player left"

// cancelTime is the time budget of a hook ending after its player left. It
// can catch the error, but it gets no more than this, so leaving players and
// shutdown stay cheap.
const cancelTime = 5 * time.Millisecond

// Env is the global environment of one loaded script. Its globals do not
// leak into other scripts, and the shared libraries are read-only.
type Env struct {
	table    *lua.LTable
	commands []Command // the script's commands table, read once its top level ran
}

// Runtime is one world's Lua state.
type Runtime struct {
	L      *lua.LState
	limits Limits
	lib    *lua.LTable               // shared globals, the __index of every Env
	locked map[*lua.LTable]bool      // read-only tables that rawset refuses
	envs   map[*lua.LTable]*envState // every Instance not released, by its globals
	paused map[*Thread]struct{}      // hooks waiting in ui.dialog
	shared map[lua.LValue]struct{}   // what every Instance reaches but none keeps: the libraries

	cur   *call // the call in progress, nil between calls
	total int64 // the sum of kept and sinceWalk over envs: at least what they keep
	extra int64 // what Reserve holds for the world, outside any Instance

	modules map[string]*Script          // require's modules by name
	loaded  map[*lua.LTable]*lua.LTable // each Instance's required modules, by its globals
	closed  bool

	canGM, canPlayer *lua.LFunction // player:can (perm.go)
}

// envState is what one Instance keeps, as last measured, plus what its
// calls allocated since: it keeps at most kept+sinceWalk.
type envState struct {
	kept      int64
	sinceWalk int64
	broken    bool            // over PlayerMemory; every later call fails
	perms     map[string]perm // what perm.define defined in this instance
}

// call is one stretch of Lua running: its budgets, and what Go functions
// called from Lua need.
type call struct {
	host    Host
	thread  *Thread
	ctx     context.Context
	end     func() int64 // stops the budgets and returns the bytes allocated
	outputs int
}

// New returns a Runtime with the sandbox libraries loaded.
func New(limits Limits) *Runtime {
	L := lua.NewState(lua.Options{
		SkipOpenLibs:        true,
		CallStackSize:       200,
		RegistrySize:        1024,
		RegistryMaxSize:     64 * 1024,
		MinimizeStackMemory: true,
	})
	if limits.PlayerMemory <= 0 || limits.PlayerMemory > limits.StateMemory {
		limits.PlayerMemory = limits.StateMemory
	}
	limits.State = limits.State.OrDefault()
	r := &Runtime{L: L, limits: limits, locked: map[*lua.LTable]bool{}, paused: map[*Thread]struct{}{}, envs: map[*lua.LTable]*envState{},
		loaded: map[*lua.LTable]*lua.LTable{}}
	r.lib = r.openSandbox()
	r.canGM, r.canPlayer = r.canFunc(true), r.canFunc(false)
	r.shared = map[lua.LValue]struct{}{r.lib: {}, L.G.Global: {}, L.G.Registry: {}, r.canGM: {}, r.canPlayer: {}}
	return r
}

// Close ends every paused hook and frees the Lua state. Later calls fail
// with ErrClosed.
func (r *Runtime) Close() {
	if r.closed {
		return
	}
	for th := range r.paused {
		r.Cancel(th)
	}
	r.closed = true
	r.L.Close()
}

// Script is a compiled script. Each Instance of it has its own globals, so
// every player can run it with their own state.
type Script struct {
	name  string
	proto *lua.FunctionProto
}

// Compile checks and compiles a script without running it. name is used in
// error messages, for example "scripts/cell.lua".
func (r *Runtime) Compile(name, src string) (*Script, error) {
	if r.closed {
		return nil, ErrClosed
	}
	if len(src) > MaxScriptBytes {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrScript, name, MaxScriptBytes)
	}
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScript, err)
	}
	if err := rewriteConcat(chunk); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrScript, name, err)
	}
	proto, err := lua.Compile(chunk, name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScript, err)
	}
	return &Script{name: name, proto: proto}, nil
}

// Instance runs the script's top level in a new Env. The Env counts toward
// the world's memory until Release. It fails with ErrFull while the world's
// Instances keep too much.
func (r *Runtime) Instance(s *Script) (*Env, error) {
	if r.closed {
		return nil, ErrClosed
	}
	if r.full() {
		return nil, ErrFull
	}
	fn := r.L.NewFunctionFromProto(s.proto)
	env := &Env{table: r.newEnv()}
	fn.Env = env.table
	r.envs[env.table] = &envState{}

	th, err := r.start(nil, env.table, fn, Hook{})
	if err != nil {
		r.Release(env)
		return nil, err
	}
	if th != nil { // ui.dialog refuses to wait without a player, so this is a bug
		r.Cancel(th)
		r.Release(env)
		return nil, fmt.Errorf("%w: %s waits in ui.dialog outside a hook", ErrScript, s.name)
	}
	env.commands = readCommands(env.table)
	return env, nil
}

// Release forgets env: its globals no longer count toward the world's
// memory and can be freed, and later calls on it fail. Cancel its paused
// hooks first.
func (r *Runtime) Release(e *Env) {
	if st := r.envs[e.table]; st != nil {
		r.total -= st.kept + st.sinceWalk
		delete(r.envs, e.table)
	}
	delete(r.loaded, e.table)
}

// Load compiles a script and runs its top level in a new Env.
func (r *Runtime) Load(name, src string) (*Env, error) {
	s, err := r.Compile(name, src)
	if err != nil {
		return nil, err
	}
	return r.Instance(s)
}

// HasHook reports whether the script defines a global function name.
func (e *Env) HasHook(name string) bool {
	_, ok := e.table.RawGetString(name).(*lua.LFunction)
	return ok
}
