package script

import (
	"encoding/binary"
	"slices"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// state.game and world.finish (T-0018): what a world keeps as a whole,
// and ending it. state.game holds the world's settings (pack.yaml
// settings, chosen in the lobby and changed with /set) and what game
// masters' hooks keep there; every script reads it, and only a hook of a
// game master in GM mode writes it. It belongs to one world and is never
// shared with another.

// Game is what a Host reaches of its world as a whole. A Host without it
// has no state.game, and world.finish raises an error.
type Game interface {
	// GameState is the world's state.game; nil when there is none.
	GameState() *State
	// GameMaster reports whether the player is a game master in GM mode
	// now: only then may the hook write state.game.
	GameMaster() bool
	// Finish ends the world for everyone once the hook ended, with text
	// as the last story line its players read. An error says why it
	// cannot, such as the server's main world; world.finish returns it.
	Finish(text string) error
}

// errNotGM refuses a state.game write from a hook of anyone but a game
// master in GM mode.
var errNotGM = limited("only a game master in GM mode changes state.game")

// openGame adds state.game to the state library and world.finish to the
// world library, before they are made read-only.
func (r *Runtime) openGame(state, world *lua.LTable) {
	L := r.L
	game := L.NewTable()
	game.RawSetString("get", L.NewFunction(r.stateGet(r.gameState, "state.game")))
	game.RawSetString("set", L.NewFunction(r.gameSet))
	state.RawSetString("game", r.readOnly(game))
	world.RawSetString("finish", L.NewFunction(r.worldFinish))
}

// gameHost returns the Game of the running hook, or raises an error.
func (r *Runtime) gameHost(L *lua.LState, what string) Game {
	if r.cur == nil || r.cur.host == nil || r.cur.thread == nil {
		L.RaiseError("no player here: call %s from a hook such as on_enter", what)
	}
	g, ok := r.cur.host.(Game)
	if !ok {
		L.RaiseError("this world has no %s", what)
	}
	return g
}

// gameState returns state.game, or raises an error outside a hook.
func (r *Runtime) gameState(L *lua.LState) *State {
	s := r.gameHost(L, "state.game").GameState()
	if s == nil {
		L.RaiseError("this world keeps no game state")
	}
	return s
}

// gameSet is state.game.set(key, value): set, for a game master in GM
// mode only. A setting's key takes one of its choices.
func (r *Runtime) gameSet(L *lua.LState) int {
	g := r.gameHost(L, "state.game")
	s := r.gameState(L)
	key := stateKey(L, s)
	v := L.Get(2)
	if !g.GameMaster() {
		return r.setResult(L, "state.game.set", errNotGM)
	}
	if choices, ok := s.Choices[key]; ok {
		if err := checkChoice(key, v, choices); err != nil {
			return r.setResult(L, "state.game.set", err)
		}
	}
	return r.setResult(L, "state.game.set", r.set(s, key, v))
}

// checkChoice refuses v for the setting key unless it is one of choices.
func checkChoice(key string, v lua.LValue, choices []string) error {
	str, ok := v.(lua.LString)
	switch {
	case v == lua.LNil:
		return limited("%s is a setting: it cannot be removed", key)
	case !ok || !slices.Contains(choices, string(str)):
		return limited("%s is a setting: one of %s", key, strings.Join(choices, ", "))
	}
	return nil
}

// worldFinish is world.finish(text): it ends the world for everyone once
// the hook ends, like ending it in the lobby, and returns true; or nil
// and the reason when this world cannot end from a script.
func (r *Runtime) worldFinish(L *lua.LState) int {
	g := r.gameHost(L, "world.finish")
	text := L.CheckString(1)
	if len(text) > MaxTextBytes {
		L.ArgError(1, "the text is longer than the limit of say")
	}
	if err := g.Finish(text); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("world.finish: " + err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

// EncodeString is the encoding of the Lua string v as a State keeps it:
// the engine writes settings with it.
func EncodeString(v string) []byte {
	b := binary.AppendUvarint([]byte{tagString}, uint64(len(v)))
	return append(b, v...)
}

// String returns the string kept for key; ok is false when key holds
// nothing or no string.
func (s *State) String(key string) (v string, ok bool) {
	b := s.vals[key]
	if len(b) == 0 || b[0] != tagString {
		return "", false
	}
	n, k := binary.Uvarint(b[1:])
	if k <= 0 || n != uint64(len(b)-1-k) {
		return "", false
	}
	return string(b[1+k:]), true
}

// SetString keeps the string v for key, as a script's set would, within
// the limits of s; a key of Choices takes only one of its strings. The
// engine sets the world's settings with it. The caller holds the lock the
// world's Lua runs under.
func (s *State) SetString(key, v string) error {
	if n := s.Limits.OrDefault().KeyBytes; key == "" || len(key) > n {
		return limited("keys are 1 to %d bytes", n)
	}
	if choices, ok := s.Choices[key]; ok {
		if err := checkChoice(key, lua.LString(v), choices); err != nil {
			return err
		}
	}
	enc := EncodeString(v)
	if len(enc) > s.Limits.OrDefault().ValueBytes {
		return limited("value larger than %d bytes", s.Limits.OrDefault().ValueBytes)
	}
	return s.put(key, enc)
}
