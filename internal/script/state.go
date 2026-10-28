package script

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// State is one player's values, written with state.player.set and read
// with state.player.get, shared by every room the player visits; or, with
// World set, the values of state.world, shared by the world's players;
// or, with Game set, those of state.game (game.go). Only what a script
// sets is kept, encoded: changing a table after set or get changes nothing
// until the next set. It lasts for the session (the world's run for
// state.world and state.game) in memory; a world with a store saves the
// same encodings in the database after every hook (package game).
type State struct {
	// Reserve, if set, is asked before the state grows by delta bytes (or
	// told it shrank); an error refuses the write, for limits shared with
	// other players.
	Reserve func(delta int64) error
	// Changed, if set, is told the key of every write that changed s.
	Changed func(key string)
	// World gives s the limits and names of state.world, Game those of
	// state.game.
	World, Game bool
	// Limits bound s; zero fields are the defaults. The world's Runtime
	// counts the writes of a hook with its own Limits.State.
	Limits StateLimits
	// Choices, for state.game, are the pack's settings: a key named here
	// takes only one of its strings, and cannot be removed.
	Choices map[string][]string

	vals map[string][]byte
	size int64 // the sum of len(key)+len(value)
}

// Size is what s keeps, as counted for MaxPlayerStateBytes.
func (s *State) Size() int64 { return s.size }

// Encoded returns the encoding kept for key, for the database; callers
// must not change it.
func (s *State) Encoded(key string) ([]byte, bool) {
	v, ok := s.vals[key]
	return v, ok
}

// Load replaces what s keeps with vals, encodings from the database. It
// checks the limits a script's writes are held to, so a damaged or
// hand-edited database cannot load more than a script could set, and asks
// Reserve for the size. A value that is damaged inside fails when a script
// gets it.
func (s *State) Load(vals map[string][]byte) error {
	maxBytes, maxKeys, name := s.limits()
	lim := s.Limits.OrDefault()
	if len(vals) > maxKeys {
		return fmt.Errorf("stored %s state has more than %d keys", name, maxKeys)
	}
	var size int64
	for k, v := range vals {
		if k == "" || len(k) > lim.KeyBytes || len(v) == 0 || len(v) > lim.ValueBytes {
			return fmt.Errorf("stored %s state has a key or value out of bounds", name)
		}
		size += int64(len(k) + len(v))
	}
	if size > maxBytes {
		return fmt.Errorf("stored %s state is larger than %d bytes", name, maxBytes)
	}
	if s.Reserve != nil {
		if err := s.Reserve(size - s.size); err != nil {
			return err
		}
	}
	s.vals = make(map[string][]byte, len(vals))
	for k, v := range vals {
		s.vals[k] = append([]byte(nil), v...)
	}
	s.size = size
	return nil
}

// limits are the size limits of s and its name in messages.
func (s *State) limits() (bytes int64, keys int, name string) {
	l := s.Limits.OrDefault()
	switch {
	case s.Game:
		return l.GameBytes, l.GameKeys, "game"
	case s.World:
		return l.WorldBytes, l.WorldKeys, "world"
	}
	return l.PlayerBytes, l.PlayerKeys, "player"
}

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

// encoder turns a Lua value into its encoding of at most max bytes, with
// tables nested less than depth deep. Only booleans, finite numbers,
// strings and tables of them (string or number keys) can be kept.
type encoder struct {
	buf        []byte
	scan       int // table slots looked at
	max, depth int
}

func (e *encoder) tooLarge() error { return limited("value larger than %d bytes", e.max) }

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
		if len(v) > e.max {
			return e.tooLarge()
		}
		e.buf = append(e.buf, tagString)
		e.buf = binary.AppendUvarint(e.buf, uint64(len(v)))
		e.buf = append(e.buf, v...)
	case *lua.LTable:
		if depth >= e.depth {
			return limited("tables nested deeper than %d", e.depth)
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
			if len(e.buf) > e.max {
				return e.tooLarge()
			}
		}
		e.buf = append(e.buf, tagEnd)
	default:
		return fmt.Errorf("%s values cannot be kept, only nil, booleans, numbers, strings and tables of them", v.Type())
	}
	if len(e.buf) > e.max {
		return e.tooLarge()
	}
	return nil
}

var errDamaged = errors.New("a kept value is damaged")

// decoder makes fresh Lua values from encodings. scan counts the table
// slots it made, as the encoder counts those it looked at: gopher-lua fills
// an array up to an integer key with empty slots, in one loop no limit can
// stop, so a damaged encoding must not name a large one.
type decoder struct {
	L     *lua.LState
	scan  int
	depth int // the deepest nesting a value may have
}

