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

// world.finish through the hub and the store, wired as serve wires it:
// the world ends for its players, it is finished in the store with its
// state gone, the lobby no longer lists it, and the audit log keeps the
// entry.
func TestWorldFinishEndsWorld(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml":     {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: r\n")},
		"rooms/r.yaml":  {Data: []byte("id: r\ntitle: R\ndescription: A room.\nscript: r.lua\n")},
		"scripts/r.lua": {Data: []byte(`function on_input(p, text) state.world.set("k", 1) say(tostring(world.finish("The end."))) end`)},
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
	w, err := db.CreateWorld(ctx, store.NewWorld{PackID: "t", PackVersion: "1.0.0", Visibility: store.VisibilityPublic, CreatedBy: a.ID})
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
	s, _ := worlds.NewSession("", ws.Who{Account: a.ID, Name: a.Name, World: w.ID})
	if s == nil {
		t.Fatal("no session")
	}
	defer s.Close()
	if got := s.Handle(ws.Message{Type: "input", Text: "go"}); len(got) != 1 || got[0].Text != "true" {
		t.Fatalf("world.finish = %+v", got)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the world did not finish")
	}
	if got, err := db.World(ctx, w.ID); err != nil || !got.Finished {
		t.Fatalf("world = %+v, %v", got, err)
	}
	ms, open := s.(ws.Pusher).Pending()
	if open || len(ms) != 2 || ms[0].Text != "The end." || ms[1].Message != hub.EndedMessage {
		t.Errorf("last messages %+v, open %v", ms, open)
	}
	if vals, _ := db.WorldState(ctx, w.ID); len(vals) != 0 {
		t.Errorf("world state kept: %v", vals)
	}
	list, err := db.ListWorlds(ctx, a.ID, true, 0, 10)
	if err != nil || len(list) != 0 {
		t.Errorf("lobby lists %+v, %v", list, err)
	}
	if log, err := db.AuditLog(ctx, w.ID, 5); err != nil || len(log) != 1 || log[0].Action != "world.finish" || log[0].Detail != "The end." {
		t.Errorf("audit log = %+v, %v", log, err)
	}
}
