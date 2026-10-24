package game

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// BenchmarkWorldMemory measures what one loaded world keeps on the heap
// (T-0010): idle (loaded, nobody playing) and active (one player who
// entered the start room and typed a line). Run it on a small box's
// memory limit:
//
//	GOMEMLIMIT=1GiB go test ./internal/game -run '^$' -bench WorldMemory -benchtime 1x
//
// The numbers are in docs/admin-limits.md.
func BenchmarkWorldMemory(b *testing.B) {
	for _, tc := range []struct {
		name string
		load func() (*pack.Pack, error)
	}{
		{"demo", func() (*pack.Pack, error) { return pack.Load(content.Demo()) }},
		{"clockmaker", func() (*pack.Pack, error) { return pack.Load(os.DirFS("../../content/clockmaker")) }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			p, err := tc.load()
			if err != nil {
				b.Fatal(err)
			}
			const n = 20
			for range b.N {
				base := heap()
				worlds := make([]*World, n)
				for i := range worlds {
					if worlds[i], err = NewWorld(p, script.DefaultLimits, quiet); err != nil {
						b.Fatal(err)
					}
				}
				idle := heap()
				sessions := make([]ws.Session, n)
				for i, w := range worlds {
					sessions[i], _ = w.NewSession("", ws.Who{})
					sessions[i].Handle(ws.Message{Type: "input", Text: "look"})
				}
				active := heap()
				for i, w := range worlds {
					sessions[i].Close()
					w.Close()
				}
				b.ReportMetric(float64(idle-base)/n, "idle-B/world")
				b.ReportMetric(float64(active-base)/n, "active-B/world")
			}
		})
	}
}

// heap is the live heap after full collections, with the hook
// goroutines of worlds closed before given time to end (a benchmark only:
// tests never sleep).
func heap() int64 {
	for range 5 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}
