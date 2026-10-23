package ws

import (
	"context"
	"errors"
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

// Shutdown: open connections close, and Shutdown gives up at its deadline.

func TestShutdownClosesConnections(t *testing.T) {
	integration(t)
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

func TestShutdownClosesManyConnections(t *testing.T) {
	integration(t)
	s, url := newServer(t)
	const n = 10
	errc := make(chan error, n)
	for range n {
		c := dialWelcomed(t, url)
		go func() {
			var m Message
			errc <- wsjson.Read(context.Background(), c, &m)
		}()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for range n {
		if got := websocket.CloseStatus(<-errc); got != websocket.StatusGoingAway {
			t.Errorf("close status = %v, want going away", got)
		}
	}
}

func TestShutdownWithoutConnections(t *testing.T) {
	s := New("v", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	// A second Shutdown is harmless.
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
	// After shutdown, requests are refused before any upgrade is attempted.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("after shutdown: %d, want 503", rec.Code)
	}
}

func TestShutdownGivesUpAtDeadline(t *testing.T) {
	integration(t)
	s, url := newServer(t)
	// This client never reads again, so it never answers the close frame and
	// the handler waits for the close handshake.
	c := dialWelcomed(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown with a stuck client = %v, want deadline exceeded", err)
	}
	c.CloseNow()
}
