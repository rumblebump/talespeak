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
	"net/netip"
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
	// DefaultConnectBurst and DefaultConnectRate limit how often one client
	// address may open connections (token bucket per address): a class
	// behind one NAT address reconnecting at once fits in the burst, a
	// client reconnecting in a loop does not.
	DefaultConnectBurst = 60
	DefaultConnectRate  = 1
	// DefaultRate and DefaultBurst limit messages per connection
	// (token bucket: Burst at once, then Rate per second).
	DefaultRate  = 10
	DefaultBurst = 20

	writeTimeout = 10 * time.Second
)

// Message is the envelope of every message in both directions. Which fields
// a type uses is listed in docs/websocket-protocol.md.
type Message struct {
	Type    string   `json:"type"`
	Version string   `json:"version,omitempty"`
	Message string   `json:"message,omitempty"`
	ID      string   `json:"id,omitempty"`
	Title   string   `json:"title,omitempty"`
	Text    string   `json:"text,omitempty"`
	Options []string `json:"options,omitempty"`
	Option  int      `json:"option,omitempty"`
	Choices []int    `json:"choices,omitempty"`
	Min     int      `json:"min,omitempty"`
	Max     int      `json:"max,omitempty"`
	Items   []Item   `json:"items,omitempty"`
	Item    string   `json:"item,omitempty"`
	URL     string   `json:"url,omitempty"`
	Strict  bool     `json:"strict,omitempty"`
	Speak   bool     `json:"speak,omitempty"`
	Hide    bool     `json:"hide,omitempty"`
	From    string   `json:"from,omitempty"`
	Ms      int64    `json:"ms,omitempty"`
}

// Item is something the player can click.
type Item struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Game is what a connection plays once it is welcomed.
type Game interface {
	// NewSession starts a player's session and returns it with the
	// messages to send first. client is what the per-client limits count
	// by: the client's address, or for IPv6 its /64.
	NewSession(client string) (Session, []Message)
}

// Session is one player's connection to a Game.
type Session interface {
	// Handle answers a message from the player. ping is answered by the
	// server and never reaches Handle.
	Handle(Message) []Message
	// Close ends the session when the connection ends.
	Close()
}

// Pusher is a Session that also sends messages the player did not ask
// for: a timer ran out, or another player changed something. The server
// calls SetNotify once, before reading the first message; the session
// calls notify (from any goroutine, without blocking) when it has messages
// waiting, and the server then calls Pending and sends them.
type Pusher interface {
	SetNotify(notify func())
	// Pending returns the messages waiting, oldest first. ok is false when
	// the session fell too far behind and the connection should close.
	Pending() (ms []Message, ok bool)
}

// Option changes a Server's limits.
type Option func(*Server)

// WithIdleTimeout sets how long a client may stay silent.
func WithIdleTimeout(d time.Duration) Option { return func(s *Server) { s.idleTimeout = d } }

// WithMaxConns sets how many connections may be open at once.
func WithMaxConns(n int) Option { return func(s *Server) { s.maxConns = n } }

// WithMaxConnsPerClient sets how many connections one client address may hold.
func WithMaxConnsPerClient(n int) Option { return func(s *Server) { s.maxPerClient = n } }

// WithConnectRate sets how many connections one client address may open at
// once (burst) and per second after that (rate).
func WithConnectRate(rate float64, burst int) Option {
	return func(s *Server) { s.connectRate, s.connectBurst = rate, burst }
}

// WithClientIP sets how the client address is found (for example behind a
// reverse proxy). The default is the TCP peer address.
func WithClientIP(f func(*http.Request) string) Option { return func(s *Server) { s.clientIP = f } }

// WithGame sets what connections play. Without one, the server answers only
// ping.
func WithGame(g Game) Option { return func(s *Server) { s.game = g } }

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
	game         Game
	connectRate  float64
	connectBurst int
	rate         float64
	burst        int

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex // guards closed, active, perClient, connects and wg.Add, so Shutdown never races a new connection
	closed    bool
	active    int
	perClient map[string]int
	connects  map[string]*bucket // per client address, for the connect rate
	wg        sync.WaitGroup
}

