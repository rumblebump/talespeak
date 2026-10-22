package game

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func demoWorld(t *testing.T) *World {
	t.Helper()
	demo, err := pack.Load(os.DirFS("testdata/dialog"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(demo, script.DefaultLimits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// kinds summarizes messages as "type:id" for comparing flows.
func kinds(ms []ws.Message) string {
	var parts []string
	for _, m := range ms {
		parts = append(parts, m.Type+":"+m.ID)
	}
	return strings.Join(parts, " ")
}

// isError reports whether ms is one error, with or without a question id.
func isError(ms []ws.Message) bool {
	return len(ms) == 1 && ms[0].Type == "error"
}

func TestDemoFlow(t *testing.T) {
	w := demoWorld(t)
	s, first := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := kinds(first); got != "room:cell text: dialog:d1" {
		t.Fatalf("first = %s", got)
	}
	d := first[2]
	if d.Title != "What do you do, traveler?" || len(d.Options) != 3 {
		t.Fatalf("dialog = %+v", d)
	}

	// Asking for water loops back to the same question with a new id.
	got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})
	if kinds(got) != "text: dialog:d2" || !strings.Contains(got[0].Text, "water") {
		t.Fatalf("after 1: %+v", got)
	}
	// The old id is gone.
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2}); kinds(got) != "error:" {
		t.Fatalf("stale id: %+v", got)
	}
	got = s.Handle(ws.Message{Type: "choose", ID: "d2", Option: 2})
	if kinds(got) != "text:" || !strings.Contains(got[0].Text, "nail") {
		t.Fatalf("after 2: %+v", got)
	}
	// The hook ended: no dialog is open.
	if got := s.Handle(ws.Message{Type: "choose", ID: "d2", Option: 1}); kinds(got) != "error:" {
		t.Fatalf("after the end: %+v", got)
	}
}

func TestChooseRejectsBadOptions(t *testing.T) {
	w := demoWorld(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	for _, opt := range []int{0, -1, 4, 1 << 30} {
		if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: opt}); kinds(got) != "error:d1" {
			t.Errorf("option %d: %+v", opt, got)
		}
	}
	// The dialog is still open after bad choices.
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 3}); kinds(got) != "text: dialog:d2" {
		t.Errorf("valid choice after bad ones: %+v", got)
	}
}

func TestUnknownMessage(t *testing.T) {
	w := demoWorld(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := s.Handle(ws.Message{Type: "dance"}); kinds(got) != "error:" {
		t.Errorf("got %+v", got)
	}
}

func packWith(script string) *pack.Pack {
	fsys := fstest.MapFS{
		"pack.yaml":     {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: r\n")},
		"rooms/r.yaml":  {Data: []byte("id: r\ntitle: R\ndescription: A room.\nscript: r.lua\n")},
		"scripts/r.lua": {Data: []byte(script)},
	}
	p, err := pack.Load(fsys)
	if err != nil {
		panic(err)
	}
	return p
}

func TestScriptErrorReachesPlayerWithoutDetails(t *testing.T) {
	w, err := NewWorld(packWith(`function on_enter() say("before") error("secret detail") end`), script.DefaultLimits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, first := w.NewSession("", ws.Who{})
	if kinds(first) != "text: error:" || strings.Contains(first[1].Message, "secret") {
		t.Errorf("first = %+v", first)
	}
}

func TestScriptLimitReachesPlayer(t *testing.T) {
	w, err := NewWorld(packWith(`function on_enter() while true do end end`), script.Limits{Time: 20 * time.Millisecond, CallMemory: 32 << 20, StateMemory: 64 << 20}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, first := w.NewSession("", ws.Who{})
	if kinds(first) != "error:" || !strings.Contains(first[0].Message, "limits") {
		t.Errorf("first = %+v", first)
	}
}

func TestBadScriptFailsWorld(t *testing.T) {
	if _, err := NewWorld(packWith(`function on_enter(`), script.DefaultLimits, quiet); err == nil || !strings.Contains(err.Error(), "rooms/r.yaml") {
		t.Errorf("err = %v, want one naming rooms/r.yaml", err)
	}
}

func TestRoomWithoutScriptIsDescribed(t *testing.T) {
	fsys := fstest.MapFS{
		"pack.yaml":    {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: r\n")},
		"rooms/r.yaml": {Data: []byte("id: r\ntitle: R\ndescription: A room.\n")},
	}
	p, err := pack.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, first := w.NewSession("", ws.Who{}); kinds(first) != "room:r" {
		t.Errorf("first = %+v", first)
	}
}

// TestDialogOverWebSocket runs the demo through a real connection.
func TestDialogOverWebSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	sockets := ws.New("test", quiet, ws.WithGame(demoWorld(t)))
	srv := httptest.NewServer(sockets)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	readN := func(n int) []ws.Message {
		var ms []ws.Message
		for range n {
			var m ws.Message
			if err := wsjson.Read(ctx, c, &m); err != nil {
				t.Fatal(err)
			}
			ms = append(ms, m)
		}
		return ms
	}
	if got := kinds(readN(4)); got != "welcome: room:cell text: dialog:d1" {
		t.Fatalf("got %s", got)
	}
	if err := wsjson.Write(ctx, c, ws.Message{Type: "choose", ID: "d1", Option: 2}); err != nil {
		t.Fatal(err)
	}
	got := readN(1)
	if got[0].Type != "text" || !strings.Contains(got[0].Text, "nail") {
		t.Fatalf("got %+v", got)
	}
	// ping still works with a game attached.
	if err := wsjson.Write(ctx, c, ws.Message{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	if got := readN(1); got[0].Type != "pong" {
		t.Fatalf("got %+v", got)
	}
}

func TestCloseWorldWithPausedSessions(t *testing.T) {
	demo, err := pack.Load(os.DirFS("testdata/dialog"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(demo, script.DefaultLimits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := w.NewSession("", ws.Who{}) // waits in the demo dialog
	w.Close()
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}); kinds(got) != "error:" || !strings.Contains(got[0].Message, "closed") {
		t.Errorf("choose after Close: %+v", got)
	}
	s.Close() // after the world: no panic
	if _, first := w.NewSession("", ws.Who{}); kinds(first) != "error:" {
		t.Errorf("new session after Close: %+v", first)
	}
}

func TestSessionCloseEndsPausedHook(t *testing.T) {
	w := demoWorld(t)
	for range 5 {
		s, _ := w.NewSession("", ws.Who{})
		s.Close()
	}
	if n := w.rt.Paused(); n != 0 {
		t.Errorf("%d hooks still paused after their sessions closed", n)
	}
}
