package script

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// Limits of state.player, part of the pack format so a pack behaves the
// same on every server (T-0018 makes them settings).
const (
	MaxStateKeyBytes    = 64       // one key
	MaxStateValueBytes  = 16 << 10 // one value, measured by stateSize
	MaxStateDepth       = 8        // tables in tables
	MaxPlayerStateBytes = 256 << 10
	MaxPlayerStateKeys  = 500
	MaxStateWrites      = 50 // state.player.set calls in one call
)

// State is one player's values, written with state.player.set and read
// with state.player.get, shared by every room the player visits. Only
// what a script sets is kept, as a copy: changing a table after set or
// get changes nothing until the next set. It lasts for the session; T-0018
// stores it in the database with the same API and limits.
type State struct {
	// Reserve, if set, is asked before the state grows by delta bytes (or
	// told it shrank); an error refuses the write, for limits shared with
	// other players.
	Reserve func(delta int64) error

	vals map[string]kept
	size int64 // the sum of the kept sizes
}

// kept is a value and its size: its key's length plus stateCopy's count.
type kept struct {
	v    stateValue
	size int64
}

// Size is what s keeps, as counted for MaxPlayerStateBytes.
func (s *State) Size() int64 { return s.size }

// stateValue is nil, bool, float64, string or *stateTable.
type stateValue any

type stateTable struct {
	keys, vals []stateValue // keys are strings or float64
}

// limitError is a write the limits refuse: set returns nil and the
// message. Any other error is a script error.
type limitError string

func (e limitError) Error() string { return string(e) }

func limited(format string, args ...any) error { return limitError(fmt.Sprintf(format, args...)) }

// stateCopy turns a Lua value into a stateValue of at most budget bytes
// and returns it with its size. Only nil, booleans, finite numbers,
// strings and tables of them (string or number keys) can be kept.
func stateCopy(v lua.LValue, depth int, budget int64) (stateValue, int64, error) {
	switch v := v.(type) {
	case *lua.LNilType:
		return nil, 1, nil
	case lua.LBool:
		return bool(v), 1, nil
	case lua.LNumber:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, 0, errors.New("numbers must be finite")
		}
		return f, 8, nil
	case lua.LString:
		n := int64(len(v)) + 2
		if n > budget {
			return nil, 0, limited("value larger than %d bytes", MaxStateValueBytes)
		}
		return string(v), n, nil
	case *lua.LTable:
		if depth >= MaxStateDepth {
			return nil, 0, limited("tables nested deeper than %d", MaxStateDepth)
		}
		t := &stateTable{}
		size := int64(2)
		for k, val := v.Next(lua.LNil); k != lua.LNil; k, val = v.Next(k) {
			switch k.(type) {
			case lua.LString, lua.LNumber:
			default:
				return nil, 0, errors.New("table keys must be strings or numbers")
			}
			kc, kn, err := stateCopy(k, depth+1, budget-size)
			if err != nil {
				return nil, 0, err
			}
			size += kn
			vc, vn, err := stateCopy(val, depth+1, budget-size)
			if err != nil {
				return nil, 0, err
			}
			size += vn
			if size > budget {
				return nil, 0, limited("value larger than %d bytes", MaxStateValueBytes)
			}
			t.keys = append(t.keys, kc)
			t.vals = append(t.vals, vc)
		}
		return t, size, nil
	default:
		return nil, 0, fmt.Errorf("%s values cannot be kept, only nil, booleans, numbers, strings and tables of them", v.Type())
	}
}

// toLua makes a fresh Lua value from v.
func toLua(L *lua.LState, v stateValue) lua.LValue {
	switch v := v.(type) {
	case bool:
		return lua.LBool(v)
	case float64:
		return lua.LNumber(v)
	case string:
		return lua.LString(v)
	case *stateTable:
		t := L.CreateTable(0, len(v.keys))
		for i, k := range v.keys {
			t.RawSet(toLua(L, k), toLua(L, v.vals[i]))
		}
		return t
	default:
		return lua.LNil
	}
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
	if r.cur == nil || r.cur.host == nil {
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
	L.Push(toLua(L, s.vals[stateKey(L)].v))
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
	if r.cur.writes++; r.cur.writes > MaxStateWrites {
		return limited("more than %d writes in one call", MaxStateWrites)
	}
	val, n, err := stateCopy(v, 0, MaxStateValueBytes)
	if err != nil {
		return err
	}
	old := s.vals[key] // size 0 when there is none
	if val == nil {
		if old.size > 0 {
			delete(s.vals, key)
			s.size -= old.size
			if s.Reserve != nil {
				_ = s.Reserve(-old.size)
			}
		}
		return nil
	}
	size := n + int64(len(key))
	if old.size == 0 && len(s.vals) >= MaxPlayerStateKeys {
		return limited("more than %d keys", MaxPlayerStateKeys)
	}
	if s.size-old.size+size > MaxPlayerStateBytes {
		return limited("player state larger than %d bytes", MaxPlayerStateBytes)
	}
	if s.Reserve != nil {
		if err := s.Reserve(size - old.size); err != nil {
			return limitError(err.Error())
		}
	}
	if s.vals == nil {
		s.vals = map[string]kept{}
	}
	s.vals[key] = kept{v: val, size: size}
	s.size += size - old.size
	return nil
}
