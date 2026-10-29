package script

import (
	"errors"
	"strings"
	"testing"
)

// state.game, world.finish and configurable state limits (T-0018).

// gameHost is a recorder with state.game: gm says whether the player is
// in GM mode, finishErr what Finish answers.
type gameHost struct {
	recorder
	game      State
	gm        bool
	finished  []string
	finishErr error
	refusals  []string
}

func (h *gameHost) GameState() *State { h.game.Game = true; return &h.game }
func (h *gameHost) GameMaster() bool  { return h.gm }
func (h *gameHost) Finish(text string) error {
	if h.finishErr != nil {
		return h.finishErr
	}
	h.finished = append(h.finished, text)
	return nil
}
func (h *gameHost) StateRefused(msg string) { h.refusals = append(h.refusals, msg) }

func runLine(t *testing.T, r *Runtime, h Host, env *Env, line string) string {
	t.Helper()
	if rec, ok := h.(*gameHost); ok {
		rec.out = nil
	}
	if _, err := r.Run(h, env, "on_input", "p", line); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return strings.Join(h.(*gameHost).out, "|")
}

const gameScript = `function on_input(p, line)
  if line == "get" then say(tostring(state.game.get("k")))
  elseif line == "set" then local ok, err = state.game.set("k", {n = 1}) say(tostring(ok) .. " " .. tostring(err))
  elseif line == "death" then local ok, err = state.game.set("death", "stay") say(tostring(ok) .. " " .. tostring(err))
  elseif line == "bad" then local ok, err = state.game.set("death", 3) say(tostring(ok) .. " " .. tostring(err))
  elseif line == "finish" then local ok, err = world.finish("The end.") say(tostring(ok) .. " " .. tostring(err))
  elseif line == "many" then
    for i = 1, 4 do local ok, err = state.player.set("x" .. i, i) say(tostring(ok) .. " " .. tostring(err)) end
  elseif line == "long" then local ok, err = state.player.set("k", string.rep("x", 2000)) say(tostring(ok) .. " " .. tostring(err))
  end
end`

func TestGameStateGMOnly(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, gameScript)
	h := &gameHost{}
	h.game.Choices = map[string][]string{"death": {"stay", "respawn"}}
	if got := runLine(t, r, h, env, "set"); got != "say:nil state.game.set: only a game master in GM mode changes state.game" {
		t.Errorf("player set = %q", got)
	}
	h.gm = true
	for line, want := range map[string]string{
		"set":   "say:true nil",
		"get":   "say:table: ",
		"death": "say:true nil",
		"bad":   "say:nil state.game.set: death is a setting: one of stay, respawn",
	} {
		if got := runLine(t, r, h, env, line); !strings.HasPrefix(got, want) {
			t.Errorf("%s = %q, want %q", line, got, want)
		}
	}
	if v, ok := h.game.String("death"); !ok || v != "stay" {
		t.Errorf("String(death) = %q, %v", v, ok)
	}
	if len(h.refusals) != 2 { // the player's write and the bad setting, one hook each
		t.Errorf("refusals = %q", h.refusals)
	}
}

// A Host without script.Game has no state.game and no world.finish.
func TestGameStateNeedsHost(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	for _, call := range []string{`state.game.get("k")`, `state.game.set("k", 1)`, `world.finish("x")`} {
		env := load(t, r, `function on_enter() `+call+` end`)
		_, err := r.Run(&recorder{}, env, "on_enter", "p")
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "this world has no") {
			t.Errorf("%s: err = %v", call, err)
		}
	}
}

func TestWorldFinishScript(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, gameScript)
	h := &gameHost{}
	if got := runLine(t, r, h, env, "finish"); got != "say:true nil" || len(h.finished) != 1 || h.finished[0] != "The end." {
		t.Errorf("finish = %q, %q", got, h.finished)
	}
	h.finishErr = errors.New("not here")
	if got := runLine(t, r, h, env, "finish"); got != "say:nil world.finish: not here" {
		t.Errorf("refused finish = %q", got)
	}
	long := load(t, r, `function on_enter() world.finish(string.rep("x", 4001)) end`)
	if _, err := r.Run(h, long, "on_enter", "p"); !errors.Is(err, ErrScript) {
		t.Errorf("text past the limit: %v", err)
	}
}

// Limits set by the server, not the defaults, bound writes, and the
// first refusal of each hook reaches the Host.
func TestConfiguredStateLimits(t *testing.T) {
	limits := DefaultLimits
	limits.State = StateLimits{Writes: 2, ValueBytes: 1000}
	r := New(limits)
	defer r.Close()
	env := load(t, r, gameScript)
	h := &gameHost{}
	h.state.Limits = limits.State
	if got := runLine(t, r, h, env, "many"); got != "say:true nil|say:true nil|say:nil state.player.set: more than 2 writes in one hook|say:nil state.player.set: more than 2 writes in one hook" {
		t.Errorf("writes = %q", got)
	}
	if len(h.refusals) != 1 {
		t.Errorf("refusals told = %q, want the first one only", h.refusals)
	}
	if got := runLine(t, r, h, env, "long"); got != "say:nil state.player.set: value larger than 1000 bytes" {
		t.Errorf("long value = %q", got)
	}
}

// SetString, String and EncodeString agree with what scripts keep.
func TestSetString(t *testing.T) {
	s := State{Game: true, Choices: map[string][]string{"death": {"stay", "revive"}}}
	if err := s.SetString("death", "revive"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetString("death", "zombie"); err == nil {
		t.Error("a value outside the choices was kept")
	}
	if err := s.SetString("", "x"); err == nil {
		t.Error("an empty key was kept")
	}
	if err := s.SetString("free", strings.Repeat("x", MaxStateValueBytes)); err == nil {
		t.Error("a value past the limit was kept")
	}
	if v, ok := s.String("death"); !ok || v != "revive" {
		t.Errorf("String = %q, %v", v, ok)
	}
	enc, _ := s.Encoded("death")
	if string(enc) != string(EncodeString("revive")) {
		t.Errorf("encoding %q, want %q", enc, EncodeString("revive"))
	}
	if _, ok := s.String("missing"); ok {
		t.Error("String of a missing key")
	}
	s.vals["num"] = []byte{tagInt, 2}
	if _, ok := s.String("num"); ok {
		t.Error("String of a number")
	}
	s.vals["bad"] = []byte{tagString, 9, 'a'}
	if _, ok := s.String("bad"); ok {
		t.Error("String of a damaged value")
	}
}

// Game state has its own limits, apart from world state.
func TestGameStateLimitsApart(t *testing.T) {
	s := State{Game: true, Limits: StateLimits{GameKeys: 2}}
	for i, k := range []string{"a", "b", "c"} {
		err := s.SetString(k, "x")
		if (err == nil) != (i < 2) {
			t.Errorf("key %d: %v", i, err)
		}
	}
	if b, keys, name := s.limits(); b != MaxGameStateBytes || keys != 2 || name != "game" {
		t.Errorf("limits = %d %d %q", b, keys, name)
	}
}
