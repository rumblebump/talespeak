// Package ws is the engine's WebSocket endpoint. Every message is one JSON
// object with a "type" field; see docs/websocket-protocol.md.
package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	// MaxMessageBytes is the largest message a client may send.
	MaxMessageBytes = 16 << 10
	// DefaultIdleTimeout closes a connection that sends nothing for this long.
	// Clients send a ping well within it.
	DefaultIdleTimeout = 2 * time.Minute
	// DefaultMaxConns caps open connections; more get 503.
	DefaultMaxConns = 1000
	// DefaultMaxConnsPerClient caps connections from one address, so one
	// client cannot take every slot. A school class behind one NAT address
	// needs a seat each, so this stays well above a class size.
	DefaultMaxConnsPerClient = 100
	// DefaultRate and DefaultBurst limit messages per connection
	// (token bucket: Burst at once, then Rate per second).
	DefaultRate  = 10
	DefaultBurst = 20

	writeTimeout = 10 * time.Second
)

// Message is the envelope of every message in both directions.
type Message struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
	Message string `json:"message,omitempty"`
}

// Option changes a Server's limits.
type Option func(*Server)

// WithIdleTimeout sets how long a client may stay silent.
func WithIdleTimeout(d time.Duration) Option { return func(s *Server) { s.idleTimeout = d } }

// WithMaxConns sets how many connections may be open at once.
func WithMaxConns(n int) Option { return func(s *Server) { s.maxConns = n } }

// WithMaxConnsPerClient sets how many connections one client address may hold.
func WithMaxConnsPerClient(n int) Option { return func(s *Server) { s.maxPerClient = n } }

// WithClientIP sets how the client address is found (for example behind a
// reverse proxy). The default is the TCP peer address.
func WithClientIP(f func(*http.Request) string) Option { return func(s *Server) { s.clientIP = f } }

// WithRateLimit sets the per-connection message rate (per second) and burst.
func WithRateLimit(rate float64, burst int) Option {
	return func(s *Server) { s.rate, s.burst = rate, burst }
}

// Server accepts WebSocket connections and tracks them for shutdown.
type Server struct {
	version      string
	log          *slog.Logger
	idleTimeout  time.Duration
	maxConns     int
	maxPerClient int
	clientIP     func(*http.Request) string
	rate         float64
	burst        int

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex // guards closed, active, perClient and wg.Add, so Shutdown never races a new connection
	closed    bool
	active    int
	perClient map[string]int
	wg        sync.WaitGroup
}

// New returns a Server that greets clients with version.
func New(version string, log *slog.Logger, opts ...Option) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		version: version, log: log,
		idleTimeout: DefaultIdleTimeout, maxConns: DefaultMaxConns,
		maxPerClient: DefaultMaxConnsPerClient, clientIP: peerIP,
		rate: DefaultRate, burst: DefaultBurst,
		ctx: ctx, cancel: cancel, perClient: map[string]int{},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ServeHTTP upgrades the request. Cross-origin upgrades are refused: the
// Origin header must match the Host the request came in on.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	client := s.clientIP(r)
	s.mu.Lock()
	switch {
	case s.closed:
		s.mu.Unlock()
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	case s.active >= s.maxConns, s.perClient[client] >= s.maxPerClient:
		s.mu.Unlock()
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	s.active++
	s.perClient[client]++
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		if s.perClient[client]--; s.perClient[client] <= 0 {
			delete(s.perClient, client) // keep the map as small as the open connections
		}
		s.mu.Unlock()
		s.wg.Done()
	}()

	// The http.Server's read and write timeouts would otherwise also apply to
	// the hijacked connection and cut every WebSocket after a few seconds.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	c, err := websocket.Accept(w, r, nil) // nil options = same-origin only
	if err != nil {
		s.log.Debug("websocket accept failed", "err", err)
		return
	}
	c.SetReadLimit(MaxMessageBytes)
	s.handle(s.ctx, c)
}

// handle runs one connection. Shutdown and the idle timeout close it from
// timers rather than by cancelling the read: the library drops a connection
// without a close frame when an operation's context ends.
func (s *Server) handle(ctx context.Context, c *websocket.Conn) {
	defer c.CloseNow()
	stop := context.AfterFunc(ctx, func() {
		c.Close(websocket.StatusGoingAway, "server shutting down")
	})
	defer stop()
	idle := time.AfterFunc(s.idleTimeout, func() {
		c.Close(websocket.StatusPolicyViolation, "idle timeout")
	})
	defer idle.Stop()
	limit := newBucket(s.rate, s.burst, time.Now)

	if err := write(c, Message{Type: "welcome", Version: s.version}); err != nil {
		return
	}
	for {
		typ, data, err := c.Read(context.Background())
		if err != nil {
			return // closed by the client, the size limit, shutdown or the idle timer
		}
		idle.Reset(s.idleTimeout)

		if !limit.allow() {
			c.Close(websocket.StatusPolicyViolation, "too many messages")
			return
		}
		m, ok := decode(typ, data)
		if !ok {
			c.Close(websocket.StatusUnsupportedData, "messages must be JSON objects in text frames")
			return
		}

		var reply Message
		switch m.Type {
		case "ping":
			reply = Message{Type: "pong"}
		default:
			reply = Message{Type: "error", Message: "unknown message type"}
		}
		if err := write(c, reply); err != nil {
			return
		}
	}
}

// peerIP is the TCP peer address without the port.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// decode accepts only text frames holding one JSON object.
func decode(typ websocket.MessageType, data []byte) (Message, bool) {
	var m Message
	if typ != websocket.MessageText {
		return m, false
	}
	if t := bytes.TrimLeft(data, " \t\r\n"); len(t) == 0 || t[0] != '{' {
		return m, false
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, false
	}
	return m, true
}

func write(c *websocket.Conn, m Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return wsjson.Write(ctx, c, m)
}

// Shutdown closes every connection and waits until their handlers return or
// ctx ends.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// bucket is a token bucket: burst tokens, refilled at rate per second.
type bucket struct {
	tokens, rate, burst float64
	last                time.Time
	now                 func() time.Time
}

func newBucket(rate float64, burst int, now func() time.Time) *bucket {
	return &bucket{tokens: float64(burst), rate: rate, burst: float64(burst), last: now(), now: now}
}

func (b *bucket) allow() bool {
	t := b.now()
	b.tokens = min(b.burst, b.tokens+t.Sub(b.last).Seconds()*b.rate)
	b.last = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
