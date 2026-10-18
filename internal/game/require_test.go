package game

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

func moduleWorld(t *testing.T, lib string) (*World, error) {
	t.Helper()
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml":     {Data: []byte("id: t\ntitle: T\nstart: a\n")},
		"rooms/a.yaml":  {Data: []byte("id: a\ntitle: A\ndescription: A.\nscript: a.lua\n")},
		"rooms/b.yaml":  {Data: []byte("id: b\ntitle: B\ndescription: B.\nscript: b.lua\n")},
		"scripts/a.lua": {Data: []byte(`local g = require("greet") function on_enter() say(g.hi("a")) end function on_input() room.go("b") end`)},
		"scripts/b.lua": {Data: []byte(`local g = require("greet") function on_enter() say(g.hi("b")) end`)},
		"lib/greet.lua": {Data: []byte(lib)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewWorld(p, script.DefaultLimits, quiet)
}

// Every room of the pack shares lib modules, and each room's instance runs
// them on its own.
func TestRoomsShareModules(t *testing.T) {
	w, err := moduleWorld(t, `runs = (runs or 0) + 1 return {hi = function(room) return "hi from " .. room .. " " .. runs end}`)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	s, out := w.NewSession("")
	if got := texts(out); !strings.Contains(got, "hi from a 1") {
		t.Errorf("enter = %s", got)
	}
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "go"})); !strings.Contains(got, "hi from b 1") {
		t.Errorf("move = %s", got)
	}
}

// A module that fails while a room's top level requires it fails the world
// at start, naming the module.
func TestBrokenModuleFailsTheWorld(t *testing.T) {
	for _, lib := range []string{`error("boom")`, `x = = 1`} {
		if w, err := moduleWorld(t, lib); err == nil || !strings.Contains(err.Error(), "lib/greet.lua") {
			if w != nil {
				w.Close()
			}
			t.Errorf("%s: err = %v", lib, err)
		}
	}
}
