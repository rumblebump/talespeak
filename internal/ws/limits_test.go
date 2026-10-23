package ws

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Connection and message limits: idle timeout, rate limits, connection
// caps per server and client, and the buckets behind them.

func TestIdleTimeoutClosesWith1008(t *testing.T) {
	integration(t)
	_, url := newServer(t, WithIdleTimeout(100*time.Millisecond))
	c := dialWelcomed(t, url)
	if _, status, closed := nextEvent(t, c); !closed || status != websocket.StatusPolicyViolation {
		t.Errorf("closed=%v status=%v, want policy violation (1008)", closed, status)
	}
}

func TestIdleTimerResetsOnEachMessage(t *testing.T) {
	integration(t)
	_, url := newServer(t, WithIdleTimeout(300*time.Millisecond))
	c := dialWelcomed(t, url)
	// Five pings 100 ms apart outlast one timeout; each must still be answered.
	for i := range 5 {
		time.Sleep(100 * time.Millisecond)
		writeText(t, c, []byte(`{"type":"ping"}`))
		if m := read(t, c); m.Type != "pong" {
			t.Fatalf("ping %d: %+v", i, m)
		}
	}
}

func TestRateLimitClosesWith1008(t *testing.T) {
	integration(t)
	_, url := newServer(t, WithRateLimit(1, 3))
	c := dialWelcomed(t, url)
	for i := range 3 {
		writeText(t, c, []byte(`{"type":"ping"}`))
		if m := read(t, c); m.Type != "pong" {
			t.Fatalf("ping %d within burst: %+v", i, m)
		}
	}
	writeText(t, c, []byte(`{"type":"ping"}`))
	if _, status, closed := nextEvent(t, c); !closed || status != websocket.StatusPolicyViolation {
		t.Errorf("closed=%v status=%v, want policy violation (1008)", closed, status)
	}
}

func TestBinaryFrameClosesWith1003(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	c := dialWelcomed(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	if _, status, closed := nextEvent(t, c); !closed || status != websocket.StatusUnsupportedData {
		t.Errorf("closed=%v status=%v, want unsupported data (1003)", closed, status)
	}
}

func TestConnectionCap(t *testing.T) {
	integration(t)
	_, url := newServer(t, WithMaxConns(2))
	a := dialWelcomed(t, url)
	dialWelcomed(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("third connection: err=%v resp=%v, want 503", err, resp)
	}

	// A closed connection frees its slot.
	a.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, _, err := websocket.Dial(ctx, url, nil)
		if err == nil {
			c.CloseNow()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot not freed after close: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServerTimeoutsDoNotCutWebSockets(t *testing.T) {
	integration(t)
	s := New("v", slog.New(slog.NewTextHandler(io.Discard, nil)))
	hs := httptest.NewUnstartedServer(s)
	hs.Config.ReadTimeout = 100 * time.Millisecond
	hs.Config.WriteTimeout = 100 * time.Millisecond
	hs.Start()
	t.Cleanup(hs.Close)
	c := dialWelcomed(t, "ws"+strings.TrimPrefix(hs.URL, "http"))

	time.Sleep(300 * time.Millisecond) // well past both server timeouts
	writeText(t, c, []byte(`{"type":"ping"}`))
	if m := read(t, c); m.Type != "pong" {
		t.Errorf("after server timeouts: %+v", m)
	}
}

func TestBucket(t *testing.T) {
	now := time.Unix(0, 0)
	b := newBucket(2, 2, func() time.Time { return now })
	if !b.allow() || !b.allow() {
		t.Fatal("burst of 2 refused")
	}
	if b.allow() {
		t.Fatal("third message without refill allowed")
	}
	now = now.Add(500 * time.Millisecond) // refills one token at 2/s
	if !b.allow() {
		t.Fatal("refilled token refused")
	}
	now = now.Add(time.Hour) // refill is capped at burst
	if !b.allow() || !b.allow() || b.allow() {
		t.Fatal("refill not capped at burst")
	}
}

func TestPerClientConnectionCap(t *testing.T) {
	integration(t)
	client := "198.51.100.1"
	_, url := newServer(t, WithMaxConnsPerClient(2), WithClientIP(func(*http.Request) string { return client }))
	dialWelcomed(t, url)
	dialWelcomed(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("third connection from one client: err=%v resp=%v, want 503", err, resp)
	}

	client = "198.51.100.2" // another address still gets in
	dialWelcomed(t, url)
}

func TestConnectRateLimit(t *testing.T) {
	integration(t)
	_, url := newServer(t, WithConnectRate(0.001, 3))
	for range 3 {
		dialWelcomed(t, url).CloseNow()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("fourth connection: err = %v, resp = %v; want 429", err, resp)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
}

func TestClientKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":          "203.0.113.7",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1": "2001:db8:1:2::/64",
		"::ffff:203.0.113.7":   "::ffff:203.0.113.7",
		"not an address":       "not an address",
		"fe80::1%eth0":         "fe80::/64",
	} {
		if got := clientKey(in); got != want {
			t.Errorf("clientKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConnectBucketsStayBounded(t *testing.T) {
	s := New("v", slog.New(slog.NewTextHandler(io.Discard, nil)), WithConnectRate(0.001, 5))
	for i := range maxConnectBuckets + 10 {
		s.mu.Lock()
		s.allowConnect(strconv.Itoa(i))
		s.mu.Unlock()
	}
	if n := len(s.connects); n > maxConnectBuckets {
		t.Errorf("%d buckets, want at most %d", n, maxConnectBuckets)
	}
}

func TestConnectBucketsArePruned(t *testing.T) {
	s := New("v", slog.New(slog.NewTextHandler(io.Discard, nil)), WithConnectRate(1e9, 1))
	for i := range maxConnectBuckets + 10 {
		s.mu.Lock()
		s.allowConnect(strconv.Itoa(i))
		s.mu.Unlock()
	}
	if n := len(s.connects); n > maxConnectBuckets {
		t.Errorf("%d buckets, want at most %d", n, maxConnectBuckets)
	}
}
