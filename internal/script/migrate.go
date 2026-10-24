package script

import (
	"errors"
	"fmt"
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// Ruleset migrations (T-0010): a ruleset pack's migrations/<major>.lua
// defines migrate(sheet), which turns a profile's sheet of the major
// version before into one of its own. It runs in a sandbox of its own,
// with no player: say, ui, state and timers raise an error in it.

// migrateWrapper calls the script's migrate with the sheet, passed in
// __sheet, and keeps its
// result, or the changed sheet when it returns nothing.
const migrateWrapper = `local sheet = __sheet
__sheet = nil
if type(migrate) ~= "function" then error("the migration defines no function migrate(sheet)", 0) end
local out = migrate(sheet)
if out == nil then out = sheet end
__migrated = out`

// Migrate runs migrate(sheet) of the migration script src (named name in
// errors) on sheet, a profile's state.profile as its encodings, and
// returns the new sheet. The sheet passed in is a table of its keys; the
// result must be one too, within the limits of state.player.
func Migrate(limits Limits, name, src string, sheet map[string][]byte) (map[string][]byte, error) {
	r := New(limits)
	defer r.Close()
	env, err := r.Load(name, src)
	if err != nil {
		return nil, err
	}
	wrap, err := r.Compile(name+" (migrate)", migrateWrapper)
	if err != nil {
		return nil, err
	}
	in := r.L.NewTable()
	for k, v := range sheet {
		d := decoder{L: r.L}
		val, rest, err := d.value(v, 0)
		if err != nil || len(rest) != 0 {
			return nil, fmt.Errorf("%w: key %s of the sheet", errDamaged, strconv.Quote(k))
		}
		in.RawSetString(k, val)
	}
	env.table.RawSetString("__sheet", in) // varargs are not available in the sandbox
	fn := r.L.NewFunctionFromProto(wrap.proto)
	fn.Env = env.table
	th, err := r.start(nil, env.table, fn, Hook{NoWait: true})
	if err != nil {
		return nil, err
	}
	if th != nil { // ui.dialog refuses to wait without a player
		r.Cancel(th)
		return nil, fmt.Errorf("%w: %s waits for an answer", ErrScript, name)
	}
	out, ok := env.table.RawGetString("__migrated").(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("%w: %s: migrate must return the sheet, a table", ErrScript, name)
	}
	return encodeSheet(name, out)
}

// encodeSheet encodes a migrated sheet: string keys of state.player's
// size, values it can keep, within its key count and size.
func encodeSheet(name string, t *lua.LTable) (map[string][]byte, error) {
	sheet := map[string][]byte{}
	var size int64
	var errs []error
	t.ForEach(func(k, v lua.LValue) {
		if len(errs) > 0 {
			return
		}
		key, ok := k.(lua.LString)
		if !ok || key == "" || len(key) > MaxStateKeyBytes {
			errs = append(errs, fmt.Errorf("%w: %s: sheet keys are strings of 1 to %d bytes", ErrScript, name, MaxStateKeyBytes))
			return
		}
		e := encoder{}
		if err := e.value(v, 0); err != nil {
			errs = append(errs, fmt.Errorf("%w: %s: key %s: %v", ErrScript, name, strconv.Quote(string(key)), err))
			return
		}
		sheet[string(key)] = e.buf
		size += int64(len(key) + len(e.buf))
		switch {
		case len(sheet) > MaxPlayerStateKeys:
			errs = append(errs, fmt.Errorf("%w: %s: the sheet has more than %d keys", ErrScript, name, MaxPlayerStateKeys))
		case size > MaxPlayerStateBytes:
			errs = append(errs, fmt.Errorf("%w: %s: the sheet is larger than %d bytes", ErrScript, name, MaxPlayerStateBytes))
		}
	})
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return sheet, nil
}
