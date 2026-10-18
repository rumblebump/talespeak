package game

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of require (T-0026) through sessions: dialogs inside a
// module and players leaving, instances giving way, a module's top level
// at world start, state.player from a module, and players requiring at
// once.

// libWorld loads a pack with scripted rooms (room id -> script; the first
// in sorted order is the start) and lib modules.
func libWorld(t *testing.T, rooms, libs map[string]string) (*World, error) {
	t.Helper()
	ids := make([]string, 0, len(rooms))
	for id := range rooms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fsys := fstest.MapFS{"pack.yaml": {Data: []byte("id: t\ntitle: T\nstart: " + ids[0] + "\n")}}
	for id, src := range rooms {
		fsys["rooms/"+id+".yaml"] = &fstest.MapFile{Data: []byte("id: " + id + "\ntitle: " + strings.ToUpper(id) + "\ndescription: Room " + id + ".\nscript: " + id + ".lua\n")}
		fsys["scripts/"+id+".lua"] = &fstest.MapFile{Data: []byte(src)}
	}
	for name, src := range libs {
		fsys["lib/"+name+".lua"] = &fstest.MapFile{Data: []byte(src)}
	}
	p, err := pack.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet)
	if err == nil {
		t.Cleanup(w.Close)
	}
	return w, err
}

func mustLibWorld(t *testing.T, rooms, libs map[string]string) *World {
	t.Helper()
	w, err := libWorld(t, rooms, libs)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// askLib waits for the player at its top level, then moves them on.
var askLib = map[string]string{"ask": `asks = (asks or 0) + 1
local n, label = ui.dialog("Which way?", {"stay", "go"})
if n == 2 then room.go("b") end
return label`}

// A player who leaves while their room's on_enter waits in a module's
// dialog ends that hook; the next player runs the module from the start.
func TestRequireDialogThenLeave(t *testing.T) {
	w := mustLibWorld(t, map[string]string{
		"a": `function on_enter() say("chose " .. require("ask") .. " " .. asks) end
function on_input() say("again " .. require("ask") .. " " .. asks) end`,
		"b": `function on_enter() say("in b") end`,
	}, askLib)
	for range 3 {
		s, first := w.NewSession("192.0.2.1")
		if kinds(first) != "dialog:d1" {
			t.Fatalf("first = %s", kinds(first))
		}
		s.Close()
	}
	s, _ := w.NewSession("192.0.2.1")
	defer s.Close()
	if got := texts(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "chose stay 1" {
		t.Errorf("answer = %q", got)
	}
	// The module is cached for this player's instance: no second question.
	if got := s.Handle(ws.Message{Type: "input", Text: "x"}); kinds(got) != "text:" || texts(got) != "again stay 1" {
		t.Errorf("input = %s %q", kinds(got), texts(got))
	}
}

// Answering a module's dialog with the choice that calls room.go moves the
// player once the hook ends.
func TestRequireDialogThenMove(t *testing.T) {
	w := mustLibWorld(t, map[string]string{
		"a": `function on_enter() say("chose " .. require("ask")) end`,
		"b": `function on_enter() say("in b") end`,
	}, askLib)
	s, _ := w.NewSession("")
	defer s.Close()
	got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2})
	if texts(got) != "chose go | items: | in b" || roomOf(s) != "b" {
		t.Errorf("answer = %q, room = %s", texts(got), roomOf(s))
	}
}

// Closing the world while players wait in a module's dialog ends their
// hooks; their next answer gets the closed world's error.
func TestRequireDialogThenWorldClose(t *testing.T) {
	w := mustLibWorld(t, map[string]string{
		"a": `function on_enter() say("chose " .. require("ask")) end`,
		"b": `function on_enter() end`,
	}, askLib)
	var ss []ws.Session
	for range 5 {
		s, _ := w.NewSession("")
		ss = append(ss, s)
	}
	w.Close()
	for _, s := range ss {
		if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}); !strings.Contains(fmt.Sprint(got), "closed") {
			t.Errorf("answer after Close = %v", got)
		}
		s.Close()
	}
}

// A player's old room gives way after MaxRoomInstances: its module cache
// goes with it, so coming back runs the module again, while a kept room
// still has the module's table.
func TestRequireEvictedRoomRunsModuleAgain(t *testing.T) {
	rooms := map[string]string{}
	for i := 1; i <= MaxRoomInstances+2; i++ {
		rooms[fmt.Sprintf("r%d", i)] = `function on_enter(p) local m = require("visits") m.n = m.n + 1 say(p.room .. " " .. m.n) end
function on_input(p, text) room.go(text) end`
	}
	w := mustLibWorld(t, rooms, map[string]string{"visits": `return {n = 0}`})
	s, first := w.NewSession("192.0.2.1")
	defer s.Close()
	if texts(first) != "r1 1" {
		t.Fatalf("first = %q", texts(first))
	}
	for i := 2; i <= MaxRoomInstances+1; i++ { // r1 gives way when r5 starts
		room := fmt.Sprintf("r%d", i)
		if got := goTo(t, s, room); got != room+" 1" {
			t.Errorf("%s = %q", room, got)
		}
	}
	if got := goTo(t, s, "r4"); got != "r4 2" {
		t.Errorf("kept room = %q, want its module table", got)
	}
	if got := goTo(t, s, "r1"); got != "r1 1" {
		t.Errorf("evicted room = %q, want the module run again", got)
	}
}