// value decodes the value at the start of b and returns the rest.
func (d *decoder) value(b []byte, depth int) (lua.LValue, []byte, error) {
	if len(b) == 0 || depth > d.depth {
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

// stateKey reads the key at argument 1 of a call on s.
func stateKey(L *lua.LState, s *State) string {
	key := L.CheckString(1)
	if n := s.Limits.OrDefault().KeyBytes; key == "" || len(key) > n {
		L.ArgError(1, "keys are 1 to "+strconv.Itoa(n)+" bytes")
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

// worldState returns state.world, or raises an error when called outside
// a hook.
func (r *Runtime) worldState(L *lua.LState) *State {
	if r.cur == nil || r.cur.host == nil || r.cur.thread == nil {
		L.RaiseError("no player here: call state.world from a hook such as on_enter")
	}
	s := r.cur.host.WorldState()
	if s == nil {
		L.RaiseError("this world keeps no world state")
	}
	return s
}

// get(key) of state.player or state.world returns a copy of the value set
// for key, or nil.
func (r *Runtime) stateGet(which func(*lua.LState) *State, name string) lua.LGFunction {
	return func(L *lua.LState) int {
		s := which(L)
		L.Push(r.get(L, s, stateKey(L, s), name))
		return 1
	}
}

func (r *Runtime) get(L *lua.LState, s *State, key, name string) lua.LValue {
	b, ok := s.vals[key]
	if !ok {
		return lua.LNil
	}
	d := decoder{L: L, depth: s.Limits.OrDefault().Depth}
	v, rest, err := d.value(b, 0)
	if err != nil || len(rest) != 0 {
		L.RaiseError("%s.get: %s", name, errDamaged)
	}
	return v
}

// set(key, value) of state.player or state.world keeps a copy of value
// for key; nil removes it. It returns true, or nil and the reason when a
// limit refuses the write. A value that cannot be kept at all (a function)
// is an error.
func (r *Runtime) stateSet(which func(*lua.LState) *State, name string) lua.LGFunction {
	return func(L *lua.LState) int {
		s := which(L)
		key := stateKey(L, s)
		return r.setResult(L, name+".set", r.set(s, key, L.Get(2)))
	}
}

// setResult pushes what set and update return for err.
func (r *Runtime) setResult(L *lua.LState, name string, err error) int {
	var limit limitError
	switch {
	case err == nil:
		L.Push(lua.LTrue)
		return 1
	case errors.As(err, &limit):
		msg := name + ": " + string(limit)
		r.refused(msg)
		L.Push(lua.LNil)
		L.Push(lua.LString(msg))
		return 2
	default:
		L.ArgError(2, err.Error())
		return 0
	}
}

// errReadOnlyWorld refuses world writes in on_change, so a change cannot
// bounce between players for ever.
var errReadOnlyWorld = limited("on_change cannot change world state")

// state.world.update(key, fn) sets key to fn(value), where value is a
// copy of what key holds now. Nothing else runs in between, and fn cannot
// wait for an answer, so two players updating one key never both see the
// old value. It returns what set returns; an error in fn is the hook's
// error and changes nothing.
func (r *Runtime) stateUpdate(L *lua.LState) int {
	s := r.worldState(L)
	key := stateKey(L, s)
	fn := L.CheckFunction(2)
	th := r.cur.thread
	if th.updating {
		L.RaiseError("state.world.update cannot run inside another update")
	}
	if th.readOnlyWorld {
		return r.setResult(L, "state.world.update", errReadOnlyWorld)
	}
	old := r.get(L, s, key, "state.world")
	th.updating = true
	L.Push(fn)
	L.Push(old)
	err := L.PCall(1, 1, nil)
	th.updating = false
	if err != nil {
		if api, ok := err.(*lua.ApiError); ok {
			L.Error(api.Object, 0)
		}
		L.RaiseError("%v", err)
	}
	v := L.Get(-1)
	L.Pop(1)
	return r.setResult(L, "state.world.update", r.set(s, key, v))
}

func (r *Runtime) set(s *State, key string, v lua.LValue) error {
	th := r.cur.thread
	if s.World && th.readOnlyWorld {
		return errReadOnlyWorld
	}
	if th.writes++; th.writes > r.limits.State.Writes {
		return limited("more than %d writes in one hook", r.limits.State.Writes)
	}
	if v == lua.LNil {
		s.remove(key)
		return nil
	}
	lim := s.Limits.OrDefault()
	e := encoder{max: lim.ValueBytes, depth: lim.Depth}
	if err := e.value(v, 0); err != nil {
		return err
	}
	return s.put(key, e.buf)
}

// remove forgets key.
func (s *State) remove(key string) {
	old, had := s.vals[key]
	if !had {
		return
	}
	oldSize := int64(len(key) + len(old))
	delete(s.vals, key)
	s.size -= oldSize
	if s.Reserve != nil {
		_ = s.Reserve(-oldSize)
	}
	if s.Changed != nil {
		s.Changed(key)
	}
}

// put keeps enc, the encoding of a value, for key within the limits of s.
func (s *State) put(key string, enc []byte) error {
	maxBytes, maxKeys, name := s.limits()
	old, had := s.vals[key]
	oldSize := int64(0)
	if had {
		oldSize = int64(len(key) + len(old))
	}
	size := int64(len(key) + len(enc))
	if !had && len(s.vals) >= maxKeys {
		return limited("more than %d keys", maxKeys)
	}
	if s.size-oldSize+size > maxBytes {
		return limited("%s state larger than %d bytes", name, maxBytes)
	}
	if s.Reserve != nil {
		if err := s.Reserve(size - oldSize); err != nil {
			return limitError(err.Error())
		}
	}
	if s.vals == nil {
		s.vals = map[string][]byte{}
	}
	same := had && bytes.Equal(old, enc)
	s.vals[key] = append([]byte(nil), enc...) // no spare capacity kept
	s.size += size - oldSize
	if s.Changed != nil && !same { // writing the same value again tells no one
		s.Changed(key)
	}
	return nil
}
