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

// retained estimates the bytes reachable from roots, and stops counting
// once it passes limit. Sizes are rough: what matters is that a table or
// string growing without bound is caught. It walks with its own stack, so
// deeply nested tables cannot overflow the Go stack.
func retained(roots []lua.LValue, limit int64) int64 {
	seen := map[lua.LValue]struct{}{}
	var stack []lua.LValue
	var total int64
	push := func(v lua.LValue) {
		switch v := v.(type) {
		case lua.LString:
			total += 16 + int64(len(v))
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
		total += 64
		switch v := v.(type) {
		case *lua.LTable:
			v.ForEach(func(k, val lua.LValue) {
				total += 32
				push(k)
				push(val)
			})
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
