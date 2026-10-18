package script

import (
	"errors"
	"fmt"
	"regexp"

	lua "github.com/yuin/gopher-lua"
)

// MaxModules bounds the modules one Runtime holds, as pack.MaxModules
// bounds the files in a pack's lib folder.
const MaxModules = 100

var moduleName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// AddModule makes s what require(name) runs: a module of the pack, from
// lib/<name>.lua. Each Instance runs it at most once, on its first
// require, with the Instance's own globals.
func (r *Runtime) AddModule(name string, s *Script) error {
	switch {
	case !moduleName.MatchString(name):
		return fmt.Errorf("module name %q must be 1 to 64 of a-z, 0-9, _ and -", name)
	case r.modules[name] == nil && len(r.modules) >= MaxModules:
		return fmt.Errorf("more than %d modules", MaxModules)
	}
	if r.modules == nil {
		r.modules = map[string]*Script{}
	}
	r.modules[name] = s
	return nil
}

// require(name) returns what lib/<name>.lua returned (true when it
// returned nothing), running it the first time this Instance asks. A
// module that requires itself, directly or through others, is an error.
func (r *Runtime) require(L *lua.LState) int {
	name := L.CheckString(1)
	if r.cur == nil || r.cur.thread == nil {
		L.RaiseError("require works only while a script runs")
	}
	env := r.cur.thread.env
	if mods := r.loaded[env]; mods != nil {
		if v := mods.RawGetString(name); v != lua.LNil {
			L.Push(v)
			return 1
		}
	}
	s := r.modules[name]
	if s == nil {
		L.ArgError(1, fmt.Sprintf("no module %q in this pack (lib/%s.lua)", name, name))
	}
	if r.loading[env][name] {
		L.RaiseError("require cycle: module %q requires itself", name)
	}
	if r.loading[env] == nil {
		r.loading[env] = map[string]bool{}
	}
	r.loading[env][name] = true
	fn := L.NewFunctionFromProto(s.proto)
	fn.Env = env
	L.Push(fn)
	err := L.PCall(0, 1, nil)
	delete(r.loading[env], name)
	if len(r.loading[env]) == 0 {
		delete(r.loading, env)
	}
	if err != nil {
		var apiErr *lua.ApiError
		if errors.As(err, &apiErr) {
			L.Error(apiErr.Object, 0)
		}
		L.RaiseError("%v", err)
	}
	v := L.Get(-1)
	L.Pop(1)
	if v == lua.LNil {
		v = lua.LTrue
	}
	if _, ok := r.envs[env]; ok {
		if r.loaded[env] == nil {
			r.loaded[env] = L.NewTable()
		}
		r.loaded[env].RawSetString(name, v)
	}
	L.Push(v)
	return 1
}
