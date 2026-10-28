package script

import (
	"math"
	"math/rand/v2"

	lua "github.com/yuin/gopher-lua"
)

// The sandbox's math (T-0033): an allowlist like string and table.
// math.randomseed is left out, because gopher-lua's calls Go's global
// rand.Seed: on a server started with GODEBUG=randseednop=0 one pack
// could fix the numbers of every world (box codes, dice). math.random
// draws from math/rand/v2's source, which no Lua can seed.

// mathFuncs are the math functions scripts get as they are.
var mathFuncs = []string{
	"abs", "acos", "asin", "atan", "atan2", "ceil", "cos", "cosh", "deg", "exp", "floor", "fmod", "frexp",
	"huge", "ldexp", "log", "log10", "max", "min", "mod", "modf", "pi", "pow", "rad", "sin", "sinh", "sqrt", "tan", "tanh",
}

// maxRandom bounds math.random's arguments: every integer up to it is a
// float64 exactly, and m..n never overflows.
const maxRandom = 1 << 53

// mathRandom is Lua 5.1's math.random: random() is a number in [0, 1),
// random(n) an integer in [1, n], random(m, n) one in [m, n]. Arguments
// are floored; an empty interval, NaN or a value past ±2^53 is a script
// error.
func mathRandom(L *lua.LState) int {
	arg := func(n int) int64 {
		v := math.Floor(float64(L.CheckNumber(n)))
		if math.IsNaN(v) || v < -maxRandom || v > maxRandom {
			L.ArgError(n, "out of range: at most 2^53")
		}
		return int64(v)
	}
	var lo, hi int64
	switch L.GetTop() {
	case 0:
		L.Push(lua.LNumber(rand.Float64()))
		return 1
	case 1:
		lo, hi = 1, arg(1)
	case 2:
		lo, hi = arg(1), arg(2)
	default:
		L.RaiseError("wrong number of arguments to math.random")
	}
	if lo > hi {
		L.ArgError(L.GetTop(), "interval is empty")
	}
	L.Push(lua.LNumber(lo + rand.Int64N(hi-lo+1)))
	return 1
}

// openMath is the sandbox's math table, from the full one.
func openMath(L *lua.LState, full *lua.LTable) *lua.LTable {
	t := L.NewTable()
	for _, name := range mathFuncs {
		t.RawSetString(name, full.RawGetString(name))
	}
	t.RawSetString("random", L.NewFunction(mathRandom))
	return t
}
