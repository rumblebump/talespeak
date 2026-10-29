package game

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// state.game, the world's settings, /set and world.finish (T-0018).

// setSetting sets a setting of w as /set would, without a game master.
func setSetting(t *testing.T, w *World, name, value string) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.game.SetString(name, value); err != nil {
		t.Fatalf("setting %s to %s: %v", name, value, err)
	}
}

// gameRoom reads and writes state.game and ends the world, by typed
// lines: "get KEY", "set KEY VALUE" ("nil" removes), "big KEY" (a 15000
// byte string), "finish TEXT".
const gameRoom = splitCmd + `
-- The words tests type that the room shares (typed.go): dragon sleeps
function on_input(p, text)
  local cmd, rest = split(text)
  if cmd == "get" then
    say(tostring(state.game.get(rest)))
  elseif cmd == "set" or cmd == "big" then
    local k, v = split(rest)
    if v == "nil" then v = nil end
    if cmd == "big" then k, v = rest, string.rep("x", 15000) end
    local ok, err = state.game.set(k, v)
    say(tostring(ok) .. " " .. tostring(err))
  elseif cmd == "finish" then
    local ok, err = world.finish(rest)
    say(tostring(ok) .. " " .. tostring(err))
  end
end`

// settingsYAML is a pack.yaml with two settings.
const settingsYAML = `id: t
version: 1.0.0
title: T
start: a
settings:
  - name: death
    label: When a player dies
    choices: [stay, revive, respawn]
    default: revive
  - name: hints
    label: Hints
    choices: ["on", "off"]
`

// gameFS is roomsFS(gameRoom, b) with the settings of settingsYAML.
func gameFS() fstest.MapFS {
	fsys := roomsFS(gameRoom, `function on_enter() end`)
	fsys["pack.yaml"] = &fstest.MapFile{Data: []byte(settingsYAML)}
	return fsys
}

func gameWorld(t *testing.T, limits script.Limits, opts ...Option) *World {
	t.Helper()
	p, err := pack.Load(gameFS())
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, limits, quiet, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

func say1(t *testing.T, s ws.Session, text string) string {
	t.Helper()
	return texts(s.Handle(ws.Message{Type: "input", Text: text}))
}

// Every script reads state.game, the settings with their defaults; only
// a game master in GM mode writes it.
func TestGameStateWriters(t *testing.T) {
	w := gameWorld(t, script.DefaultLimits)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()
	asPlayer, _ := w.NewSession("", ws.Who{GM: true, AsPlayer: true})
	defer asPlayer.Close()

	for _, s := range []ws.Session{gm, player, asPlayer} {
		if got := say1(t, s, "get death"); got != "revive" {
			t.Errorf("default of death = %q, want revive", got)
		}
		if got := say1(t, s, "get hints"); got != "on" {
			t.Errorf("default of hints = %q, want the first choice", got)
		}
	}
	refused := "nil state.game.set: only a game master in GM mode changes state.game"
	if got := say1(t, player, "set door open"); got != refused {
		t.Errorf("player set = %q", got)
	}
	if got := say1(t, asPlayer, "set door open"); got != refused {
		t.Errorf("GM mode off set = %q", got)
	}
	if got := say1(t, gm, "set door open"); got != "true nil" {
		t.Fatalf("GM set = %q", got)
	}
	if got := say1(t, player, "get door"); got != "open" {
		t.Errorf("player reads %q", got)
	}
	// GM mode switched on, the same game master writes; off again, not.
	asPlayer.Handle(ws.Message{Type: "input", Text: "/gm on"})
	if got := say1(t, asPlayer, "set door shut"); got != "true nil" {
		t.Errorf("after /gm on: %q", got)
	}
	gm.Handle(ws.Message{Type: "input", Text: "/gm off"})
	if got := say1(t, gm, "set door open"); got != refused {
		t.Errorf("after /gm off: %q", got)
	}
}

// A setting takes only one of its choices, and cannot be removed.
func TestGameStateSettingChoices(t *testing.T) {
	w := gameWorld(t, script.DefaultLimits)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	for _, tt := range []struct{ line, want string }{
		{"set death respawn", "true nil"},
		{"get death", "respawn"},
		{"set death zombie", "nil state.game.set: death is a setting: one of stay, revive, respawn"},
		{"set death nil", "nil state.game.set: death is a setting: it cannot be removed"},
		{"get death", "respawn"},
	} {
		if got := say1(t, gm, tt.line); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.line, got, tt.want)
		}
	}
}

