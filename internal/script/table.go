package script

import (
	"reflect"
	"unsafe"

	lua "github.com/yuin/gopher-lua"
)

// MaxArrayIndex is the largest integer key a table keeps in its array part;
// larger keys go to its hash part. gopher-lua fills the array up to a new
// key with empty slots, in one Go loop the time and memory limits cannot
// stop: with its own limit of 2^26, t[6e7] = true allocates about 1 GiB at
// once. At 2^18 such a write fills at most 4 MiB. A list keeps at most
// MaxArrayIndex-1 entries: table.insert refuses more, and t[#t+1] past it
// lands in the hash part, where # does not see it.
const MaxArrayIndex = 1 << 18

func init() { lua.MaxArrayIndex = MaxArrayIndex }

// The slices behind a table's array part and its hash keys. The key list
// keeps removed keys too (gopher-lua never shrinks it), so it is the only
// way to see what they hold.
var tableArray, tableKeys = tableField("array"), tableField("keys")

func tableField(name string) int {
	f, ok := reflect.TypeFor[lua.LTable]().FieldByName(name)
	if !ok || f.Type != reflect.TypeFor[[]lua.LValue]() {
		panic("gopher-lua's LTable has no []LValue " + name + ": check tableParts")
	}
	return f.Index[0]
}

// tableParts returns t's array part, empty slots included, and its hash
// keys, removed ones included. They are gopher-lua's own slices: read
// them, never change them.
func tableParts(t *lua.LTable) (array, keys []lua.LValue) {
	v := reflect.ValueOf(t).Elem()
	field := func(i int) []lua.LValue {
		f := v.Field(i)
		return *(*[]lua.LValue)(unsafe.Pointer(f.UnsafeAddr()))
	}
	return field(tableArray), field(tableKeys)
}

// tableSlots is how many slots Next may look at to walk t.
func tableSlots(t *lua.LTable) int {
	array, keys := tableParts(t)
	return len(array) + len(keys)
}

// tableInsert refuses to grow a list's array part past MaxArrayIndex:
// gopher-lua's table.insert appends to it whatever its length, and reads
// of keys past MaxArrayIndex look in the hash part only.
func tableInsert(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if array, _ := tableParts(L.CheckTable(1)); len(array) >= MaxArrayIndex-1 {
			L.RaiseError("table.insert: a list holds at most %d entries", MaxArrayIndex-1)
		}
		return orig.GFunction(L)
	}
}
