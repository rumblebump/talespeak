package hub

import (
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// T-0036: the hub hands Who.GM on to the world, for player.gm and
// world.players.
func TestHubCarriesGM(t *testing.T) {
	gmPack, err := pack.Load(fstest.MapFS{
		"pack.yaml":       {Data: []byte("id: test-pack\nversion: 1.0.0\ntitle: T\nstart: hall\n")},
		"rooms/hall.yaml": {Data: []byte("id: hall\ntitle: Hall\ndescription: A hall.\nscript: hall.lua\n")},
		"scripts/hall.lua": {Data: []byte(`function on_enter(p)
  local parts = {tostring(p.gm)}
  for _, q in ipairs(world.players()) do parts[#parts + 1] = q.id .. "=" .. tostring(q.gm) end
  say(table.concat(parts, " "))
end`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := &worlds{m: map[int64]store.World{1: {ID: 1, PackID: "test-pack"}}}
	h := New(Config{Store: st, Packs: catalog(t, testPack(t)), Log: quiet,
		Load: func(store.World, *pack.Pack) (*game.World, error) {
			return game.NewWorld(gmPack, script.DefaultLimits, quiet)
		}})
	t.Cleanup(h.Close)
	for _, tt := range []struct {
		gm   bool
		want string
	}{{true, "true p1=true"}, {false, "false p1=true p2=false"}} {
		s, ms := h.NewSession("", ws.Who{World: 1, GM: tt.gm})
		if s == nil {
			t.Fatalf("refused: %+v", ms)
		}
		defer s.Close()
		var got string
		for _, m := range ms {
			if m.Type == "text" {
				got = m.Text
			}
		}
		if got != tt.want {
			t.Errorf("GM %v: on_enter said %q, want %q", tt.gm, got, tt.want)
		}
	}
}