// A module's top level runs where it is first required. Required from a
// room's top level, which the world runs at start without a player, a
// module using say or state.player fails the world, naming the module.
// Required in a hook, the same module works.
func TestRequireModuleTopLevelAtWorldStart(t *testing.T) {
	for _, lib := range []string{`say("hello")`, `state.player.set("k", 1)`, `room.go("a")`, `ui.dialog("?", {"a"})`} {
		_, err := libWorld(t, map[string]string{"a": `require("m") function on_enter() end`}, map[string]string{"m": lib})
		if err == nil || !strings.Contains(err.Error(), "lib/m.lua") || !strings.Contains(err.Error(), "scripts/a.lua") && !strings.Contains(err.Error(), "rooms/a.yaml") {
			t.Errorf("%s at the top level: err = %v", lib, err)
		}
		if strings.HasPrefix(lib, "ui.dialog") || strings.HasPrefix(lib, "room.go") {
			continue
		}
		w := mustLibWorld(t, map[string]string{"a": `function on_enter() require("m") say("ok") end`}, map[string]string{"m": lib})
		s, first := w.NewSession("")
		if isError(first) {
			t.Errorf("%s in a hook: %v", lib, first)
		}
		s.Close()
	}
}

// Modules no room requires still compile at world start, so a syntax
// error fails the world; an error at their top level only shows once a
// script requires them.
func TestRequireUnusedModules(t *testing.T) {
	if _, err := libWorld(t, map[string]string{"a": `function on_enter() end`}, map[string]string{"bad": `x = = 1`}); err == nil || !strings.Contains(err.Error(), "lib/bad.lua") {
		t.Errorf("syntax error in an unused module: err = %v", err)
	}
	w := mustLibWorld(t, map[string]string{"a": `function on_enter() say("in a") end function on_input() require("boom") end`}, map[string]string{"boom": `error("boom")`})
	s, first := w.NewSession("")
	defer s.Close()
	if texts(first) != "in a" {
		t.Errorf("first = %v", first)
	}
	if got := s.Handle(ws.Message{Type: "input", Text: "x"}); !isError(got) || strings.Contains(fmt.Sprint(got), "boom") {
		t.Errorf("require of the failing module = %v, want an error without details", got)
	}
}

// A module's functions reach state.player of the player running them, in
// every room, and separately for each player.
func TestRequireModuleUsesPlayerState(t *testing.T) {
	room := `local visits = require("visits")
function on_enter(p) say(p.room .. " " .. visits.bump()) end
function on_input(p, text) room.go(text) end`
	w := mustLibWorld(t, map[string]string{"a": room, "b": room}, map[string]string{"visits": `local M = {}
function M.bump()
  local n = (state.player.get("visits") or 0) + 1
  state.player.set("visits", n)
  return n
end
return M`})
	s1, first := w.NewSession("192.0.2.1")
	defer s1.Close()
	if texts(first) != "a 1" {
		t.Fatalf("first = %q", texts(first))
	}
	for _, step := range []struct{ room, want string }{{"b", "b 2"}, {"a", "a 3"}, {"b", "b 4"}} {
		if got := goTo(t, s1, step.room); got != step.want {
			t.Errorf("%s = %q, want %q", step.room, got, step.want)
		}
	}
	s2, first := w.NewSession("192.0.2.2")
	defer s2.Close()
	if texts(first) != "a 1" {
		t.Errorf("second player = %q", texts(first))
	}
}

// Many players requiring modules at once, each on their own goroutine, in
// their hooks and in a dialog inside a module; -race checks that every
// require runs under the world's lock.
func TestConcurrentRequire(t *testing.T) {
	w := mustLibWorld(t, map[string]string{
		"a": `local g = require("greet")
function on_enter(p) say(g.hi(p.room) .. " " .. require("ask")) end
function on_input(p, text) room.go(text) end`,
		"b": `function on_enter(p) say(require("greet").hi(p.room)) end
function on_input(p, text) room.go(text) end`,
	}, map[string]string{
		"greet": `return {hi = function(room) return "hi " .. room end}`,
		"ask":   `local n, label = ui.dialog("?", {"x", "y"}) return label`,
	})
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, first := w.NewSession(fmt.Sprintf("192.0.2.%d", i+1))
			defer s.Close()
			if kinds(first) != "dialog:d1" {
				errs <- fmt.Errorf("player %d first = %s", i, kinds(first))
				return
			}
			if got := texts(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1 + i%2})); got != "hi a "+[]string{"x", "y"}[i%2] {
				errs <- fmt.Errorf("player %d answer = %q", i, got)
			}
			for n := range 5 {
				want, room := "items: | hi b", "b"
				if n%2 == 1 { // back in a: its instance has both modules cached
					want, room = "items: | hi a "+[]string{"x", "y"}[i%2], "a"
				}
				if got := texts(s.Handle(ws.Message{Type: "input", Text: room})); got != want {
					errs <- fmt.Errorf("player %d move %d = %q, want %q", i, n, got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// Players requiring a module while the world closes: every call ends with
// output or an error, never a panic or a race.
func TestConcurrentRequireAndClose(t *testing.T) {
	w := mustLibWorld(t, map[string]string{
		"a": `function on_enter() end function on_input(p, text) say(require("ask")) end`,
	}, map[string]string{"ask": `local n, label = ui.dialog("?", {"x"}) return label`})
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _ := w.NewSession(fmt.Sprintf("192.0.2.%d", i+1))
			defer s.Close()
			for range 20 {
				s.Handle(ws.Message{Type: "input", Text: "go"})
				s.Handle(ws.Message{Type: "choose", ID: s.(*session).dialogID, Option: 1})
			}
		}()
	}
	w.Close()
	wg.Wait()
}
