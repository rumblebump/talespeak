package game

import (
	"os"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// What the hub (package hub) asks a world: who plays, since when it is
// empty, and closing it when it is.
func TestIdleAndClose(t *testing.T) {
	p, err := pack.Load(os.DirFS("testdata/dialog"))
	if err != nil {
		t.Fatal(err)
	}
	c := newClock()
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithClock(c.Now, c.After))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	start := c.Now()
	if since, ok := w.IdleSince(); !ok || !since.Equal(start) || w.Online() != 0 {
		t.Fatalf("new world: IdleSince %v, %v; online %d", since, ok, w.Online())
	}
	a, _ := w.NewSession("", ws.Who{})
	b, _ := w.NewSession("", ws.Who{})
	if _, ok := w.IdleSince(); ok || w.Online() != 2 {
		t.Errorf("with players: idle %v, online %d", ok, w.Online())
	}
	if w.CloseIfIdle(c.Now().Add(time.Hour)) || w.CloseIfEmpty() || w.Closed() {
		t.Fatal("a world with players closed")
	}
	c.Advance(time.Minute)
	a.Close()
	if _, ok := w.IdleSince(); ok {
		t.Error("idle with one player left")
	}
	c.Advance(time.Minute)
	b.Close()
	left := c.Now()
	if since, ok := w.IdleSince(); !ok || !since.Equal(left) {
		t.Errorf("IdleSince = %v, %v; want %v", since, ok, left)
	}
	if w.CloseIfIdle(left.Add(-time.Second)) {
		t.Error("closed though the last player left after the cutoff")
	}
	if !w.CloseIfIdle(left) || !w.Closed() {
		t.Fatal("not closed at the cutoff")
	}
	if _, ok := w.IdleSince(); ok || w.CloseIfIdle(left) || w.CloseIfEmpty() {
		t.Error("a closed world reports idle or closes again")
	}
	w.Close() // twice is harmless
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if w.Online() != 0 {
		t.Error("a player joined a closed world")
	}
}

// End sends every player away with a message, runs their on_leave and
// closes the world; their connections close after the message.
func TestEnd(t *testing.T) {
	w := demoWorld(t)
	a, _ := w.NewSession("", ws.Who{})
	b, _ := w.NewSession("", ws.Who{})
	notified := 0
	a.(ws.Pusher).SetNotify(func() { notified++ })
	w.End("The world has ended.")
	if !w.Closed() || w.Online() != 0 {
		t.Fatalf("after End: closed %v, online %d", w.Closed(), w.Online())
	}
	if notified == 0 {
		t.Error("the connection was not told")
	}
	for _, s := range []ws.Session{a, b} {
		ms, ok := s.(ws.Pusher).Pending()
		if ok || len(ms) != 1 || ms[0].Type != "error" || ms[0].Message != "The world has ended." {
			t.Errorf("Pending = %+v, %v", ms, ok)
		}
		if r := s.Handle(ws.Message{Type: "input", Text: "look"}); !isError(r) {
			t.Errorf("after End, Handle = %+v", r)
		}
		s.Close()
	}
	// A session that gets its notify later still learns it is over.
	late := false
	b.(ws.Pusher).SetNotify(func() { late = true })
	if !late {
		t.Error("SetNotify after End did not notify")
	}
}
