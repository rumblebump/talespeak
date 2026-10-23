package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// integration skips tests that start a real (loopback) server under -short.
func integration(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: starts a WebSocket server")
	}
}

func newServer(t testing.TB, opts ...Option) (*Server, string) {
	t.Helper()
	s := New("test-1", slog.New(slog.NewTextHandler(io.Discard, nil)), opts...)
	hs := httptest.NewServer(s)
	t.Cleanup(hs.Close)
	return s, "ws" + strings.TrimPrefix(hs.URL, "http")
}

func dial(t testing.TB, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(-1) // the client side accepts anything the server sends
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func read(t testing.TB, c *websocket.Conn) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var m Message
	if err := wsjson.Read(ctx, c, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// dialWelcomed dials and consumes the welcome message.
func dialWelcomed(t testing.TB, url string) *websocket.Conn {
	t.Helper()
	c := dial(t, url)
	if m := read(t, c); m.Type != "welcome" {
		t.Fatalf("first message = %+v, want welcome", m)
	}
	return c
}

// writeText sends raw bytes as one text message.
func writeText(t testing.TB, c *websocket.Conn, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

// nextEvent reads the next server message. It returns the message, or the
// close status if the server closed the connection (-1 for a connection that
// ended without a close frame).
func nextEvent(t testing.TB, c *websocket.Conn) (msg []byte, status websocket.StatusCode, closed bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("server neither replied nor closed within 5 s")
		}
		return nil, websocket.CloseStatus(err), true
	}
	return data, 0, false
}

// pingOfSize returns a valid ping message of exactly n bytes.
func pingOfSize(t testing.TB, n int) []byte {
	t.Helper()
	const head, tail = `{"type":"ping","message":"`, `"}`
	pad := n - len(head) - len(tail)
	if pad < 0 {
		t.Fatalf("size %d too small", n)
	}
	b := []byte(head + strings.Repeat("x", pad) + tail)
	if len(b) != n {
		t.Fatalf("built %d bytes, want %d", len(b), n)
	}
	return b
}

func TestWelcomePingAndUnknown(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	c := dial(t, url)
	ctx := context.Background()

	if m := read(t, c); m.Type != "welcome" || m.Version != "test-1" {
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
	if m := read(t, c); m.Type != "error" || m.Message == "" {
		t.Errorf("reply to unknown type = %+v", m)
	}
	// The protocol says the connection stays open after an error reply.
	if err := wsjson.Write(ctx, c, Message{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	if m := read(t, c); m.Type != "pong" {
		t.Errorf("ping after error reply = %+v", m)
	}
}

func TestWireFormat(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	c := dial(t, url)

	data, _, closed := nextEvent(t, c)
	if closed {
		t.Fatal("closed before welcome")
	}
	// Exactly one JSON object with only the fields the protocol lists.
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("welcome is not a JSON object: %q", data)
	}
	if len(fields) != 2 || fields["type"] != "welcome" || fields["version"] != "test-1" {
		t.Errorf("welcome = %s, want {type, version} only", data)
	}

	writeText(t, c, []byte(`{"type":"ping"}`))
	data, _, closed = nextEvent(t, c)
	if closed {
		t.Fatal("closed after ping")
	}
	if got := strings.TrimSpace(string(data)); got != `{"type":"pong"}` {
		t.Errorf("pong = %s, want {\"type\":\"pong\"}", got)
	}
}

func TestMessagesInOrder(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	c := dialWelcomed(t, url)

	// Several messages written back to back each get their own reply, in order.
	types := []string{"ping", "nope", "ping", "", "ping"}
	for _, typ := range types {
		writeText(t, c, []byte(`{"type":"`+typ+`"}`))
	}
	for i, typ := range types {
		want := "error"
		if typ == "ping" {
			want = "pong"
		}
		if m := read(t, c); m.Type != want {
			t.Errorf("reply %d to %q = %+v, want %s", i, typ, m, want)
		}
	}
}

func TestMessageSizeLimit(t *testing.T) {
	integration(t)
	tests := []struct {
		name   string
		size   int
		closes bool
	}{
		{"small", 64, false},
		{"one below limit", MaxMessageBytes - 1, false},
		{"exactly the limit", MaxMessageBytes, false},
		{"one over limit", MaxMessageBytes + 1, true},
	}
	_, url := newServer(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dialWelcomed(t, url)
			writeText(t, c, pingOfSize(t, tt.size))
			data, status, closed := nextEvent(t, c)
			if !tt.closes {
				if closed {
					t.Fatalf("%d bytes: connection closed (%v), want pong", tt.size, status)
				}
				var m Message
				if err := json.Unmarshal(data, &m); err != nil || m.Type != "pong" {
					t.Fatalf("%d bytes: reply %s, want pong", tt.size, data)
				}
				return
			}
			if !closed {
				t.Fatalf("%d bytes: got reply %s, want close 1009", tt.size, data)
			}
			if status != websocket.StatusMessageTooBig {
				t.Errorf("%d bytes: close status = %v, want %v", tt.size, status, websocket.StatusMessageTooBig)
			}
		})
	}
}

func TestFarOversizedMessageEndsConnection(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	c := dialWelcomed(t, url)
	// The server stops reading at the limit, so the rest of a large message
	// may make the TCP connection reset under the writer: ignore write errors
	// and only check that no reply comes and the connection ends.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	big := pingOfSize(t, 4*MaxMessageBytes)
	go c.Write(ctx, websocket.MessageText, big)
	data, status, closed := nextEvent(t, c)
	if !closed {
		t.Fatalf("got reply %s, want the connection closed", data)
	}
	if status != -1 && status != websocket.StatusMessageTooBig {
		t.Errorf("close status = %v, want 1009 (or a reset)", status)
	}
}

func TestMessageSizeLimitIsPerMessage(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	c := dialWelcomed(t, url)
	// Many messages near the limit add up to far more than the limit; only a
	// single message counts.
	for i := range 8 {
		writeText(t, c, pingOfSize(t, MaxMessageBytes))
		if m := read(t, c); m.Type != "pong" {
			t.Fatalf("message %d: %+v", i, m)
		}
	}
}

// TestMalformedMessageClosesWith1003 checks the protocol's rule that a message
// that is not a JSON object closes the connection with 1003.
func TestMalformedMessageClosesWith1003(t *testing.T) {
	integration(t)
	tests := []struct {
		name string
		data string
	}{
		{"truncated object", `{"type":"ping"`},
		{"not JSON", `hello`},
		{"empty message", ``},
		{"array", `[{"type":"ping"}]`},
		{"string", `"ping"`},
		{"number", `42`},
		{"type is a number", `{"type":1}`},
		{"type is an object", `{"type":{"x":1}}`},
		{"trailing garbage", `{"type":"ping"} x`},
		{"two objects", `{"type":"ping"}{"type":"ping"}`},
	}
	_, url := newServer(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dialWelcomed(t, url)
			writeText(t, c, []byte(tt.data))
			data, status, closed := nextEvent(t, c)
			if !closed {
				t.Fatalf("got reply %s, want close 1003", data)
			}
			if status != websocket.StatusUnsupportedData {
				t.Errorf("close status = %v (%d), want %v (1003)", status, int(status), websocket.StatusUnsupportedData)
			}
		})
	}
}

