package content_test

import (
	"io"
	"io/fs"
	"log/slog"
	"os"
	"testing"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
)

// Every pack in content/ loads and its scripts start.
func TestPacksLoad(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"demo":      content.Demo(),
		"test-pack": os.DirFS("test-pack"),
	} {
		t.Run(name, func(t *testing.T) {
			p, err := pack.Load(fsys)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Assets) == 0 {
				t.Error("no images: every pack here shows ui.image")
			}
			w, err := game.NewWorld(p, script.DefaultLimits, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			w.Close()
		})
	}
}
