package script

import (
	"context"
	"runtime/metrics"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Go has no per-goroutine allocation counter, so a call's memory budget is
// checked against the process's total allocations while the call runs.
// Other goroutines' allocations count too; at a budget of tens of MiB per
// call of at most 100 ms that only matters on a server already under heavy
// load, and then the call fails cleanly.

const allocMetric = "/gc/heap/allocs:bytes"

// sampleEvery is how often a running call's allocations are checked.
const sampleEvery = time.Millisecond

func allocated() int64 {
	s := []metrics.Sample{{Name: allocMetric}}
	metrics.Read(s)
	return int64(s[0].Value.Uint64())
}

// watchAllocs calls exceeded once more than limit bytes were allocated
// after it started, until ctx ends or stop is called. stop returns the bytes
// allocated meanwhile.
func watchAllocs(ctx context.Context, limit int64, exceeded func()) (stop func() int64) {
	start := allocated()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(sampleEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if allocated()-start > limit {
					exceeded()
					return
				}
			}
		}
	}()
	return func() int64 {
		close(done)
		<-finished
		return allocated() - start
	}
}

// Rough sizes for the state walk, on the high side: a hash entry in a
// gopher-lua table lives in a map, a key slice and an index map, and a
// slot of the array part or the key slice is one interface value.
const (
	sizeString = 16
	sizeObject = 128
	sizeEntry  = 96
	sizeSlot   = 16
)

// retained estimates the bytes reachable from roots, without what is only
// reachable through skip, and stops counting once it passes limit. What
// matters is that a table or string growing without bound is caught. It
// walks with its own stack, so deeply nested tables cannot overflow the Go
// stack.
func retained(roots []lua.LValue, skip map[lua.LValue]struct{}, limit int64) int64 {
	seen := make(map[lua.LValue]struct{}, len(skip))
	for v := range skip {
		seen[v] = struct{}{}
	}
	var stack []lua.LValue
	var total int64
	push := func(v lua.LValue) {
		switch v := v.(type) {
		case lua.LString:
			total += sizeString + int64(len(v))
		case *lua.LTable, *lua.LFunction, *lua.LUserData:
			if _, ok := seen[v]; !ok {
				seen[v] = struct{}{}
				stack = append(stack, v)
			}
		}
	}
	for _, v := range roots {
		push(v)
	}
	for len(stack) > 0 && total <= limit {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		total += sizeObject
		switch v := v.(type) {
		case *lua.LTable:
			// Not ForEach: it skips empty array slots and removed keys,
			// which gopher-lua keeps, with what the removed keys hold.
			array, keys := tableParts(v)
			total += sizeSlot * int64(cap(array)+cap(keys))
			for _, val := range array {
				if val != lua.LNil {
					total += sizeEntry
					push(val)
				}
			}
			for _, k := range keys {
				total += sizeEntry
				push(k)
				push(v.RawGetH(k))
			}
			push(v.Metatable)
		case *lua.LFunction:
			if v.Env != nil {
				push(v.Env)
			}
			for _, uv := range v.Upvalues {
				push(uv.Value())
			}
		case *lua.LUserData:
			push(v.Metatable)
		}
	}
	return total
}

// stackValues returns the locals and functions of every frame of a paused
// hook, so what a hook holds while it waits in ui.dialog counts too.
func stackValues(co *lua.LState) []lua.LValue {
	var out []lua.LValue
	for level := 0; level < 256; level++ {
		dbg, ok := co.GetStack(level)
		if !ok {
			return out
		}
		if fn, err := co.GetInfo("f", dbg, lua.LNil); err == nil {
			out = append(out, fn)
		}
		for n := 1; ; n++ {
			name, v := co.GetLocal(dbg, n)
			if name == "" {
				break
			}
			out = append(out, v)
		}
	}
	return out
}