func TestNullMessage(t *testing.T) {
	integration(t)
	// `null` is valid JSON but not an object. The protocol says such messages
	// close the connection with 1003.
	_, url := newServer(t)
	c := dialWelcomed(t, url)
	writeText(t, c, []byte(`null`))
	data, status, closed := nextEvent(t, c)
	if !closed {
		t.Fatalf("null: got reply %s, want close 1003", data)
	}
	if status != websocket.StatusUnsupportedData {
		t.Errorf("null: close status = %v, want 1003", status)
	}
}

func TestOtherConnectionsSurviveABadClient(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	good := dialWelcomed(t, url)
	bad := dialWelcomed(t, url)

	writeText(t, bad, pingOfSize(t, MaxMessageBytes+1))
	if _, _, closed := nextEvent(t, bad); !closed {
		t.Fatal("oversized message did not close the bad connection")
	}
	writeText(t, good, []byte(`{"type":"ping"}`))
	if m := read(t, good); m.Type != "pong" {
		t.Errorf("good connection after bad one closed: %+v", m)
	}
}

func TestClientCloseEndsHandler(t *testing.T) {
	integration(t)
	s, url := newServer(t)
	c := dialWelcomed(t, url)
	if err := c.Close(websocket.StatusNormalClosure, "bye"); err != nil {
		t.Fatal(err)
	}
	// Shutdown waits for handlers; a closed client must not keep one alive.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown after client close: %v", err)
	}
}