// Writes past the state.game limits fail in the script, and the log
// shows the first one of the hook.
func TestGameStateLimits(t *testing.T) {
	var log bytes.Buffer
	limits := script.DefaultLimits
	limits.State.GameBytes = 20 << 10
	p, err := pack.Load(gameFS())
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, limits, slog.New(slog.NewTextHandler(&log, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if got := say1(t, gm, "big one"); got != "true nil" {
		t.Fatalf("first value = %q", got)
	}
	if got := say1(t, gm, "big two"); got != "nil state.game.set: game state larger than 20480 bytes" {
		t.Fatalf("past the limit = %q", got)
	}
	if !strings.Contains(log.String(), "state write refused") || !strings.Contains(log.String(), "game state larger than 20480 bytes") {
		t.Errorf("log = %q", log.String())
	}
}

// /set lists the settings, changes one (audited, every player hears of
// it) and refuses what the pack does not offer; players do not see it.
func TestSetCommand(t *testing.T) {
	w := gameWorld(t, script.DefaultLimits)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	player, _ := w.NewSession("", ws.Who{})
	defer player.Close()

	cmd := func(s ws.Session, line string) string {
		ms := s.Handle(ws.Message{Type: "input", Text: line})
		var out []string
		for _, m := range ms {
			out = append(out, m.Text)
		}
		return strings.Join(out, " | ")
	}
	if got := cmd(player, "/set death stay"); !strings.Contains(got, "There is no command /set here") {
		t.Errorf("player /set = %q", got)
	}
	for _, tt := range []struct{ line, want string }{
		{"/set", "Settings of this world (type /set NAME VALUE to change one):\ndeath (When a player dies): revive; choices: stay, revive, respawn\nhints (Hints): on; choices: on, off"},
		{"/set death", "death (When a player dies): revive; choices: stay, revive, respawn"},
		{"/set nope x", `There is no setting "nope". /set lists them.`},
		{"/set death zombie", "death takes one of: stay, revive, respawn."},
		{"/set death revive", "death is revive already."},
		{"/set DEATH Stay", `set "When a player dies" to stay.`},
	} {
		if got := cmd(gm, tt.line); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.line, got, tt.want)
		}
	}
	ms, _ := player.(ws.Pusher).Pending()
	if len(ms) != 1 || ms[0].Role != NoticeGM || ms[0].Text != `set "When a player dies" to stay.` {
		t.Errorf("player heard %+v", ms)
	}
	if got := say1(t, player, "get death"); got != "stay" {
		t.Errorf("after /set: %q", got)
	}
	w.mu.Lock()
	last := w.auditLog[len(w.auditLog)-1]
	w.mu.Unlock()
	if last.Action != "set" || last.Detail != "death stay" {
		t.Errorf("audit = %+v", last)
	}
}

// A pack without settings says so.
func TestSetCommandNoSettings(t *testing.T) {
	w := roomsWorld(t, `function on_enter() end`, `function on_enter() end`)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if got := gm.Handle(ws.Message{Type: "input", Text: "/set death stay"}); len(got) != 1 || got[0].Text != "This world's pack has no settings." {
		t.Errorf("/set = %+v", got)
	}
}

