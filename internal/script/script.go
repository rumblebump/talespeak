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
}

// DefaultLimits suit story scripts: they describe, branch and wait, so
// anything near these limits is a bug or an attack. A word list of 10000
// entries keeps about 1.5 MiB.
var DefaultLimits = Limits{Time: 100 * time.Millisecond, CallMemory: 32 << 20, PlayerMemory: 4 << 20, StateMemory: 256 << 20}

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

// Host receives what a script sends to the player during a call.
type Host interface {
	Say(text string, how Voice)
	DescribeRoom()
	// Items replaces the things in the room the player can click.
	Items(items []Item)
	// Image shows one of the pack's images; it reports false when the pack
	// has no image of that name.
	Image(name, caption string) bool
	// Go moves the player to room once the hook ends; it reports false
	// when the pack has no such room.
	Go(room string) bool
	// Room is the id of the player's current room.
	Room() string
	// PlayerState is the player's state.player; nil when there is none.
	PlayerState() *State
}

// Voice says how a line reaches the player, set by say(text, {speak =
// true, show = false}): Speak reads it aloud even when the player has not
// turned read aloud on, Hide shows it only when it cannot be spoken.
type Voice struct {
	Speak bool
	Hide  bool
}

// Item is something the player can click, set with ui.items.
type Item struct {
	ID    string
	Label string
}

// Dialog is a question a paused hook waits on: ui.dialog, ui.select or
// ui.ask.
type Dialog struct {
	Title   string
	Options []string
	// Multiple is set by ui.select: the player picks Min to Max options.
	Multiple bool
	Min, Max int
	// Ask is set by ui.ask: the player types the answer, and Options is
	// empty. Strict asks the client for no typing help.
	Ask    bool
	Strict bool
	// Voice is how the title reaches the player.
	Voice Voice
}

// answer is what a paused hook gets back: choices for ui.dialog and
// ui.select, text for ui.ask.
type answer struct {
	choices []int
	text    string
}

// Check returns the player's answer to d in ascending order, or an error
// when it is not one d accepts: one option for ui.dialog, Min to Max
// different options for ui.select.
func (d *Dialog) Check(choices []int) ([]int, error) {
	n := len(d.Options)
	if d.Ask {
		return nil, errors.New("this question wants a typed answer")
	}
	if !d.Multiple {
		if len(choices) != 1 || choices[0] < 1 || choices[0] > n {
			return nil, errors.New("pick one of the options")
		}
		return []int{choices[0]}, nil
	}
	if len(choices) < d.Min || len(choices) > d.Max {
		return nil, fmt.Errorf("pick %d to %d options", d.Min, d.Max)
	}
	seen := make([]bool, n+1)
	for _, c := range choices {
		if c < 1 || c > n || seen[c] {
			return nil, errors.New("options must be different numbers of the dialog's options")
		}
		seen[c] = true
	}
	out := make([]int, 0, len(choices))
	for c := 1; c <= n; c++ {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out, nil
}

// Env is the global environment of one loaded script. Its globals do not
// leak into other scripts, and the shared libraries are read-only.
type Env struct{ table *lua.LTable }

// Thread is a hook paused in ui.dialog or ui.select. Resume it with the
// player's choice, or Cancel it when the player leaves.
type Thread struct {
	Dialog Dialog

	co       *lua.LState
	env      *lua.LTable     // the globals of the Instance it runs in
	resume   chan *answer    // the player's answer; nil ends the hook
	events   chan result     // the end of each call: a dialog, or the hook's end
	canceled bool            // set on the hook's goroutine once resume sent nil
	moved    bool            // room.go was called: the hook may not wait again
	writes   int             // state.player.set calls, across its dialogs
	loading  map[string]bool // modules this hook is running now, for require's cycle check
}

type result struct {
	dialog *Dialog
	err    error
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
}

// envState is what one Instance keeps, as last measured, plus what its
// calls allocated since: it keeps at most kept+sinceWalk.
type envState struct {
	kept      int64
	sinceWalk int64
	broken    bool // over PlayerMemory; every later call fails
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
	r := &Runtime{L: L, limits: limits, locked: map[*lua.LTable]bool{}, paused: map[*Thread]struct{}{}, envs: map[*lua.LTable]*envState{},
		loaded: map[*lua.LTable]*lua.LTable{}}
	r.lib = r.openSandbox()
	r.shared = map[lua.LValue]struct{}{r.lib: {}, L.G.Global: {}, L.G.Registry: {}}
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

	th, err := r.start(nil, env.table, fn)
	if err != nil {
		r.Release(env)
		return nil, err
	}
	if th != nil { // ui.dialog refuses to wait without a player, so this is a bug
		r.Cancel(th)
		r.Release(env)
		return nil, fmt.Errorf("%w: %s waits in ui.dialog outside a hook", ErrScript, s.name)
	}
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

// Run calls hook(player, args...) from env. It returns a Thread when the
// hook waits in ui.dialog, and nil when it finished or the script has no
// such hook.
func (r *Runtime) Run(host Host, env *Env, hook, player string, args ...string) (*Thread, error) {
	if r.closed {
		return nil, ErrClosed
	}
	fn, ok := env.table.RawGetString(hook).(*lua.LFunction)
	if !ok {
		return nil, nil
	}
	switch st := r.envs[env.table]; {
	case st == nil:
		return nil, fmt.Errorf("%w: the script instance was released", ErrScript)
	case st.broken:
		return nil, ErrStateMemory
	}
	p := r.L.NewTable()
	p.RawSetString("name", lua.LString(player))
	if host != nil {
		p.RawSetString("room", lua.LString(host.Room()))
	}
	values := []lua.LValue{p}
	for _, a := range args {
		values = append(values, lua.LString(a))
	}
	return r.start(host, env.table, fn, values...)
}

// Resume continues a paused hook with the player's choices (1-based): one
// for ui.dialog, which returns it and its label, and Min to Max for
// ui.select. An answer the dialog does not accept (see Dialog.Check) leaves
// the hook paused, with an error wrapping ErrAnswer. On any other error the
// hook is gone.
func (r *Runtime) Resume(host Host, th *Thread, choices ...int) (*Thread, error) {
	if _, ok := r.paused[th]; !ok || r.closed {
		return nil, fmt.Errorf("%w: hook is not waiting", ErrScript)
	}
	choices, err := th.Dialog.Check(choices)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnswer, err)
	}
	return r.answer(host, th, &answer{choices: choices})
}

