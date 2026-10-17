package ws

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func newServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := New("test-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	hs := httptest.NewServer(s)
	t.Cleanup(hs.Close)
	return s, "ws" + strings.TrimPrefix(hs.URL, "http")
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func read(t *testing.T, c *websocket.Conn) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var m Message
	if err := wsjson.Read(ctx, c, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWelcomePingAndUnknown(t *testing.T) {
	_, url := newServer(t)
	c := dial(t, url)
	ctx := context.Background()

	if m := read(t, c); m != (Message{Type: "welcome", Version: "test-1"}) {
		t.Fatalf("first message = %+v", m)
	}
	if err := wsjson.Write(ctx, c, Message{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	if m := read(t, c); m.Type != "pong" {
		t.Errorf("reply to ping = %+v", m)
	}
	if err := wsjson.Write(ctx, c, Message{Type: "dance"}); err != nil {
		t.Fatal(err)
	}
	if m := read(t, c); m.Type != "error" {
		t.Errorf("reply to unknown type = %+v", m)
	}
}

func TestCrossOriginRefused(t *testing.T) {
	_, url := newServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"https://evil.example"}},
	})
	if err == nil {
		t.Fatal("cross-origin upgrade succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("response = %v, want 403", resp)
	}
}

func TestShutdownClosesConnections(t *testing.T) {
	s, url := newServer(t)
	c := dial(t, url)
	read(t, c) // welcome

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A real client keeps reading, which answers the server's close frame.
	readErr := make(chan error, 1)
	go func() {
		var m Message
		readErr <- wsjson.Read(ctx, c, &m)
	}()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if got := websocket.CloseStatus(<-readErr); got != websocket.StatusGoingAway {
		t.Errorf("close status = %v, want going away", got)
	}

	resp, err := http.Get(strings.Replace(url, "ws", "http", 1))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("after shutdown: %d, want 503", resp.StatusCode)
	}
}
