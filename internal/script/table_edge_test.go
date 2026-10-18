package script

import (
	"errors"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// T-0024 edges: the array cap at its boundary, every way a script can set
// an integer key, and what tableParts sees.

// table.insert fills a list up to MaxArrayIndex-1 entries exactly, and the
// next one is refused, by both forms.
func TestTableInsertAtTheBoundary(t *testing.T) {
	n := tostr(MaxArrayIndex - 2)
	for _, call := range []string{"table.insert(t, true)", "table.insert(t, 1, true)", "table.insert(t, #t, true)"} {
		out, _, err := runHook(t, roomy, `function on_enter()
  local t = {} t[`+n+`] = true
  `+call+` say(#t)
  local ok, err = pcall(table.insert, t, true)
  say(tostring(ok) .. " " .. tostring(string.find(err, "at most", 1, true) ~= nil) .. " " .. #t)
end`)
		want := "say:" + tostr(MaxArrayIndex-1) + ";say:false true " + tostr(MaxArrayIndex-1)
		if err != nil || strings.Join(out, ";") != want {
			t.Errorf("%s: out = %q, err = %v, want %q", call, out, err, want)
		}
	}
}

// Every way to set a far integer key puts it in the hash part, quickly.
func TestHugeIndexEveryWay(t *testing.T) {
	far := "6e7"
	tests := []struct{ name, src string }{
		{"rawset", `local t = {} rawset(t, ` + far + `, true) say(tostring(rawget(t, ` + far + `)) .. " " .. #t)`},
		{"constructor", `local t = {[` + far + `] = true} say(tostring(t[` + far + `]) .. " " .. #t)`},
		{"insert at a far position", `local t = {} table.insert(t, ` + far + `, true) say(tostring(t[` + far + `]) .. " " .. #t)`},
		{"insert far into a list", `local t = {1, 2, 3} table.insert(t, ` + far + `, true) say(tostring(t[` + far + `]) .. " " .. #t)`},
		{"__newindex rawset", `local t = setmetatable({}, {__newindex = function(t, k, v) rawset(t, k, v) end}) t[` + far + `] = true say(tostring(t[` + far + `]) .. " " .. #t)`},
		{"max integer", `local t = {} t[2^53] = true say(tostring(t[2^53]) .. " " .. #t)`},
		{"unpack of a sparse list", `local t = {} t[` + far + `] = true say(tostring(select("#", unpack(t))) .. " " .. #t)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			out, _, err := runHook(t, DefaultLimits, `function on_enter() `+tt.src+` end`)
			if err != nil || len(out) != 1 || !strings.HasSuffix(out[0], " 0") && !strings.HasSuffix(out[0], " 3") {
				t.Errorf("out = %q, err = %v", out, err)
			}
			if d := time.Since(start); d > 200*time.Millisecond {
				t.Errorf("took %v", d)
			}
		})
	}
}

// Keys that are not positive integers never touch the array part.
func TestOddKeysStayInTheHash(t *testing.T) {
	for _, k := range []string{"0", "-1", "1.5", "-2^31", "2^31 + 0.5", "1/0", "-1/0"} {
		out, _, err := runHook(t, DefaultLimits, `function on_enter() local t = {} t[`+k+`] = true say(tostring(t[`+k+`]) .. " " .. #t) end`)
		if err != nil || len(out) != 1 || out[0] != "say:true 0" {
			t.Errorf("t[%s]: out = %q, err = %v", k, out, err)
		}
	}
}

func TestTableInsertBadArguments(t *testing.T) {
	for _, call := range []string{
		`table.insert()`, `table.insert(1, 2)`, `table.insert("s", 1)`, `table.insert(nil, 1)`,
		`table.insert({}, "x", 1)`,
	} {
		_, _, err := runHook(t, DefaultLimits, `function on_enter() `+call+` end`)
		if !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", call, err)
		}
	}
	// The wrapper keeps what table.insert returns: nothing.
	if got := say1(t, `select("#", table.insert({}, 1))`); got != "0" {
		t.Errorf("table.insert returns %s values", got)
	}
}

func TestTableParts(t *testing.T) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	empty := L.NewTable()
	if a, k := tableParts(empty); len(a) != 0 || len(k) != 0 {
		t.Errorf("empty table: %d array, %d keys", len(a), len(k))
	}
	tbl := L.NewTable()
	tbl.RawSetInt(1, lua.LString("a"))
	tbl.RawSetInt(3, lua.LString("c"))
	tbl.RawSetString("x", lua.LTrue)
	tbl.RawSetString("y", lua.LTrue)
	tbl.RawSetString("y", lua.LNil) // removed, but still in the key list
	tbl.RawSetH(lua.LNumber(1<<30), lua.LTrue)
	a, k := tableParts(tbl)
	if len(a) != 3 || a[0] != lua.LString("a") || a[1] != lua.LNil || a[2] != lua.LString("c") {
		t.Errorf("array = %v", a)
	}
	if len(k) != 3 {
		t.Errorf("keys = %v, want x, y (removed) and 2^30", k)
	}
	if n := tableSlots(tbl); n != 6 {
		t.Errorf("tableSlots = %d", n)
	}
	// What retained counts follows the capacity of both slices.
	big := L.CreateTable(10000, 0)
	if got := retained([]lua.LValue{big}, nil, 1<<40); got < 10000*sizeSlot {
		t.Errorf("a table made with room for 10000 entries: retained = %d", got)
	}
}

// Removed keys keep counting until the table goes. The walk checks its
// limit between tables: one table is walked whole, the next is not.
func TestRetainedRemovedKeysStopsAtLimit(t *testing.T) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	tbl := L.NewTable()
	for i := range 20000 {
		k := lua.LString(strings.Repeat("k", 50) + tostr(i))
		tbl.RawSetString(string(k), lua.LTrue)
		tbl.RawSetString(string(k), lua.LNil)
	}
	full := retained([]lua.LValue{tbl}, nil, 1<<40)
	if full < 20000*50 {
		t.Errorf("20000 removed keys of 50 bytes: retained = %d", full)
	}
	if got := retained([]lua.LValue{tbl, tbl}, nil, 1<<40); got != full {
		t.Errorf("the same table twice: retained = %d, want %d", got, full)
	}
	other := L.NewTable()
	for i := range 20000 {
		other.RawSetString(strings.Repeat("o", 50)+tostr(i), lua.LNil)
		other.RawSetString(strings.Repeat("o", 50)+tostr(i), lua.LTrue)
		other.RawSetString(strings.Repeat("o", 50)+tostr(i), lua.LNil)
	}
	both := retained([]lua.LValue{tbl, other}, nil, 1<<40)
	if got := retained([]lua.LValue{tbl, other}, nil, 1000); got >= both || got < full/2 {
		t.Errorf("two tables with limit 1000: retained = %d, want one table's worth (%d of %d)", got, full, both)
	}
}
