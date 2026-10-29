package game

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// notesRoom writes the profile by typed lines: "big N" keeps N bytes in
// state.profile, "keys N" N keys, "export N" exports N bytes.
const notesRoom = splitCmd + `
function on_enter(p) say("at the desk " .. tostring(#(state.profile.get("big") or ""))) end
function on_input(p, text)
  local cmd, rest = split(text)
  local n = tonumber(rest)
  local ok, why = true, nil
  if cmd == "big" then ok, why = state.profile.set("big", string.rep("x", n))
  elseif cmd == "keys" then
    for i = 1, n do
      ok, why = state.profile.set("k" .. i, i)
      if not ok then break end
    end
  elseif cmd == "export" then ok, why = progress.export("flags.note", string.rep("y", n))
  end
  say(tostring(ok) .. " " .. tostring(why))
end`

// notesPack is a story on tiny-rules made of notesRoom.
func notesPack(t *testing.T) *pack.Pack {
	t.Helper()
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml": {Data: []byte(`id: notes
version: 1.0.0
title: Notes
start: desk
ruleset: { id: tiny-rules, version: "^1.0" }
exports: [flags.note]
`)},
		"rooms/desk.yaml":  {Data: []byte("id: desk\ntitle: A desk\ndescription: Paper and ink.\nscript: desk.lua\n")},
		"scripts/desk.lua": {Data: []byte(notesRoom)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A world's configured state limits (TALESPEAK_STATE_*) hold for the
// sheet its players play and for what they export: lower ones refuse
// what the defaults allow, higher ones keep what the defaults refuse,
// and the sheet loads again under them on the next join.
func TestProfileConfiguredLimits(t *testing.T) {
	notes, rules := notesPack(t), rulesetPack(t, "tiny-rules")
	st, world, who, hero := rulesetWorld(t, notes, rules)
	ctx := context.Background()
	if _, err := st.BindProfile(ctx, store.Bind{World: world.ID, Account: who.Account, Name: who.Name, Profile: hero.ID, Pack: notes.ID, Version: notes.Version}); err != nil {
		t.Fatal(err)
	}
	open := func(limits script.StateLimits) *World {
		t.Helper()
		l := script.DefaultLimits
		l.State = limits
		w, err := NewWorld(notes, l, quiet, WithStore(st, world.ID), WithRuleset(rules, st))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(w.Close)
		return w
	}
	join := func(w *World) ws.Session {
		t.Helper()
		s, first := w.NewSession("", who)
		if s == nil {
			t.Fatalf("join refused: %+v", first)
		}
		return s
	}

	low := open(script.StateLimits{ValueBytes: 1 << 10, PlayerKeys: 10})
	s := join(low)
	for _, tt := range []struct{ line, want string }{
		{"big 2000", "nil state.profile.set: value larger than 1024 bytes"},
		{"big 900", "true nil"},
		{"keys 10", "nil state.profile.set: more than 10 keys"}, // big is one of them
		{"export 2000", "nil progress.export: value larger than 1024 bytes"},
		{"export 900", "true nil"},
	} {
		if got := say1(t, s, tt.line); got != tt.want {
			t.Errorf("low limits, %s = %q, want %q", tt.line, got, tt.want)
		}
	}
	s.Close()
	low.Close()
	if ex, _ := st.Exports(ctx, hero.ID, notes.ID); len(ex["flags.note"]) == 0 || len(ex["flags.note"]) > 1<<10 {
		t.Errorf("export kept = %d bytes", len(ex["flags.note"]))
	}

	high := open(script.StateLimits{ValueBytes: 64 << 10, Writes: 1000})
	s = join(high)
	for _, tt := range []struct{ line, want string }{
		{"big 30000", "true nil"},
		{"export 30000", "true nil"},
		{"keys 600", "nil state.profile.set: more than 500 keys"}, // the default key count holds
	} {
		if got := say1(t, s, tt.line); got != tt.want {
			t.Errorf("high limits, %s = %q, want %q", tt.line, got, tt.want)
		}
	}
	s.Close()
	sheet, _ := st.ProfileState(ctx, hero.ID)
	ex, _ := st.Exports(ctx, hero.ID, notes.ID)
	if len(sheet["big"]) < 30000 || len(ex["flags.note"]) < 30000 {
		t.Fatalf("saved sheet value %d bytes, export %d", len(sheet["big"]), len(ex["flags.note"]))
	}
	// The sheet loads again under the same limits.
	s = join(high)
	if got := texts(s.Handle(ws.Message{Type: "input", Text: "big 1"})); got != "true nil" {
		t.Errorf("after rejoin: %q", got)
	}
	s.Close()
	high.Close()

	// What a world with higher limits kept does not load into one with
	// the defaults: the player is refused, the sheet stays as it is.
	if err := st.Save(ctx, store.Save{World: world.ID, Character: mustCharacter(t, st, world, who).ID, Profile: hero.ID,
		ProfileState: []store.Change{{Key: "big", Value: script.EncodeString(strings.Repeat("x", 30000))}}}); err != nil {
		t.Fatal(err)
	}
	def := open(script.StateLimits{})
	if s, first := def.NewSession("", who); s != nil {
		s.Close()
		t.Errorf("a sheet past the limits loaded: %q", texts(first))
	} else if msg := errorText(first); msg == "" || strings.Contains(msg, "stored") {
		t.Errorf("the refused player reads %q", msg) // a plain sentence, never the internals
	}
	if sheet, _ := st.ProfileState(ctx, hero.ID); len(sheet["big"]) < 30000 {
		t.Errorf("the refused join changed the sheet: %d bytes", len(sheet["big"]))
	}
}

// mustCharacter returns who's character in world.
func mustCharacter(t *testing.T, st interface {
	Character(context.Context, int64, int64, string) (store.Character, error)
}, world store.World, who ws.Who) store.Character {
	t.Helper()
	c, err := st.Character(context.Background(), world.ID, who.Account, who.Name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
