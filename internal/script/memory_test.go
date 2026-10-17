package script

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func TestRetained(t *testing.T) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()

	str := L.NewTable()
	str.RawSetString("s", lua.LString(make([]byte, 1000)))
	cycle := L.NewTable()
	cycle.RawSetString("self", cycle)
	shared := L.NewTable()
	shared.RawSetString("big", lua.LString(make([]byte, 1<<20)))
	twice := L.NewTable()
	twice.RawSetInt(1, shared)
	twice.RawSetInt(2, shared)
	withMeta := L.NewTable()
	withMeta.Metatable = shared
	deep := L.NewTable()
	for range 100000 {
		next := L.NewTable()
		next.RawSetInt(1, deep)
		deep = next
	}

	tests := []struct {
		name     string
		root     lua.LValue
		min, max int64
	}{
		{"nil", lua.LNil, 0, 0},
		{"number", lua.LNumber(1), 0, 0},
		{"string", lua.LString("abc"), 3, 64},
		{"table with a string", str, 1000, 1400},
		{"cycle", cycle, 64, 400},
		{"shared table counted once", twice, 1 << 20, 1<<20 + 800},
		{"metatable", withMeta, 1 << 20, 1<<20 + 600},
		{"deep nesting", deep, 100000 * 64, 100000 * 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := retained([]lua.LValue{tt.root}, nil, 1<<40)
			if got < tt.min || got > tt.max {
				t.Errorf("retained = %d, want %d to %d", got, tt.min, tt.max)
			}
		})
	}

	// Functions count their environment and upvalues.
	fn, err := L.LoadString(`local up = string.rep("x", 1) return function() return up end`)
	if err != nil {
		t.Fatal(err)
	}
	fn.Env = shared
	if got := retained([]lua.LValue{fn}, nil, 1<<40); got < 1<<20 {
		t.Errorf("function retained = %d, want its env counted", got)
	}
	// The walk stops soon after passing the limit.
	if got := retained([]lua.LValue{deep}, nil, 1000); got > 1000+400 {
		t.Errorf("retained with limit 1000 = %d", got)
	}
}

func TestWatchAllocs(t *testing.T) {
	t.Run("under the limit", func(t *testing.T) {
		var fired atomic.Bool
		stop := watchAllocs(context.Background(), 1<<50, func() { fired.Store(true) })
		buf := make([]byte, 1<<20)
		runtime.KeepAlive(buf)
		if n := stop(); n < 1<<20 {
			t.Errorf("stop = %d, want at least 1 MiB", n)
		}
		if fired.Load() {
			t.Error("exceeded called under the limit")
		}
	})
	t.Run("over the limit", func(t *testing.T) {
		fired := make(chan struct{})
		stop := watchAllocs(context.Background(), 1<<20, func() { close(fired) })
		defer stop()
		deadline := time.After(10 * time.Second)
		var sink [][]byte
		for {
			select {
			case <-fired:
				return
			case <-deadline:
				t.Fatal("exceeded not called within 10 s")
			default:
				sink = append(sink, make([]byte, 64<<10))
				if len(sink) > 64 {
					sink = sink[:0]
				}
			}
		}
	})
	t.Run("context ends the watcher", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var fired atomic.Bool
		stop := watchAllocs(ctx, 1, func() { fired.Store(true) })
		cancel()
		stop() // must return even though the watcher may have ended first
		stop = watchAllocs(ctx, 1<<50, func() { fired.Store(true) })
		stop()
	})
}

// A world under its state limit keeps working call after call.
func TestStateMemoryUnderLimit(t *testing.T) {
	r := New(Limits{Time: time.Second, CallMemory: 64 << 20, StateMemory: 8 << 20})
	defer r.Close()
	// Each call allocates 1 MiB but keeps only the last string.
	env := load(t, r, `last = "" function on_enter() last = string.rep("x", 1e6) .. "y" end`)
	for i := range 50 {
		if _, err := r.Run(&recorder{}, env, "on_enter", "p"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

// What a paused hook keeps in local variables is kept by the world too.
//
// BUG: checkState walks only the globals, the registry and the script envs
// (script.go:220); a hook waiting in ui.dialog keeps its locals on its own
// coroutine stack, which is never walked. Every open dialog can keep up to
// the call memory budget, so 40 players in this test keep about 40 MiB in a
// world limited to 8 MiB.
func TestPausedHooksCountTowardStateMemory(t *testing.T) {
	r := New(Limits{Time: time.Second, CallMemory: 64 << 20, StateMemory: 8 << 20})
	defer r.Close()
	env := load(t, r, `function on_enter() local s = string.rep("x", 1e6) .. "y" ui.dialog("?", {"a"}) say(#s) end`)
	var ths []*Thread
	var err error
	for i := 0; i < 40 && err == nil; i++ {
		var th *Thread
		th, err = r.Run(&recorder{}, env, "on_enter", "p")
		ths = append(ths, th)
	}
	runtime.KeepAlive(ths)
	if !errors.Is(err, ErrStateMemory) {
		t.Errorf("40 paused hooks keeping 1 MiB each: err = %v, want ErrStateMemory", err)
	}
}

// Each Instance has its own memory limit: one going over it breaks only
// that Instance, and the world's limit counts all of them together without
// latching (T-0004 security re-review).
func TestPlayerAndWorldMemory(t *testing.T) {
	r := New(Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 3 << 20, StateMemory: 8 << 20})
	defer r.Close()
	sc, err := r.Compile("test.lua", `keep = {} function grow() keep[#keep + 1] = string.rep("x", 1e6) .. #keep end`)
	if err != nil {
		t.Fatal(err)
	}
	inst := func() *Env {
		t.Helper()
		env, err := r.Instance(sc)
		if err != nil {
			t.Fatalf("Instance: %v", err)
		}
		return env
	}

	// One instance over its own limit.
	a, b := inst(), inst()
	for i := 0; i < 10 && err == nil; i++ {
		_, err = r.Run(&recorder{}, a, "grow", "p")
	}
	if !errors.Is(err, ErrStateMemory) {
		t.Fatalf("a growing past 3 MiB: err = %v, want ErrStateMemory", err)
	}
	if _, err := r.Run(&recorder{}, a, "grow", "p"); !errors.Is(err, ErrStateMemory) {
		t.Errorf("a's next call: err = %v, want ErrStateMemory", err)
	}
	if _, err := r.Run(&recorder{}, b, "grow", "p"); err != nil {
		t.Errorf("b after a broke: %v", err)
	}
	r.Release(a)
	if _, err := r.Run(&recorder{}, a, "grow", "p"); !errors.Is(err, ErrScript) {
		t.Errorf("released a: err = %v, want ErrScript", err)
	}

	// Many instances, each under its limit, together over the world's.
	envs := []*Env{b}
	err = nil
	for i := 0; i < 20 && err == nil; i++ {
		env := inst()
		envs = append(envs, env)
		_, err = r.Run(&recorder{}, env, "grow", "p")
	}
	if !errors.Is(err, ErrFull) {
		t.Fatalf("20 instances keeping 1 MiB each in 8 MiB: err = %v, want ErrFull", err)
	}
	if _, err := r.Instance(sc); !errors.Is(err, ErrFull) {
		t.Errorf("Instance while full: err = %v, want ErrFull", err)
	}
	for _, env := range envs[:len(envs)/2] {
		r.Release(env)
	}
	if _, err := r.Instance(sc); err != nil {
		t.Errorf("Instance after half were released: %v", err)
	}
}
