package script

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// Limits of state.player, part of the pack format so a pack behaves the
// same on every server (T-0018 makes them settings).
const (
	MaxStateKeyBytes    = 64       // one key
	MaxStateValueBytes  = 16 << 10 // one value, encoded
	MaxStateDepth       = 8        // tables in tables
	MaxPlayerStateBytes = 256 << 10
	MaxPlayerStateKeys  = 500
	MaxStateWrites      = 50 // state.player.set calls in one hook

	// maxStateScan is how many table slots one set may look at, empty
	// ones included: gopher-lua skips empty array slots and removed keys
	// one by one, in Go, where the call's time limit cannot stop it.
	maxStateScan = 64 << 10
)

// State is one player's values, written with state.player.set and read
// with state.player.get, shared by every room the player visits. Only
// what a script sets is kept, encoded: changing a table after set or get
// changes nothing until the next set. It lasts for the session; T-0018
// stores the same encoded values in the database.
type State struct {
	// Reserve, if set, is asked before the state grows by delta bytes (or
	// told it shrank); an error refuses the write, for limits shared with
	// other players.
	Reserve func(delta int64) error

	vals map[string][]byte
	size int64 // the sum of len(key)+len(value)
}

// Size is what s keeps, as counted for MaxPlayerStateBytes.
func (s *State) Size() int64 { return s.size }

// The encoding of a value: one tag byte, then its payload. A table is its
// key and value pairs, ended by tagEnd. Integers up to 2^53 are zigzag
// varints, so small numbers take 2 bytes.
const (
	tagEnd byte = iota
	tagFalse
	tagTrue
	tagInt
	tagFloat
	tagString
	tagTable
)

// limitError is a write the limits refuse: set returns nil and the
// message. Any other error is a script error.
type limitError string

func (e limitError) Error() string { return string(e) }

func limited(format string, args ...any) error { return limitError(fmt.Sprintf(format, args...)) }

var errTooLarge = limited("value larger than %d bytes", MaxStateValueBytes)

// encoder turns a Lua value into its encoding of at most
// MaxStateValueBytes. Only booleans, finite numbers, strings and tables of
// them (string or number keys) can be kept.
type encoder struct {
	buf  []byte
	scan int // table slots looked at
}

func (e *encoder) value(v lua.LValue, depth int) error {
	switch v := v.(type) {
	case lua.LBool:
		if v {
			e.buf = append(e.buf, tagTrue)
		} else {
			e.buf = append(e.buf, tagFalse)
		}
	case lua.LNumber:
		f := float64(v)
		switch {
		case math.IsNaN(f) || math.IsInf(f, 0):
			return errors.New("numbers must be finite")
		case f == math.Trunc(f) && math.Abs(f) <= 1<<53 && !(f == 0 && math.Signbit(f)):
			e.buf = append(e.buf, tagInt)
			e.buf = binary.AppendVarint(e.buf, int64(f))
		default:
			e.buf = append(e.buf, tagFloat)
			e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(f))
		}
	case lua.LString:
		if len(v) > MaxStateValueBytes {
			return errTooLarge
		}
		e.buf = append(e.buf, tagString)
		e.buf = binary.AppendUvarint(e.buf, uint64(len(v)))
		e.buf = append(e.buf, v...)
	case *lua.LTable:
		if depth >= MaxStateDepth {
			return limited("tables nested deeper than %d", MaxStateDepth)
		}
		if e.scan += tableSlots(v); e.scan > maxStateScan {
			return limited("tables with more than %d slots in all, empty ones included", maxStateScan)
		}
		e.buf = append(e.buf, tagTable)
		for k, val := v.Next(lua.LNil); k != lua.LNil; k, val = v.Next(k) {
			switch k.(type) {
			case lua.LString, lua.LNumber:
			default:
				return errors.New("table keys must be strings or numbers")
			}
			if err := e.value(k, depth+1); err != nil {
				return err
			}
			if err := e.value(val, depth+1); err != nil {
				return err
			}
			if len(e.buf) > MaxStateValueBytes {
				return errTooLarge
			}
		}
		e.buf = append(e.buf, tagEnd)
	default:
		return fmt.Errorf("%s values cannot be kept, only nil, booleans, numbers, strings and tables of them", v.Type())
	}
	if len(e.buf) > MaxStateValueBytes {
		return errTooLarge
	}
	return nil
}

// The slices behind a table's array part and its hash keys, which keep
// removed keys: reflect can read their lengths, not their contents.
var tableArray, tableKeys = tableField("array"), tableField("keys")

func tableField(name string) int {
	f, ok := reflect.TypeFor[lua.LTable]().FieldByName(name)
	if !ok || f.Type.Kind() != reflect.Slice {
		panic("gopher-lua's LTable has no slice " + name + ": check tableSlots")
	}
	return f.Index[0]
}

// tableSlots is how many slots Next may look at to walk t.
func tableSlots(t *lua.LTable) int {
	v := reflect.ValueOf(t).Elem()
	return v.Field(tableArray).Len() + v.Field(tableKeys).Len()
}

var errDamaged = errors.New("a kept value is damaged")

// decoder makes fresh Lua values from encodings. scan counts the table
// slots it made, as the encoder counts those it looked at: gopher-lua fills
// an array up to an integer key with empty slots, in one loop no limit can
// stop, so a damaged encoding must not name a large one.
type decoder struct {
	L    *lua.LState
	scan int
}

