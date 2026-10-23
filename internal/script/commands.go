package script

import (
	"slices"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// Slash commands a room script offers: a global table commands, read by
// the engine for /help and to check who may use them, and the hook
// on_command(player, name, args) that runs them.
//
//	commands = {
//	  skip = { help = "skip whoever is acting now", gm = true },
//	  roll = { help = "roll the dice" },
//	}

// Limits of a room's commands.
const (
	// MaxCommands is how many commands one room offers; more are left
	// out of /help and cannot be used.
	MaxCommands = 20
	// MaxCommandBytes is the longest command name.
	MaxCommandBytes = 20
	// MaxCommandHelpBytes is the longest help line of a command.
	MaxCommandHelpBytes = 100
)

// Command is a slash command a room script offers.
type Command struct {
	Name string // like an item id, at most MaxCommandBytes
	Help string // one line for /help; "" when the script gives none
	GM   bool   // only the world's game masters may use it
}

// ValidCommand reports whether name can name a command: 1 to
// MaxCommandBytes of a-z, 0-9, _ and -, starting with a letter or digit.
func ValidCommand(name string) bool {
	return len(name) <= MaxCommandBytes && ValidItemID(name)
}

// Commands lists the commands env's script offers in its global table
// commands, sorted by name: entries that are not a valid name with a
// table are left out, and so are those past MaxCommands. A help line
// that is not one line of at most MaxCommandHelpBytes is dropped. The
// table is read once, when the script's top level has run (Instance), so
// a hook changing it later changes nothing, and asking costs nothing.
func (e *Env) Commands() []Command {
	if e == nil {
		return nil
	}
	return e.commands
}

// readCommands reads the commands table of a script's globals. It runs no
// Lua, so it needs no time budget; the caller holds the world's lock.
func readCommands(globals *lua.LTable) []Command {
	t, ok := globals.RawGetString("commands").(*lua.LTable)
	if !ok {
		return nil
	}
	var list []Command
	seen := 0
	// Next instead of ForEach: it stops after 4*MaxCommands entries
	// (skipping empty slots on the way, bounded by what the top level
	// could build in its time and memory).
	for k, v := t.Next(lua.LNil); k != lua.LNil && seen < 4*MaxCommands && len(list) < MaxCommands; k, v = t.Next(k) {
		seen++
		name, ok := k.(lua.LString)
		if !ok || !ValidCommand(string(name)) {
			continue
		}
		spec, ok := v.(*lua.LTable)
		if !ok {
			continue
		}
		c := Command{Name: string(name), GM: lua.LVAsBool(spec.RawGetString("gm"))}
		if help, ok := spec.RawGetString("help").(lua.LString); ok && ValidLabel(string(help)) && len(help) <= MaxCommandHelpBytes {
			c.Help = strings.TrimSpace(string(help))
		}
		list = append(list, c)
	}
	slices.SortFunc(list, func(a, b Command) int { return strings.Compare(a.Name, b.Name) })
	return list
}
