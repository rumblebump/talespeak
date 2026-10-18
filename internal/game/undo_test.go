package game

import (
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The new room's instance starts, then its on_enter is refused for time:
// the move is undone and the new instance freed, so repeating it cannot
// pile up instances past MaxRoomInstances (security re-review).
func TestUndoneMovesKeepNoInstance(t *testing.T) {
	fs := fstest.MapFS{
		"pack.yaml":       {Data: []byte("id: t\ntitle: T\nstart: hub\n")},
		"rooms/hub.yaml":  {Data: []byte("id: hub\ntitle: H\ndescription: H.\nscript: hub.lua\n")},
		"scripts/hub.lua": {Data: []byte(`function on_input(p, t) room.go(t) end`)},
	}
	const n = 30
	for i := range n {
		fs[fmt.Sprintf("rooms/r%d.yaml", i)] = &fstest.MapFile{Data: []byte(fmt.Sprintf("id: r%d\ntitle: R\ndescription: R.\nscript: r%d.lua\n", i, i))}
		fs[fmt.Sprintf("scripts/r%d.lua", i)] = &fstest.MapFile{Data: []byte(`words = {} for i = 1, 1000 do words[i] = "w" .. i end function on_enter() say("in") end function on_input(p, t) room.go(t) end`)}
	}
	p, err := pack.Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	// The fake clock decides the client budget; the real call limit only
	// has to be loose enough for -race on a slow machine.
	limits := script.DefaultLimits
	limits.Time = 5 * time.Second
	w, err := NewWorld(p, limits, quiet, WithClientBudget(100*time.Millisecond, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	now := time.Unix(0, 0)
	w.now = func() time.Time { now = now.Add(300 * time.Millisecond); return now }
	const key = "192.0.2.1"
	s, _ := w.NewSession(key, ws.Who{})
	ss := s.(*session)
	for i := range n {
		b := w.budgets[key]
		b.left, b.last = 0.5, now // enough for the hub hook and Instance, not on_enter
		out := s.Handle(ws.Message{Type: "input", Text: fmt.Sprintf("r%d", i)})
		if texts(out) != "error" || ss.room != "hub" || len(ss.envs) != 1 {
			t.Fatalf("move %d: %s room=%s envs=%d visited=%v", i, texts(out), ss.room, len(ss.envs), ss.visited)
		}
	}
}
