// Package ws is the engine's WebSocket endpoint. Every message is one JSON
// object with a "type" field; see docs/websocket-protocol.md.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	// MaxMessageBytes is the largest message a client may send.
	MaxMessageBytes = 16 << 10
	// IdleTimeout closes a connection that sends nothing for this long.
	// Clients send a ping well within it.
	IdleTimeout  = 2 * time.Minute
	writeTimeout = 10 * time.Second
)

// Message is the envelope of every message in both directions.
type Message struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
	Message string `json:"message,omitempty"`
}

// Server accepts WebSocket connections and tracks them for shutdown.
type Server struct {
	version string
	log     *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex // guards closed and wg.Add, so Shutdown never races a new connection
	closed bool
	wg     sync.WaitGroup
}

// New returns a Server that greets clients with version.
func New(version string, log *slog.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{version: version, log: log, ctx: ctx, cancel: cancel}
}

// ServeHTTP upgrades the request. Cross-origin upgrades are refused: the
// Origin header must match the Host the request came in on.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	c, err := websocket.Accept(w, r, nil) // nil options = same-origin only
	if err != nil {
		s.log.Debug("websocket accept failed", "err", err)
		return
	}
	c.SetReadLimit(MaxMessageBytes)
	s.handle(s.ctx, c)
}

// handle runs one connection. Reads and writes use their own timeouts, not
// the server context: the library drops a connection without a close frame
// when an operation's context is cancelled, so shutdown sends a proper close
// from AfterFunc instead.
func (s *Server) handle(ctx context.Context, c *websocket.Conn) {
	defer c.CloseNow()
	stop := context.AfterFunc(ctx, func() {
		c.Close(websocket.StatusGoingAway, "server shutting down")
	})
	defer stop()

	if err := write(c, Message{Type: "welcome", Version: s.version}); err != nil {
		return
	}
	for {
		readCtx, cancel := context.WithTimeout(context.Background(), IdleTimeout)
		var m Message
		err := wsjson.Read(readCtx, c, &m)
		cancel()
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return // AfterFunc is closing the connection
		case errors.Is(err, context.DeadlineExceeded):
			c.Close(websocket.StatusPolicyViolation, "idle timeout")
			return
		case websocket.CloseStatus(err) != -1:
			return // client closed
		default:
			var syntaxErr *json.SyntaxError
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
				c.Close(websocket.StatusUnsupportedData, "messages must be JSON objects")
			}
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