// New returns a Server that greets clients with version.
func New(version string, log *slog.Logger, opts ...Option) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		version: version, log: log,
		idleTimeout: DefaultIdleTimeout, maxConns: DefaultMaxConns,
		maxPerClient: DefaultMaxConnsPerClient, clientIP: peerIP,
		connectRate: DefaultConnectRate, connectBurst: DefaultConnectBurst,
		rate: DefaultRate, burst: DefaultBurst,
		ctx: ctx, cancel: cancel, perClient: map[string]int{}, connects: map[string]*bucket{},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ServeHTTP upgrades the request. Cross-origin upgrades are refused: the
// Origin header must match the Host the request came in on.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	client := clientKey(s.clientIP(r))
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
	case !s.allowConnect(client):
		s.mu.Unlock()
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many new connections", http.StatusTooManyRequests)
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
	s.handle(s.ctx, c, client)
}

// handle runs one connection. Shutdown and the idle timeout close it from
// timers rather than by cancelling the read: the library drops a connection
// without a close frame when an operation's context ends.
func (s *Server) handle(ctx context.Context, c *websocket.Conn, client string) {
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
	var session Session
	var wmu sync.Mutex // held while making and writing replies, so pushed messages never overtake them
	if s.game != nil {
		var first []Message
		session, first = s.game.NewSession(client)
		defer session.Close()
		if err := writeAll(c, first); err != nil {
			return
		}
		if p, ok := session.(Pusher); ok {
			done := make(chan struct{})
			finished := make(chan struct{})
			defer func() { close(done); <-finished }()
			go s.push(c, p, &wmu, done, finished)
		}
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

		wmu.Lock()
		var replies []Message
		switch {
		case m.Type == "ping":
			replies = []Message{{Type: "pong"}}
		case session != nil:
			replies = session.Handle(m)
		default:
			replies = []Message{{Type: "error", Message: "unknown message type"}}
		}
		err = writeAll(c, replies)
		wmu.Unlock()
		if err != nil {
			return
		}
	}
}

// push sends what p has waiting each time it notifies, until done. A
// session too far behind closes the connection.
func (s *Server) push(c *websocket.Conn, p Pusher, wmu *sync.Mutex, done <-chan struct{}, finished chan<- struct{}) {
	defer close(finished)
	kick := make(chan struct{}, 1)
	p.SetNotify(func() {
		select {
		case kick <- struct{}{}:
		default: // a send is due already
		}
	})
	for {
		select {
		case <-done:
			return
		case <-kick:
		}
		wmu.Lock()
		ms, ok := p.Pending()
		err := writeAll(c, ms)
		wmu.Unlock()
		if !ok {
			c.Close(websocket.StatusPolicyViolation, "too far behind")
			return
		}
		if err != nil {
			c.CloseNow()
			return
		}
	}
}

// maxConnectBuckets bounds the connect-rate map; past it, buckets that have
// refilled (clients quiet for a while) are dropped. The caller holds mu.
const maxConnectBuckets = 10000

func (s *Server) allowConnect(client string) bool {
	b := s.connects[client]
	if b == nil {
		if len(s.connects) >= maxConnectBuckets {
			for k, old := range s.connects {
				if old.full() {
					delete(s.connects, k)
				}
			}
		}
		for k := range s.connects { // still full: drop any one, the map stays bounded
			if len(s.connects) < maxConnectBuckets {
				break
			}
			delete(s.connects, k)
		}
		b = newBucket(s.connectRate, s.connectBurst, time.Now)
		s.connects[client] = b
	}
	return b.allow()
}

// clientKey is what the per-client limits count by: the address, or for
// IPv6 its /64, which one home or school network usually gets as a whole.
func clientKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() || a.Is4In6() {
		return ip
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
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

func writeAll(c *websocket.Conn, ms []Message) error {
	for _, m := range ms {
		if err := write(c, m); err != nil {
			return err
		}
	}
	return nil
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

// full reports whether the bucket has refilled completely.
func (b *bucket) full() bool {
	return b.tokens+b.now().Sub(b.last).Seconds()*b.rate >= b.burst
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
