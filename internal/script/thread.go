package script

import (
	"context"
	"errors"
	"fmt"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Running hooks: each on its own goroutine with its own Lua thread,
// paused in ui.dialog, ui.select and ui.ask until the player answers, a
// timeout expires or the hook is cancelled.

// Thread is a hook paused in ui.dialog or ui.select. Resume it with the
// player's choice, or Cancel it when the player leaves.
type Thread struct {
	Dialog Dialog

	co            *lua.LState
	env           *lua.LTable     // the globals of the Instance it runs in
	resume        chan *answer    // the player's answer; nil ends the hook
	events        chan result     // the end of each call: a dialog, or the hook's end
	canceled      bool            // set on the hook's goroutine once resume sent nil
	moved         bool            // room.go was called: the hook may not wait again
	noWait        bool            // started while the player had a question open: it may not wait
	updating      bool            // running the function of state.world.update: it may not wait
	readOnlyWorld bool            // on_change: state.world writes are refused
	noGM          bool            // Hook.NoGM: state.game writes are refused, whatever the Host says
	writes        int             // state.player.set calls, across its dialogs
	refusedTold   bool            // the Host heard of a refused write (Refusals)
	loading       map[string]bool // modules this hook is running now, for require's cycle check
}

type result struct {
	dialog *Dialog
	err    error
}

// Run calls hook(player, args...) from env. It returns a Thread when the
// hook waits in ui.dialog, and nil when it finished or the script has no
// such hook.
func (r *Runtime) Run(host Host, env *Env, hook, player string, args ...string) (*Thread, error) {
	return r.RunHook(host, env, Hook{Name: hook, Player: player, Args: args})
}

// Hook is one hook call for RunHook.
type Hook struct {
	Name   string
	Player string
	// PlayerID, when not "", is the player's id in the world, player.id.
	PlayerID string
	// GM is player.gm: the player is a game master of the world in GM
	// mode.
	GM bool
	// NoGM runs the hook without the player's GM rights (player.gm false,
	// no engine permissions, no state.game writes): for a room timer
	// another player started, which only falls to this one.
	NoGM bool
	Args []string
	// NoWait refuses ui.dialog, ui.select and ui.ask in the hook: the
	// engine runs it while the player has a question open already, for a
	// timer or another player's change.
	NoWait bool
	// Keys, when not nil, is passed as a list after the player, before
	// Args: the keys of on_change.
	Keys []string
	// ReadOnlyWorld refuses state.world writes in the hook and in the
	// rest of it after its questions, for on_change.
	ReadOnlyWorld bool
}

// RunHook is Run with the options of h.
func (r *Runtime) RunHook(host Host, env *Env, h Hook) (*Thread, error) {
	if r.closed {
		return nil, ErrClosed
	}
	hook, player, args := h.Name, h.Player, h.Args
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
	if h.PlayerID != "" {
		p.RawSetString("id", lua.LString(h.PlayerID))
	}
	p.RawSetString("gm", lua.LBool(h.GM))
	p.RawSetString("can", r.can(h.GM))
	if host != nil {
		p.RawSetString("room", lua.LString(host.Room()))
	}
	values := []lua.LValue{p}
	if h.Keys != nil {
		keys := r.L.CreateTable(len(h.Keys), 0)
		for i, k := range h.Keys {
			keys.RawSetInt(i+1, lua.LString(k))
		}
		values = append(values, keys)
	}
	for _, a := range args {
		values = append(values, lua.LString(a))
	}
	return r.start(host, env.table, fn, h, values...)
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

// Expire ends the wait of a hook paused on a question with a Timeout: the
// question returns nil, and the hook goes on as after an answer.
func (r *Runtime) Expire(host Host, th *Thread) (*Thread, error) {
	if _, ok := r.paused[th]; !ok || r.closed {
		return nil, fmt.Errorf("%w: hook is not waiting", ErrScript)
	}
	if th.Dialog.Timeout <= 0 {
		return nil, fmt.Errorf("%w: this question has no timeout", ErrAnswer)
	}
	return r.answer(host, th, &answer{expired: true})
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
func (r *Runtime) start(host Host, env *lua.LTable, fn *lua.LFunction, h Hook, args ...lua.LValue) (*Thread, error) {
	co, _ := r.L.NewThread()
	th := &Thread{co: co, env: env, resume: make(chan *answer), events: make(chan result), noWait: h.NoWait, readOnlyWorld: h.ReadOnlyWorld, noGM: h.NoGM}
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
	if th.noWait {
		L.RaiseError("the player has a question open already: this hook cannot wait for an answer")
	}
	if th.updating {
		L.RaiseError("a hook cannot wait for an answer inside state.world.update")
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