// state.game lasts through a restart, like state.world: what a game
// master's hook set, what /set changed and the lobby's pick; a stored
// setting the pack no longer offers falls back to its default.
func TestGameStateRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, world, who := openStore(t, path)
	setRole(t, st, world, who[0], store.MemberGM)
	ctx := context.Background()
	if err := st.Save(ctx, store.Save{World: world.ID, GameState: []store.Change{{Key: "hints", Value: script.EncodeString("off")}}}); err != nil {
		t.Fatal(err)
	}
	open := func() *World {
		p, err := pack.Load(gameFS())
		if err != nil {
			t.Fatal(err)
		}
		w, err := NewWorld(p, script.DefaultLimits, quiet, WithStore(st, world.ID))
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	w := open()
	gm, _ := w.NewSession("", who[0])
	if got := say1(t, gm, "get hints"); got != "off" {
		t.Fatalf("stored pick = %q", got)
	}
	if got := say1(t, gm, "set door open"); got != "true nil" {
		t.Fatalf("set = %q", got)
	}
	gm.Handle(ws.Message{Type: "input", Text: "/set death stay"})
	gm.Close()
	w.Close()
	st.Close()

	st, world, who = openStore(t, path)
	vals, err := st.GameState(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(vals["death"]) != string(script.EncodeString("stay")) || len(vals) != 3 {
		t.Fatalf("stored game state = %q", vals)
	}
	// A value the pack does not offer (from an older version) is reset.
	if err := st.Save(ctx, store.Save{World: world.ID, GameState: []store.Change{{Key: "hints", Value: script.EncodeString("loud")}}}); err != nil {
		t.Fatal(err)
	}
	w = open()
	defer w.Close()
	zed, _ := w.NewSession("", who[1])
	defer zed.Close()
	for line, want := range map[string]string{"get door": "open", "get death": "stay", "get hints": "on"} {
		if got := say1(t, zed, line); got != want {
			t.Errorf("after restart %s = %q, want %q", line, got, want)
		}
	}
	// The fallback is written with the next save.
	say1(t, zed, "get door")
	if vals, _ := st.GameState(ctx, world.ID); string(vals["hints"]) != string(script.EncodeString("on")) {
		t.Errorf("hints stored as %q", vals["hints"])
	}
	// Finishing the world deletes it.
	if err := st.FinishWorld(ctx, world.ID); err != nil {
		t.Fatal(err)
	}
	if vals, _ := st.GameState(ctx, world.ID); len(vals) != 0 {
		t.Errorf("after finishing: %q", vals)
	}
}

// world.finish ends the world once the hook is over: every player reads
// its text, then that the world ended. Without WithFinish (the main
// world) it returns nil and the reason.
func TestWorldFinish(t *testing.T) {
	main := gameWorld(t, script.DefaultLimits)
	p1, _ := main.NewSession("", ws.Who{})
	defer p1.Close()
	if got := say1(t, p1, "finish Bye"); !strings.HasPrefix(got, "nil world.finish: this world cannot end from a script") {
		t.Errorf("main world: %q", got)
	}

	done := make(chan string, 2)
	var w *World
	w = gameWorld(t, script.DefaultLimits, WithFinish(func(text string) {
		w.End("This world has ended.")
		done <- text
	}))
	a, _ := w.NewSession("", ws.Who{})
	b, _ := w.NewSession("", ws.Who{})
	if got := say1(t, a, "finish The dragon sleeps."); got != "true nil" {
		t.Fatalf("finish = %q", got)
	}
	select {
	case text := <-done:
		if text != "The dragon sleeps." {
			t.Errorf("finish got %q", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the world did not end")
	}
	if !w.Closed() {
		t.Error("world still open")
	}
	for _, s := range []ws.Session{a, b} {
		ms, ok := s.(ws.Pusher).Pending()
		if ok || texts(ms) != "The dragon sleeps. | error" || ms[1].Message != "This world has ended." {
			t.Errorf("last messages %+v, open %v", ms, ok)
		}
	}
	w.mu.Lock()
	last := w.auditLog[len(w.auditLog)-1]
	w.mu.Unlock()
	if last.Action != "world.finish" || last.Detail != "The dragon sleeps." {
		t.Errorf("audit = %+v", last)
	}
}
