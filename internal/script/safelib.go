package script

import (
	"math"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// The string and table functions that are wrapped rather than given as
// they are: arguments and results bounded, so no single Go call can run
// past the time or memory limits.

// intArg reads argument n as an integer within ±2^31, or def when absent.
// Larger values, NaN and infinities are errors, so later arithmetic on the
// result cannot overflow.
func intArg(L *lua.LState, n, def int) int {
	v := L.Get(n)
	if v == lua.LNil {
		return def
	}
	f := float64(L.CheckNumber(n))
	if math.IsNaN(f) || f < math.MinInt32 || f > math.MaxInt32 {
		L.ArgError(n, "number out of range")
	}
	return int(f)
}

// MaxResults is how many values unpack and string.byte may return. Past
// the Lua stack limit gopher-lua fails only after seconds of work.
const MaxResults = 8000

// limitResults wraps f(x, i, j), which returns one value per index from i
// to j, so that it refuses ranges longer than MaxResults. first is the
// default i; j defaults to the length of a table, or to i for a string.
func limitResults(f *lua.LFunction, name string, first int) lua.LGFunction {
	return func(L *lua.LState) int {
		i := intArg(L, 2, first)
		var j int
		switch v := L.Get(1).(type) {
		case *lua.LTable:
			j = intArg(L, 3, v.Len())
		case lua.LString:
			j = intArg(L, 3, i)
			if i < 0 {
				i += len(v) + 1
			}
			if j < 0 {
				j += len(v) + 1
			}
		}
		if j >= i && j-i >= MaxResults {
			L.RaiseError("too many results from %s (more than %d)", name, MaxResults)
		}
		return f.GFunction(L)
	}
}

// strFind allows plain search only: string.find(s, text, init, true).
func strFind(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if !lua.LVAsBool(L.Get(4)) {
			L.RaiseError("string.find needs plain = true: Lua patterns are off in the sandbox (docs/lua-sandbox.md)")
		}
		intArg(L, 3, 1)
		return orig.GFunction(L)
	}
}

// strSub copies the substring: a Go substring would keep its whole parent
// string alive while the state measures only the slice.
func strSub(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		n := orig.GFunction(L)
		if s, ok := L.Get(-1).(lua.LString); ok {
			L.Pop(1)
			L.Push(lua.LString(strings.Clone(string(s))))
		}
		return n
	}
}

func strRep(L *lua.LState) int {
	s := L.CheckString(1)
	n := float64(L.CheckNumber(2))
	if !(n >= 1) || s == "" { // also NaN
		L.Push(lua.LString(""))
		return 1
	}
	if n > float64(MaxStringBytes/len(s)) {
		L.RaiseError("string.rep result larger than %d bytes", MaxStringBytes)
	}
	L.Push(lua.LString(strings.Repeat(s, int(n))))
	return 1
}

// concat implements .. for scripts: strings and numbers only, and never a
// result over MaxStringBytes. The check comes before the copy, so one
// a .. b .. c cannot allocate gigabytes.
func concat(L *lua.LState) int {
	n := L.GetTop()
	total := 0
	for i := 1; i <= n; i++ {
		switch v := L.Get(i).(type) {
		case lua.LString:
			total += len(v)
		case lua.LNumber:
			total += len(v.String())
		default:
			L.RaiseError("attempt to concatenate a %s value", v.Type().String())
		}
		if total > MaxStringBytes {
			L.RaiseError("string longer than %d bytes", MaxStringBytes)
		}
	}
	var b strings.Builder
	b.Grow(total)
	for i := 1; i <= n; i++ {
		b.WriteString(L.Get(i).String())
	}
	L.Push(lua.LString(b.String()))
	return 1
}

// tableConcat checks the result size before building it: a table can hold
// the same large string many times.
func tableConcat(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		t := L.CheckTable(1)
		sep := len(L.OptString(2, ""))
		i, j := intArg(L, 3, 1), intArg(L, 4, t.Len())
		total := 0
		for k := max(i, 1); k <= min(j, t.Len()); k++ {
			switch v := t.RawGetInt(k).(type) {
			case lua.LString:
				total += len(v)
			case lua.LNumber:
				total += len(v.String())
			}
			total += sep
			if total > MaxStringBytes {
				L.RaiseError("table.concat result larger than %d bytes", MaxStringBytes)
			}
		}
		return orig.GFunction(L)
	}
}

func tableSort(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if L.CheckTable(1).Len() > MaxSortLength {
			L.RaiseError("table.sort: more than %d entries", MaxSortLength)
		}
		return orig.GFunction(L)
	}
}
