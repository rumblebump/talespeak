package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/hub"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
	"github.com/rumblebump/talespeak/internal/ws"
)

// A paused world whose game master wrote state.game and started lasting
// timers ends with world.finish: everything it kept is gone from the
// store afterwards, also what the hook that ended it wrote last.
func TestWorldFinishWhilePausedClearsState(t *testing.T) {
	integration(t)
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml":    {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: r\nsettings:\n  - {name: death, label: Death, choices: [stay, revive]}\n")},
		"rooms/r.yaml": {Data: []byte("id: r\ntitle: R\ndescription: A room.\nscript: r.lua\n")},
		"scripts/r.lua": {Data: []byte(`
function on_input(p, text)
  if text == "prep" then
    assert(state.game.set("door", "open"))
    state.player.set("hp", 3)
    timer.start("bell", 600, {room = true})
    timer.start("tick", 600, {game = true})
    say("ready")
  elseif text == "end" then
    state.game.set("last", "words")
    say(tostring(world.finish("The end.")))
  end
end`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := pack.NewCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := db.CreateAccount(ctx, "ann", "x", store.RolePlayer)
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.CreateWorld(ctx, store.NewWorld{PackID: "t", PackVersion: "1.0.0", Visibility: store.VisibilityPublic, CreatedBy: a.ID,
		GameState: []store.Change{{Key: "death", Value: script.EncodeString("stay")}}})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var worlds *hub.Hub
	done := make(chan struct{})
	worlds = hub.New(hub.Config{Store: db, Packs: cat, Log: log, Load: func(sw store.World, p *pack.Pack) (*game.World, error) {
		return game.NewWorld(p, script.DefaultLimits, log, game.WithStore(db, sw.ID), game.WithAudit(db),
			game.WithFinish(func(string) { finishWorld(worlds, db, sw.ID, log); close(done) }))
	}})
	defer worlds.Close()
	s, first := worlds.NewSession("", ws.Who{Account: a.ID, Name: a.Name, World: w.ID, GM: true})
	if s == nil {
		t.Fatalf("refused: %+v", first)
	}
	defer s.Close()
	text := func(line string) string {
		var out string
		for _, m := range s.Handle(ws.Message{Type: "input", Text: line}) {
			if m.Type == "text" {
				out += m.Text
			}
		}
		return out
	}
	if got := text("prep"); got != "ready" {
		t.Fatalf("prep = %q", got)
	}
	s.Handle(ws.Message{Type: "input", Text: "/pause"})
	c, err := db.Character(ctx, w.ID, a.ID, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	gs, _ := db.GameState(ctx, w.ID)
	rooms, _ := db.RoomTimers(ctx, w.ID)
	mine, _ := db.PlayerTimers(ctx, c.ID)
	clock, _ := db.WorldClock(ctx, w.ID)
	if len(gs) != 2 || len(rooms) != 1 || len(mine) != 1 || clock.PausedAt.IsZero() {
		t.Fatalf("before the end: game %q, room timers %d, player timers %d, clock %+v", gs, len(rooms), len(mine), clock)
	}
	if got := text("end"); got != "true" {
		t.Fatalf("end = %q", got)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the world did not finish")
	}
	if got, err := db.World(ctx, w.ID); err != nil || !got.Finished {
		t.Fatalf("world = %+v, %v", got, err)
	}
	gs, _ = db.GameState(ctx, w.ID)
	rooms, _ = db.RoomTimers(ctx, w.ID)
	mine, _ = db.PlayerTimers(ctx, c.ID)
	player, _ := db.PlayerState(ctx, c.ID)
	if len(gs) != 0 || len(rooms) != 0 || len(mine) != 0 || len(player) != 0 {
		t.Errorf("after the end: game %q, room timers %d, player timers %d, player state %q", gs, len(rooms), len(mine), player)
	}
}
