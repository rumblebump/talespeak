// Package script runs story and rule pack Lua in a sandbox: no files, no
// network, no OS access, and every call into Lua has a time and a memory
// budget. Hooks run as coroutines, so ui.dialog can pause a script until the
// player picks an option. See docs/lua-sandbox.md.
//
// A Runtime is not safe for concurrent use; the world that owns it calls it
// under its own lock.
package script

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Limits bound what one world's Lua may use.
type Limits struct {
	// Time is the longest one call into Lua may run: loading a script, or
	// running a hook until it ends or waits in ui.dialog.
	Time time.Duration
	// CallMemory is how many bytes one call may allocate.
	CallMemory int64
	// StateMemory is how many bytes the world's Lua state may keep.
	StateMemory int64
}

// DefaultLimits suit story scripts: they describe, branch and wait, so
// anything near these limits is a bug or an attack.
var DefaultLimits = Limits{Time: 100 * time.Millisecond, CallMemory: 32 << 20, StateMemory: 64 << 20}

const (
	// MaxScriptBytes is the largest script Load accepts.
	MaxScriptBytes = 256 << 10
	// MaxStringBytes caps strings built by string.rep, string.format and
	// table.concat. The .. operator is covered by the call memory budget.
	MaxStringBytes = 1 << 20
	// MaxOutputs caps the say and room.describe calls in one call.
	MaxOutputs = 100
	// MaxTextBytes caps one say() text.
	MaxTextBytes = 4000
	// MaxDialogOptions, MaxTitleBytes and MaxOptionBytes bound ui.dialog.
	MaxDialogOptions = 9
	MaxTitleBytes    = 200
	MaxOptionBytes   = 200
)

// Errors a call can end with. Script errors wrap ErrScript.
var (
	ErrTimeout     = errors.New("script ran too long")
	ErrMemory      = errors.New("script used too much memory")
	ErrStateMemory = errors.New("world scripts keep too much memory")
	ErrScript      = errors.New("script error")
)

// Host receives what a script sends to the player during a call.
type Host interface {
	Say(text string)
	DescribeRoom()
}

// Dialog is a question a paused hook waits on.
type Dialog struct {
	Title   string
	Options []string
}

// Env is the global environment of one loaded script. Its globals do not
// leak into other scripts; the sandbox libraries are shared.
type Env struct{ table *lua.LTable }

// Thread is a hook paused in ui.dialog. Resume it with the player's choice
// or drop it; a dropped Thread is collected with its coroutine.
type Thread struct {
	co     *lua.LState
	fn     *lua.LFunction
	Dialog Dialog
}

// Runtime is one world's Lua state.
type Runtime struct {
	L      *lua.LState
	limits Limits
	lib    *lua.LTable // sandbox globals, the __index of every Env

	envs         []lua.LValue // every loaded script's globals, for measuring the state
	cur          *call        // the call in progress, nil between calls
	sinceWalk    int64        // bytes allocated by calls since the state was last measured
	stateExceeds bool         // set once the state is over StateMemory; every later call fails
}

// call is what Go functions called from Lua need during one call.
type call struct {
	host    Host
	outputs int
	dialog  *Dialog
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
	r := &Runtime{L: L, limits: limits}
	r.lib = r.openSandbox()
	return r
}

// Close frees the Lua state.
func (r *Runtime) Close() { r.L.Close() }

// Load compiles and runs a script's top level in a new Env. name is used in
// error messages, for example "rooms/cell.lua".
func (r *Runtime) Load(name, src string) (*Env, error) {
	if len(src) > MaxScriptBytes {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrScript, name, MaxScriptBytes)
	}
	fn, err := r.L.Load(strings.NewReader(src), name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScript, err)
	}
	env := r.L.NewTable()
	meta := r.L.NewTable()
	meta.RawSetString("__index", r.lib)
	env.Metatable = meta
	fn.Env = env

	th, err := r.run(nil, r.L.NewThread, fn)
	if err != nil {
		return nil, err
	}
	if th != nil {
		return nil, fmt.Errorf("%w: %s calls ui.dialog outside a hook", ErrScript, name)
	}
	r.envs = append(r.envs, env)
	return &Env{table: env}, nil
}

// HasHook reports whether the script defines a global function name.
func (e *Env) HasHook(name string) bool {
	_, ok := e.table.RawGetString(name).(*lua.LFunction)
	return ok
}

// Run calls hook(player) from env in a new coroutine. It returns a Thread
// when the hook waits in ui.dialog, and nil when it finished or the script
// has no such hook.
func (r *Runtime) Run(host Host, env *Env, hook, player string) (*Thread, error) {
	fn, ok := env.table.RawGetString(hook).(*lua.LFunction)
	if !ok {
		return nil, nil
	}
	p := r.L.NewTable()
	p.RawSetString("name", lua.LString(player))
	return r.run(host, r.L.NewThread, fn, p)
}

// Resume continues a paused hook; ui.dialog returns choice (1-based) and the
// option's label. The caller checks that choice is in range.
func (r *Runtime) Resume(host Host, th *Thread, choice int) (*Thread, error) {
	if choice < 1 || choice > len(th.Dialog.Options) {
		return nil, fmt.Errorf("choice %d out of range", choice)
	}
	again := func() (*lua.LState, context.CancelFunc) { return th.co, nil }
	return r.run(host, again, th.fn, lua.LNumber(choice), lua.LString(th.Dialog.Options[choice-1]))
}

// run resumes a coroutine (a new one from thread) with the budgets of one call.
func (r *Runtime) run(host Host, thread func() (*lua.LState, context.CancelFunc), fn *lua.LFunction, args ...lua.LValue) (*Thread, error) {
	if r.stateExceeds {
		return nil, ErrStateMemory
	}
	co, _ := thread()
	base, abort := context.WithCancelCause(context.Background())
	defer abort(nil)
	ctx, cancel := context.WithTimeoutCause(base, r.limits.Time, ErrTimeout)
	defer cancel()
	stop := watchAllocs(ctx, r.limits.CallMemory, func() { abort(ErrMemory) })

	r.cur = &call{host: host}
	co.SetContext(ctx)
	state, err, _ := r.L.Resume(co, fn, args...)
	co.RemoveContext()
	dialog := r.cur.dialog
	r.cur = nil
	r.sinceWalk += stop()

	if err != nil {
		if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
			return nil, cause
		}
		return nil, fmt.Errorf("%w: %v", ErrScript, err)
	}
	if err := r.checkState(); err != nil {
		return nil, err
	}
	if state == lua.ResumeYield {
		if dialog == nil {
			return nil, fmt.Errorf("%w: coroutine yielded without ui.dialog", ErrScript)
		}
		return &Thread{co: co, fn: fn, Dialog: *dialog}, nil
	}
	return nil, nil
}

// checkState measures what the state keeps once enough has been allocated
// since the last measurement for it to have grown past the limit.
func (r *Runtime) checkState() error {
	if r.sinceWalk < r.limits.StateMemory/8 {
		return nil
	}
	r.sinceWalk = 0
	roots := append([]lua.LValue{r.L.G.Global, r.L.G.Registry}, r.envs...)
	if retained(roots, r.limits.StateMemory) > r.limits.StateMemory {
		r.stateExceeds = true
		return ErrStateMemory
	}
	return nil
}