// Answer continues a hook paused in ui.ask with the player's typed text,
// which ui.ask returns. The caller cleans the text (one line, no control
// characters); Answer refuses an empty or too long one with ErrAnswer and
// leaves the hook paused. On any other error the hook is gone.
func (r *Runtime) Answer(host Host, th *Thread, text string) (*Thread, error) {
	if _, ok := r.paused[th]; !ok || r.closed {
		return nil, fmt.Errorf("%w: hook is not waiting", ErrScript)
	}
	if !th.Dialog.Ask {
		return nil, fmt.Errorf("%w: this question wants a choice", ErrAnswer)
	}
	if text == "" || len(text) > MaxAnswerBytes {
		return nil, fmt.Errorf("%w: an answer is 1 to %d bytes", ErrAnswer, MaxAnswerBytes)
	}
	return r.answer(host, th, &answer{text: text})
}

func (r *Runtime) answer(host Host, th *Thread, a *answer) (*Thread, error) {
	delete(r.paused, th)
	r.begin(host, th, r.limits.Time)
	th.resume <- a
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
	th.resume <- nil
	// After the nil, ui.dialog refuses to wait again, so the next event is
	// the hook's end.
	_, _ = r.wait(th)
}

// Paused is the number of hooks waiting in ui.dialog.
func (r *Runtime) Paused() int { return len(r.paused) }

// start runs fn(args...) of env on a new hook goroutine until it ends or
// waits.
func (r *Runtime) start(host Host, env *lua.LTable, fn *lua.LFunction, args ...lua.LValue) (*Thread, error) {
	co, _ := r.L.NewThread()
	th := &Thread{co: co, env: env, resume: make(chan *answer), events: make(chan result)}
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
	if st := r.envs[th.env]; st != nil {
		n := c.end()
		st.sinceWalk += n
		r.total += n
	} else {
		c.end()
	}

	if res.err != nil {
		if cause != nil && !errors.Is(cause, context.Canceled) {
			return nil, cause
		}
		return nil, fmt.Errorf("%w: %v", ErrScript, res.err)
	}
	if res.dialog != nil {
		th.Dialog = *res.dialog
		r.paused[th] = struct{}{}
		if err := r.checkState(th.env); err != nil {
			r.Cancel(th)
			return nil, err
		}
		return th, nil
	}
	return nil, r.checkState(th.env)
}