func TestCrossOriginRefused(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	for _, origin := range []string{
		"https://evil.example",
		"null",               // sandboxed iframes, file:// pages
		"http://127.0.0.1:1", // same host, other port
		"http://localhost" + strings.TrimPrefix(url, "ws://127.0.0.1"), // other host name, same port
	} {
		t.Run(origin, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
				HTTPHeader: http.Header{"Origin": {origin}},
			})
			if err == nil {
				t.Fatal("cross-origin upgrade succeeded")
			}
			if resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Errorf("response = %v, want 403", resp)
			}
		})
	}
}

func TestSameOriginAllowed(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	host := strings.TrimPrefix(url, "ws://")
	for _, origin := range []string{
		"", // non-browser clients send no Origin
		"http://" + host,
		"https://" + host,
		"HTTP://" + strings.ToUpper(host),
	} {
		t.Run(origin, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			h := http.Header{}
			if origin != "" {
				h.Set("Origin", origin)
			}
			c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
			if err != nil {
				t.Fatalf("same-origin upgrade refused: %v", err)
			}
			defer c.CloseNow()
			if m := read(t, c); m.Type != "welcome" {
				t.Errorf("first message = %+v", m)
			}
		})
	}
}

func TestPlainHTTPRequestRefused(t *testing.T) {
	integration(t)
	s, url := newServer(t)
	resp, err := http.Get(strings.Replace(url, "ws", "http", 1))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("plain GET: %d, want 426", resp.StatusCode)
	}
	// A refused upgrade must not leave a handler counted as running.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown after refused upgrade: %v", err)
	}
}

func TestManyConnections(t *testing.T) {
	integration(t)
	s, url := newServer(t)
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, url, nil)
			if err != nil {
				errs <- err
				return
			}
			defer c.CloseNow()
			var m Message
			if err := wsjson.Read(ctx, c, &m); err != nil || m.Type != "welcome" {
				errs <- errors.New("no welcome")
				return
			}
			for range 5 {
				if err := wsjson.Write(ctx, c, Message{Type: "ping"}); err != nil {
					errs <- err
					return
				}
				if err := wsjson.Read(ctx, c, &m); err != nil || m.Type != "pong" {
					errs <- errors.New("no pong")
					return
				}
			}
			c.Close(websocket.StatusNormalClosure, "")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// FuzzMessage sends arbitrary text messages to a live connection. Whatever the
// client sends, the server either answers with a protocol message (pong for a
// ping, error otherwise) or closes the connection with a close frame. The exact
// close code for malformed input is checked by TestMalformedMessageClosesWith1003.
func FuzzMessage(f *testing.F) {
	integration(f)
	for _, seed := range []string{
		`{"type":"ping"}`,
		`{"type":"dance"}`,
		`{"type":""}`,
		`{}`,
		`{"type":"ping","version":"x","message":"y","extra":[1,2]}`,
		`{"type":"\u0000"}`,
		`{"TYPE":"ping"}`,
		`null`,
		`[]`,
		`{`,
		`"x"`,
		`{"type":1}`,
		"\xff\xfe",
		strings.Repeat(`[`, 10000),
	} {
		f.Add([]byte(seed))
	}
	// Every input opens a connection, far more than the connect rate allows.
	_, url := newServer(f, WithConnectRate(1e9, 1<<30))
	f.Fuzz(func(t *testing.T, data []byte) {
		c := dialWelcomed(t, url)
		writeText(t, c, data)
		reply, status, closed := nextEvent(t, c)

		var want Message
		parseErr := json.Unmarshal(data, &want)
		switch {
		case len(data) > MaxMessageBytes:
			if !closed || status != websocket.StatusMessageTooBig {
				t.Fatalf("%d bytes: reply %q, status %v; want close 1009", len(data), reply, status)
			}
		case parseErr == nil && bytes.Equal(bytes.TrimSpace(data), []byte("null")):
			// Valid JSON but not an object; TestNullMessage pins the rule.
			if closed && status == -1 {
				t.Fatalf("null: connection dropped without a close frame")
			}
		case parseErr != nil:
			if !closed {
				t.Fatalf("malformed %q: got reply %q, want close", data, reply)
			}
			if status == -1 {
				t.Fatalf("malformed %q: connection dropped without a close frame", data)
			}
		default:
			if closed {
				t.Fatalf("valid %q: closed with %v, want a reply", data, status)
			}
			var got Message
			if err := json.Unmarshal(reply, &got); err != nil {
				t.Fatalf("reply %q is not JSON: %v", reply, err)
			}
			wantType := "error"
			if want.Type == "ping" {
				wantType = "pong"
			}
			if got.Type != wantType {
				t.Fatalf("message %q: reply type %q, want %q", data, got.Type, wantType)
			}
		}
	})
}
