package script

import (
	"errors"

	lua "github.com/yuin/gopher-lua"
)

// Profiles (T-0010): state.profile, the sheet of the player's character
// that goes with it from story to story on one ruleset; progress, what a
// story gives the stories after it; and pack, which pack and ruleset
// versions the world runs. See docs/rulesets.md.

// ProfileHost is a Host whose player may play a profile. A Host without
// it has none: state.profile and progress raise an error.
type ProfileHost interface {
	// ProfileState is the profile's sheet, state.profile; nil when the
	// player plays none (a pack without a ruleset).
	ProfileState() *State
	// Export keeps value, an encoding of this package, as what the story
	// exports under name (flags.x or items.x); nil removes it. An error
	// is the script's: a name the pack does not export, or no profile.
	Export(name string, value []byte) error
	// Complete marks the story completed in the profile's progress.
	Complete() error
}

// PackInfo is what scripts read from pack: the pack and the ruleset the
// world runs.
type PackInfo struct {
	ID, Version string
	// Ruleset and RulesetVersion are "" for a pack without a ruleset.
	Ruleset, RulesetVersion string
}

// SetPack gives every script of the runtime pack.id, pack.version and,
// for a story on a ruleset, pack.ruleset.id and pack.ruleset.version.
func (r *Runtime) SetPack(info PackInfo) {
	L := r.L
	t := L.NewTable()
	t.RawSetString("id", lua.LString(info.ID))
	t.RawSetString("version", lua.LString(info.Version))
	if info.Ruleset != "" {
		rs := L.NewTable()
		rs.RawSetString("id", lua.LString(info.Ruleset))
		rs.RawSetString("version", lua.LString(info.RulesetVersion))
		t.RawSetString("ruleset", r.readOnly(rs))
	}
	r.lib.RawSetString("pack", r.readOnly(t))
}

// openProfile adds state.profile and progress to the sandbox: state is
// the state library before it is made read-only.
func (r *Runtime) openProfile(lib, state *lua.LTable) {
	L := r.L
	sheet := L.NewTable()
	sheet.RawSetString("get", L.NewFunction(r.stateGet(r.profileState, "state.profile")))
	sheet.RawSetString("set", L.NewFunction(r.stateSet(r.profileState, "state.profile")))
	state.RawSetString("profile", r.readOnly(sheet))
	progress := L.NewTable()
	progress.RawSetString("export", L.NewFunction(r.progressExport))
	progress.RawSetString("complete", L.NewFunction(r.progressComplete))
	lib.RawSetString("progress", r.readOnly(progress))
}

// profileHost returns the call's ProfileHost, or raises an error outside
// a hook or for a player without a profile.
func (r *Runtime) profileHost(L *lua.LState, name string) ProfileHost {
	if r.cur == nil || r.cur.host == nil || r.cur.thread == nil {
		L.RaiseError("no player here: call %s from a hook such as on_enter", name)
	}
	h, ok := r.cur.host.(ProfileHost)
	if !ok || h.ProfileState() == nil {
		L.RaiseError("%s: this player plays no profile: the pack names no ruleset (docs/rulesets.md)", name)
	}
	return h
}

// profileState returns state.profile of the call's player.
func (r *Runtime) profileState(L *lua.LState) *State {
	return r.profileHost(L, "state.profile").ProfileState()
}

// progress.export(name, value) keeps value as what this story exports
// under name, a name in the pack's exports; nil removes it. Values follow
// the rules of state.player. It returns true, or nil and the reason when
// a limit refuses it.
func (r *Runtime) progressExport(L *lua.LState) int {
	h := r.profileHost(L, "progress.export")
	name := L.CheckString(1)
	var value []byte
	if v := L.Get(2); v != lua.LNil {
		e := encoder{}
		if err := e.value(v, 0); err != nil {
			return r.setResult(L, "progress.export", err)
		}
		value = e.buf
	}
	if th := r.cur.thread; th.writes+1 > MaxStateWrites {
		return r.setResult(L, "progress.export", limited("more than %d writes in one hook", MaxStateWrites))
	}
	r.cur.thread.writes++
	if err := h.Export(name, value); err != nil {
		var limit limitError
		if errors.As(err, &limit) {
			return r.setResult(L, "progress.export", err)
		}
		L.ArgError(1, err.Error())
	}
	L.Push(lua.LTrue)
	return 1
}

// progress.complete() marks this story completed for the player's
// profile: stories that continue it with completed = true open to it.
func (r *Runtime) progressComplete(L *lua.LState) int {
	h := r.profileHost(L, "progress.complete")
	if err := h.Complete(); err != nil {
		L.RaiseError("progress.complete: %s", err.Error())
	}
	return 0
}

// LimitError makes err a refusal that progress.export returns as nil and
// a message, instead of raising an error: for limits a Host enforces.
func LimitError(err error) error { return limitError(err.Error()) }
