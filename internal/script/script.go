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
	// StateMemory is how many bytes the world's Lua state may keep,
	// including what paused hooks hold.
	StateMemory int64
}

// DefaultLimits suit story scripts: they describe, branch and wait, so
// anything near these limits is a bug or an attack.
var DefaultLimits = Limits{Time: 100 * time.Millisecond, CallMemory: 32 << 20, StateMemory: 64 << 20}

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
	ErrClosed      = errors.New("world closed")
)

// errLeft ends a paused hook whose player left.
const errLeft = "the player left"

// cancelTime is the time budget of a hook ending after its player left. It
// can catch the error, but it gets no more than this, so leaving players and
// shutdown stay cheap.
const cancelTime = 5 * time.Millisecond

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
// leak into other scripts, and the shared libraries are read-only.
type Env struct{ table *lua.LTable }

// Thread is a hook paused in ui.dialog. Resume it with the player's choice,
// or Cancel it when the player leaves.
type Thread struct {
	Dialog Dialog

	co       *lua.LState
	resume   chan int    // the player's choice; 0 ends the hook
	events   chan result // the end of each call: a dialog, or the hook's end
	canceled bool        // set on the hook's goroutine once resume sent 0
}

type result struct {
	dialog *Dialog
	err    error
}

// Runtime is one world's Lua state.
type Runtime struct {
	L      *lua.LState
	limits Limits
	lib    *lua.LTable          // shared globals, the __index of every Env
	locked map[*lua.LTable]bool // read-only tables that rawset refuses
	envs   []lua.LValue         // every loaded script's globals, for measuring the state
	paused map[*Thread]struct{} // hooks waiting in ui.dialog

	cur          *call // the call in progress, nil between calls
	sinceWalk    int64 // bytes allocated by calls since the state was last measured
	stateExceeds bool  // set once the state is over StateMemory; every later call fails
	closed       bool
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
	r := &Runtime{L: L, limits: limits, locked: map[*lua.LTable]bool{}, paused: map[*Thread]struct{}{}}
	r.lib = r.openSandbox()
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

// Load compiles and runs a script's top level in a new Env. name is used in
// error messages, for example "scripts/cell.lua".
func (r *Runtime) Load(name, src string) (*Env, error) {
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
	fn := r.L.NewFunctionFromProto(proto)
	env := r.newEnv()
	fn.Env = env

	th, err := r.start(nil, fn)
	if err != nil {
		return nil, err
	}
	if th != nil { // ui.dialog refuses to wait without a player, so this is a bug
		r.Cancel(th)
		return nil, fmt.Errorf("%w: %s waits in ui.dialog outside a hook", ErrScript, name)
	}
	r.envs = append(r.envs, env)
	return &Env{table: env}, nil
}

// HasHook reports whether the script defines a global function name.
func (e *Env) HasHook(name string) bool {
	_, ok := e.table.RawGetString(name).(*lua.LFunction)
	return ok
}

// Run calls hook(player) from env. It returns a Thread when the hook waits
// in ui.dialog, and nil when it finished or the script has no such hook.
func (r *Runtime) Run(host Host, env *Env, hook, player string) (*Thread, error) {
	if r.closed {
		return nil, ErrClosed
	}
	fn, ok := env.table.RawGetString(hook).(*lua.LFunction)
	if !ok {
		return nil, nil
	}
	p := r.L.NewTable()
	p.RawSetString("name", lua.LString(player))
	return r.start(host, fn, p)
}

// Resume continues a paused hook; ui.dialog returns choice (1-based) and the
// option's label. A choice out of range is an error and leaves the hook
// paused. On any other error the hook is gone.
func (r *Runtime) Resume(host Host, th *Thread, choice int) (*Thread, error) {
	if _, ok := r.paused[th]; !ok || r.closed {
		return nil, fmt.Errorf("%w: hook is not waiting", ErrScript)
	}
	if choice < 1 || choice > len(th.Dialog.Options) {
		return nil, fmt.Errorf("choice %d out of range", choice)
	}
	if r.stateExceeds {
		r.Cancel(th)
		return nil, ErrStateMemory
	}
	delete(r.paused, th)
	r.begin(host, th, r.limits.Time)
	th.resume <- choice
	return r.wait(th)
}

// Cancel ends a paused hook: ui.dialog raises an error in it, and it runs
// to its end without a player. Its output is dropped. Cancel does nothing
// for a hook that is not paused.
func (r *Runtime) Cancel(th *Thread) {
	if _, ok := r.paused[th]; !ok {
		return
	}
	delete(r.paused, th)
	r.begin(nil, th, cancelTime)
	th.resume <- 0
	// After the 0, ui.dialog refuses to wait again, so the next event is
	// the hook's end.
	_, _ = r.wait(th)
}

// Paused is the number of hooks waiting in ui.dialog.
func (r *Runtime) Paused() int { return len(r.paused) }

// start runs fn(args...) on a new hook goroutine until it ends or waits.
func (r *Runtime) start(host Host, fn *lua.LFunction, args ...lua.LValue) (*Thread, error) {
	if r.stateExceeds {
		return nil, ErrStateMemory
	}
	co, _ := r.L.NewThread()
	th := &Thread{co: co, resume: make(chan int), events: make(chan result)}
	r.begin(host, th, r.limits.Time)
	go th.main(r.cur.ctx, fn, args)
	return r.wait(th)
}

// main is the hook goroutine.
func (th *Thread) main(ctx context.Context, fn *lua.LFunction, args []lua.LValue) {
	var err error
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
		th.co.RemoveContext()
		th.events <- result{err: err}
	}()
	th.co.SetContext(ctx)
	th.co.Push(fn)
	for _, a := range args {
		th.co.Push(a)
	}
	err = th.co.PCall(len(args), 0, nil)
}