// value decodes the value at the start of b and returns the rest.
func (d *decoder) value(b []byte, depth int) (lua.LValue, []byte, error) {
	if len(b) == 0 || depth > MaxStateDepth {
		return nil, nil, errDamaged
	}
	tag, b := b[0], b[1:]
	switch tag {
	case tagFalse:
		return lua.LFalse, b, nil
	case tagTrue:
		return lua.LTrue, b, nil
	case tagInt:
		n, k := binary.Varint(b)
		if k <= 0 {
			return nil, nil, errDamaged
		}
		return lua.LNumber(n), b[k:], nil
	case tagFloat:
		if len(b) < 8 {
			return nil, nil, errDamaged
		}
		return lua.LNumber(math.Float64frombits(binary.LittleEndian.Uint64(b))), b[8:], nil
	case tagString:
		n, k := binary.Uvarint(b)
		if k <= 0 || n > uint64(len(b)-k) {
			return nil, nil, errDamaged
		}
		b = b[k:]
		return lua.LString(b[:n]), b[n:], nil
	case tagTable:
		t := d.L.NewTable()
		for {
			if len(b) == 0 {
				return nil, nil, errDamaged
			}
			if b[0] == tagEnd {
				return t, b[1:], nil
			}
			var k, v lua.LValue
			var err error
			if k, b, err = d.value(b, depth+1); err != nil {
				return nil, nil, err
			}
			if v, b, err = d.value(b, depth+1); err != nil {
				return nil, nil, err
			}
			if n, ok := k.(lua.LNumber); ok && n > maxStateScan && float64(n) < float64(lua.MaxArrayIndex) && n == lua.LNumber(math.Trunc(float64(n))) {
				return nil, nil, errDamaged // the encoder never keeps such an array
			}
			before := tableSlots(t)
			t.RawSet(k, v)
			if d.scan += tableSlots(t) - before; d.scan > maxStateScan {
				return nil, nil, errDamaged
			}
		}
	}
	return nil, nil, errDamaged
}

func stateKey(L *lua.LState) string {
	key := L.CheckString(1)
	if key == "" || len(key) > MaxStateKeyBytes {
		L.ArgError(1, "keys are 1 to "+strconv.Itoa(MaxStateKeyBytes)+" bytes")
	}
	return key
}

// playerState returns the state of the call's player, or raises an error
// when called outside a hook.
func (r *Runtime) playerState(L *lua.LState) *State {
	if r.cur == nil || r.cur.host == nil || r.cur.thread == nil {
		L.RaiseError("no player here: call state.player from a hook such as on_enter")
	}
	s := r.cur.host.PlayerState()
	if s == nil {
		L.RaiseError("this world keeps no player state")
	}
	return s
}

// state.player.get(key) returns a copy of the value set for key, or nil.
func (r *Runtime) stateGet(L *lua.LState) int {
	s := r.playerState(L)
	b, ok := s.vals[stateKey(L)]
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	d := decoder{L: L}
	v, rest, err := d.value(b, 0)
	if err != nil || len(rest) != 0 {
		L.RaiseError("state.player.get: %s", errDamaged)
	}
	L.Push(v)
	return 1
}

// state.player.set(key, value) keeps a copy of value for key; nil removes
// it. It returns true, or nil and the reason when a limit refuses the
// write. A value that cannot be kept at all (a function) is an error.
func (r *Runtime) stateSet(L *lua.LState) int {
	s := r.playerState(L)
	key := stateKey(L)
	var limit limitError
	switch err := r.set(s, key, L.Get(2)); {
	case err == nil:
		L.Push(lua.LTrue)
		return 1
	case errors.As(err, &limit):
		L.Push(lua.LNil)
		L.Push(lua.LString("state.player.set: " + string(limit)))
		return 2
	default:
		L.ArgError(2, err.Error())
		return 0
	}
}

func (r *Runtime) set(s *State, key string, v lua.LValue) error {
	th := r.cur.thread
	if th.writes++; th.writes > MaxStateWrites {
		return limited("more than %d writes in one hook", MaxStateWrites)
	}
	old, had := s.vals[key]
	oldSize := int64(0)
	if had {
		oldSize = int64(len(key) + len(old))
	}
	if v == lua.LNil {
		if had {
			delete(s.vals, key)
			s.size -= oldSize
			if s.Reserve != nil {
				_ = s.Reserve(-oldSize)
			}
		}
		return nil
	}
	e := encoder{}
	if err := e.value(v, 0); err != nil {
		return err
	}
	size := int64(len(key) + len(e.buf))
	if !had && len(s.vals) >= MaxPlayerStateKeys {
		return limited("more than %d keys", MaxPlayerStateKeys)
	}
	if s.size-oldSize+size > MaxPlayerStateBytes {
		return limited("player state larger than %d bytes", MaxPlayerStateBytes)
	}
	if s.Reserve != nil {
		if err := s.Reserve(size - oldSize); err != nil {
			return limitError(err.Error())
		}
	}
	if s.vals == nil {
		s.vals = map[string][]byte{}
	}
	s.vals[key] = append([]byte(nil), e.buf...) // no spare capacity kept
	s.size += size - oldSize
	return nil
}
