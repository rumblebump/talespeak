package ws

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// pushGame's sessions push on demand: a "push" message makes the session
// queue a text and notify from another goroutine, and a "lag" message
// makes Pending report that the session fell behind.
type pushGame struct{}

func (pushGame) NewSession(string) (Session, []Message) { return &pushSession{}, nil }

type pushSession struct {
	mu     sync.Mutex
	queue  []Message
	lagged bool
	notify func()
}

func (s *pushSession) SetNotify(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notify = f
}

func (s *pushSession) Pending() ([]Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queue
	s.queue = nil
	return q, !s.lagged
}

func (s *pushSession) Handle(m Message) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch m.Type {
	case "push":
		s.queue = append(s.queue, Message{Type: "text", Text: "pushed"})
		go s.notify() // from another goroutine, as a timer does
		return []Message{{Type: "text", Text: "reply"}}
	case "lag":
		s.lagged = true
		go s.notify()
	}
	return nil
}

func (s *pushSession) Close() {}

func TestPushedMessagesFollowReplies(t *testing.T) {
	integration(t)
	_, url := newServer(t, WithGame(pushGame{}))
	c := dialWelcomed(t, url)
	for range 3 {
		writeText(t, c, []byte(`{"type":"push"}`))
		if m := read(t, c); m.Text != "reply" {
			t.Fatalf("first = %+v, want the reply", m)
		}
		if m := read(t, c); m.Text != "pushed" {
			t.Fatalf("second = %+v, want the pushed text", m)
		}
	}
}

func TestLaggingSessionClosesConnection(t *testing.T) {
	integration(t)
	_, url := newServer(t, WithGame(pushGame{}))
	c := dialWelcomed(t, url)
	writeText(t, c, []byte(`{"type":"lag"}`))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.StatusPolicyViolation {
		t.Fatalf("read after lag: %v, want a policy violation close", err)
	}
}
