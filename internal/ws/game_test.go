package ws

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeGame records what the server asks of it. Each session echoes the
// type it got and can be told to answer with several messages or none.
type fakeGame struct {
	mu       sync.Mutex
	handled  []string
	closed   chan struct{} // closed once a session is closed
	once     sync.Once
	first    []Message
	sessions int
}

func newFakeGame(first ...Message) *fakeGame {
	return &fakeGame{closed: make(chan struct{}), first: first}
}

func (g *fakeGame) NewSession(string) (Session, []Message) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessions++
	return &fakeSession{g: g}, g.first
}

func (g *fakeGame) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.handled...)
}

type fakeSession struct{ g *fakeGame }

func (s *fakeSession) Handle(m Message) []Message {
	s.g.mu.Lock()
	s.g.handled = append(s.g.handled, m.Type)
	s.g.mu.Unlock()
	switch m.Type {
	case "silent":
		return nil
	case "many":
		return []Message{{Type: "text", Text: "1"}, {Type: "text", Text: "2"}, {Type: "text", Text: "3"}}
	default:
		return []Message{{Type: "echo", Text: m.Type, ID: m.ID, Option: m.Option}}
	}
}

func (s *fakeSession) Close() { s.g.once.Do(func() { close(s.g.closed) }) }

func waitClosed(t *testing.T, g *fakeGame) {
	t.Helper()
	select {
	case <-g.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("session not closed within 5 s")
	}
}

func TestGameFirstMessagesFollowWelcome(t *testing.T) {
	integration(t)
	g := newFakeGame(Message{Type: "room", ID: "cell", Title: "T", Text: "x"}, Message{Type: "dialog", ID: "d1", Title: "?", Options: []string{"a", "b"}})
	_, url := newServer(t, WithGame(g))
	c := dial(t, url)
	var got []string
	for range 3 {
		data, _, closed := nextEvent(t, c)
		if closed {
			t.Fatal("closed early")
		}
		got = append(got, string(data))
	}
	want := []string{
		`{"type":"welcome","version":"test-1"}`,
		`{"type":"room","id":"cell","title":"T","text":"x"}`,
		`{"type":"dialog","id":"d1","title":"?","options":["a","b"]}`,
	}
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			t.Errorf("message %d = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestGameHandlesEverythingButPing(t *testing.T) {
	integration(t)
	g := newFakeGame()
	_, url := newServer(t, WithGame(g))
	c := dialWelcomed(t, url)

	writeText(t, c, []byte(`{"type":"choose","id":"d3","option":2}`))
	if m := read(t, c); m.Type != "echo" || m.Text != "choose" || m.ID != "d3" || m.Option != 2 {
		t.Errorf("choose reply = %+v", m)
	}
	writeText(t, c, []byte(`{"type":"ping"}`))
	if m := read(t, c); m.Type != "pong" {
		t.Errorf("ping reply = %+v", m)
	}
	// Several replies arrive in order; none at all is fine too.
	writeText(t, c, []byte(`{"type":"many"}`))
	for _, want := range []string{"1", "2", "3"} {
		if m := read(t, c); m.Type != "text" || m.Text != want {
			t.Errorf("reply = %+v, want text %s", m, want)
		}
	}
	writeText(t, c, []byte(`{"type":"silent"}`))
	writeText(t, c, []byte(`{"type":"ping"}`))
	if m := read(t, c); m.Type != "pong" {
		t.Errorf("after a silent message: %+v, want pong", m)
	}
	if got := strings.Join(g.seen(), ","); got != "choose,many,silent" {
		t.Errorf("Handle saw %s; ping must not reach it", got)
	}
}

func TestGameSessionClosedWhenClientLeaves(t *testing.T) {
	integration(t)
	g := newFakeGame()
	_, url := newServer(t, WithGame(g))
	c := dialWelcomed(t, url)
	if err := c.Close(websocket.StatusNormalClosure, "bye"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, g)
}

func TestGameSessionClosedOnShutdown(t *testing.T) {
	integration(t)
	g := newFakeGame()
	s, url := newServer(t, WithGame(g))
	c := dialWelcomed(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _, _, _ = c.Read(ctx) }() // answer the close frame
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, g)
}

func TestGameSessionClosedOnBadMessage(t *testing.T) {
	integration(t)
	g := newFakeGame()
	_, url := newServer(t, WithGame(g))
	c := dialWelcomed(t, url)
	writeText(t, c, []byte(`{"type":"choose","option":"two"}`))
	if _, status, closed := nextEvent(t, c); !closed || status != websocket.StatusUnsupportedData {
		t.Errorf("closed = %v, status %v; want 1003", closed, status)
	}
	waitClosed(t, g)
	if got := g.seen(); len(got) != 0 {
		t.Errorf("Handle saw %v", got)
	}
}

func TestWithoutGameOnlyPingIsAnswered(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	c := dialWelcomed(t, url)
	writeText(t, c, []byte(`{"type":"choose","id":"d1","option":1}`))
	if m := read(t, c); m.Type != "error" || m.Message == "" {
		t.Errorf("choose without a game = %+v", m)
	}
}

func TestEachConnectionGetsItsOwnSession(t *testing.T) {
	integration(t)
	g := newFakeGame()
	_, url := newServer(t, WithGame(g))
	for range 3 {
		c := dialWelcomed(t, url)
		// NewSession runs after the welcome is written: a pong shows this
		// handler got past it.
		writeText(t, c, []byte(`{"type":"ping"}`))
		if m := read(t, c); m.Type != "pong" {
			t.Fatalf("reply = %+v", m)
		}
	}
	g.mu.Lock()
	n := g.sessions
	g.mu.Unlock()
	if n != 3 {
		t.Errorf("%d sessions for 3 connections", n)
	}
}

// The new fields stay out of messages that do not use them, and a client's
// choose decodes into ID and Option.
func TestMessageFieldsJSON(t *testing.T) {
	tests := []struct {
		m    Message
		want string
	}{
		{Message{Type: "pong"}, `{"type":"pong"}`},
		{Message{Type: "error", Message: "x"}, `{"type":"error","message":"x"}`},
		{Message{Type: "choose", ID: "d1", Option: 3}, `{"type":"choose","id":"d1","option":3}`},
	}
	for _, tt := range tests {
		b, err := json.Marshal(tt.m)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tt.want {
			t.Errorf("Marshal(%+v) = %s, want %s", tt.m, b, tt.want)
		}
	}
	m, ok := decode(websocket.MessageText, []byte(`{"type":"choose","id":"d7","option":9,"unknown":true}`))
	if !ok || m.Type != "choose" || m.ID != "d7" || m.Option != 9 {
		t.Errorf("decode choose = %+v, %v", m, ok)
	}
	for _, bad := range []string{`{"type":"choose","option":"1"}`, `{"type":"choose","option":1.5}`, `{"type":"choose","options":"a"}`, `{"type":"choose","option":1e40}`} {
		if _, ok := decode(websocket.MessageText, []byte(bad)); ok {
			t.Errorf("decode(%s) accepted", bad)
		}
	}
}