// begin starts the budgets of one call that may run for d.
func (r *Runtime) begin(host Host, th *Thread, d time.Duration) {
	base, abort := context.WithCancelCause(context.Background())
	ctx, cancel := context.WithTimeoutCause(base, d, ErrTimeout)
	stop := watchAllocs(ctx, r.limits.CallMemory, func() { abort(ErrMemory) })
	r.cur = &call{host: host, thread: th, ctx: ctx, end: func() int64 {
		n := stop()
		cancel()
		abort(nil)
		return n
	}}
}

// wait blocks until the call ends and turns its end into the result.
func (r *Runtime) wait(th *Thread) (*Thread, error) {
	res := <-th.events
	c := r.cur
	r.cur = nil
	cause := context.Cause(c.ctx)
	r.sinceWalk += c.end()

	if res.err != nil {
		if cause != nil && !errors.Is(cause, context.Canceled) {
			return nil, cause
		}
		return nil, fmt.Errorf("%w: %v", ErrScript, res.err)
	}
	if res.dialog != nil {
		th.Dialog = *res.dialog
		r.paused[th] = struct{}{}
		if err := r.checkState(); err != nil {
			r.Cancel(th)
			return nil, err
		}
		return th, nil
	}
	return nil, r.checkState()
}

// pause is called by ui.dialog on the hook goroutine: it ends the current
// call, waits for the player and continues under the next call's budgets.
func (r *Runtime) pause(L *lua.LState, d *Dialog) (int, string) {
	th := r.cur.thread
	if th.canceled {
		L.RaiseError(errLeft)
	}
	th.events <- result{dialog: d}
	choice := <-th.resume // the caller has called begin
	L.SetContext(r.cur.ctx)
	if choice == 0 {
		th.canceled = true
		L.RaiseError(errLeft)
	}
	return choice, d.Options[choice-1]
}

// checkState measures what the state keeps once enough has been allocated
// since the last measurement for it to have grown past the limit.
func (r *Runtime) checkState() error {
	if r.sinceWalk < r.limits.StateMemory/8 {
		return nil
	}
	r.sinceWalk = 0
	roots := append([]lua.LValue{r.L.G.Global, r.L.G.Registry}, r.envs...)
	for th := range r.paused {
		roots = append(roots, stackValues(th.co)...)
	}
	if retained(roots, r.limits.StateMemory) > r.limits.StateMemory {
		r.stateExceeds = true
		return ErrStateMemory
	}
	return nil
}