// pause is called by ui.dialog, ui.select and ui.ask on the hook
// goroutine: it ends the current call, waits for the player and continues
// under the next call's budgets. It returns the answer Resume or Answer
// accepted.
func (r *Runtime) pause(L *lua.LState, d *Dialog) *answer {
	th := r.cur.thread
	if th.canceled {
		L.RaiseError(errLeft)
	}
	if th.moved {
		L.RaiseError("the player is leaving this room (room.go was called): a hook cannot wait for an answer after it")
	}
	th.events <- result{dialog: d}
	a := <-th.resume // the caller has called begin
	L.SetContext(r.cur.ctx)
	if a == nil {
		th.canceled = true
		L.RaiseError(errLeft)
	}
	return a
}

// checkState measures what env keeps once its calls allocated enough
// since the last measurement for it to have grown past PlayerMemory, and
// what the world keeps once the sum of what every Instance may keep is past
// StateMemory.
func (r *Runtime) checkState(env *lua.LTable) error {
	st := r.envs[env]
	if st == nil {
		return nil
	}
	if st.sinceWalk >= r.limits.PlayerMemory/8 {
		var paused []*Thread
		for th := range r.paused {
			if th.env == env {
				paused = append(paused, th)
			}
		}
		r.measure(env, st, paused)
		if st.kept > r.limits.PlayerMemory {
			st.broken = true
			return ErrStateMemory
		}
	}
	if r.full() {
		return ErrFull
	}
	return nil
}

// full reports whether the world's Instances and Reserve keep more than
// StateMemory. It
// measures the Instances that ran since their last measurement only when
// the cheap upper bound is past the limit.
func (r *Runtime) full() bool {
	if r.total+r.extra <= r.limits.StateMemory {
		return false
	}
	paused := map[*lua.LTable][]*Thread{}
	for th := range r.paused {
		paused[th.env] = append(paused[th.env], th)
	}
	for env, st := range r.envs {
		if st.sinceWalk > 0 {
			r.measure(env, st, paused[env])
		}
	}
	return r.total+r.extra > r.limits.StateMemory
}

// Reserve counts delta more bytes (or fewer, when negative) toward the
// world's StateMemory, for what is kept outside any Instance, such as
// state.player. Growing past the limit, with what the Instances keep, is
// refused with ErrFull and changes nothing. The caller serializes it with
// the Runtime's other calls.
//
// Called from a hook, it compares the cheap upper bound only: measuring
// the world there would run on the caller's time and memory budgets, and
// would miss what the running hook holds in its locals. The check after
// the call measures and corrects the bound.
func (r *Runtime) Reserve(delta int64) error {
	r.extra += delta
	if delta <= 0 {
		return nil
	}
	if r.cur != nil && r.total+r.extra <= r.limits.StateMemory || r.cur == nil && !r.full() {
		return nil
	}
	r.extra -= delta
	return ErrFull
}

// measure walks what env keeps: its globals and the stacks of its paused
// hooks, without the shared libraries.
func (r *Runtime) measure(env *lua.LTable, st *envState, paused []*Thread) {
	roots := []lua.LValue{env}
	if mods := r.loaded[env]; mods != nil {
		roots = append(roots, mods)
	}
	for _, th := range paused {
		roots = append(roots, stackValues(th.co)...)
	}
	kept := retained(roots, r.shared, r.limits.PlayerMemory)
	r.total += kept - st.kept - st.sinceWalk
	st.kept, st.sinceWalk = kept, 0
	if kept > r.limits.PlayerMemory {
		st.broken = true // the walk stopped counting: what it keeps is unknown
	}
}

// Kept is at least what env keeps: its last measurement plus what its calls
// allocated since. It is 0 for a released Env.
func (r *Runtime) Kept(e *Env) int64 {
	if st := r.envs[e.table]; st != nil {
		return st.kept + st.sinceWalk
	}
	return 0
}
