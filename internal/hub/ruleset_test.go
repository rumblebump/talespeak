package hub

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/store"
)

// A world of a ruleset pack never runs, nor a story whose ruleset is not
// installed.
func TestPackRulesets(t *testing.T) {
	load := func(name string) *pack.Pack {
		p, err := pack.Load(os.DirFS(filepath.Join("..", "..", "content", "ruleset-test", name)))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cat, err := pack.NewCatalog(load("tiny-rules"), load("forest-start"), load("sky-tower"))
	if err != nil {
		t.Fatal(err)
	}
	h := New(Config{Packs: cat})
	for _, tc := range []struct {
		pack string
		want error
	}{{"forest-start", nil}, {"tiny-rules", errNoPack}, {"sky-tower", errNoRule}} {
		p, err := h.Pack(store.World{PackID: tc.pack})
		if err != tc.want || (err == nil && p.ID != tc.pack) {
			t.Errorf("%s: %v, %v; want %v", tc.pack, p, err, tc.want)
		}
	}
}
