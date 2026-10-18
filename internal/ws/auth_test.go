package ws

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// whoGame records who and which client each session was made for, and
// refuses sessions while refuse is set.
type whoGame struct {
	mu      sync.Mutex
	who     []Who
	clients []string
	refuse  []Message
}

func (g *whoGame) NewSession(client string, who Who) (Session, []Message) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.who = append(g.who, who)
	g.clients = append(g.clients, client)
	if g.refuse != nil {
		return nil, g.refuse
	}
	return &fakeSession{g: newFakeGame()}, nil
}

func (g *whoGame) seen() ([]Who, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Who(nil), g.who...), append([]string(nil), g.clients...)
}

func dialStatus(t *testing.T, url string) (int, *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		t.Cleanup(func() { c.CloseNow() })
		return resp.StatusCode, c
	}
	if resp == nil {
		t.Fatalf("dial: %v", err)
	}
	return resp.StatusCode, nil
}

// WithAuth refuses before the upgrade with its status; the game never
// sees a refused connection.
func TestAuthRefusesBeforeUpgrade(t *testing.T) {
	integration(t)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		g := &whoGame{}
		_, url := newServer(t, WithGame(g), WithAuth(func(*http.Request) (Who, int) { return Who{}, status }))
		if got, c := dialStatus(t, url); got != status || c != nil {
			t.Errorf("auth %d: dial status %d", status, got)
		}
		if who, _ := g.seen(); len(who) != 0 {
			t.Errorf("auth %d: the game made %d sessions", status, len(who))
		}
	}
}

// The game gets who WithAuth found, and the client key.
func TestAuthPassesWho(t *testing.T) {
	integration(t)
	g := &whoGame{}
	_, url := newServer(t, WithGame(g),
		WithClientIP(func(*http.Request) string { return "2001:db8:1:2::7" }),
		WithAuth(func(r *http.Request) (Who, int) { return Who{Account: 7, Name: "Mira"}, 0 }))
	c := dialWelcomed(t, url)
	writeText(t, c, []byte(`{"type":"ping"}`)) // the session exists once a reply comes
	read(t, c)
	who, clients := g.seen()
	if len(who) != 1 || who[0] != (Who{Account: 7, Name: "Mira"}) || clients[0] != "2001:db8:1:2::/64" {
		t.Errorf("sessions for %+v from %q", who, clients)
	}
}

// Without WithAuth, sessions get the zero Who.
func TestNoAuthZeroWho(t *testing.T) {
	integration(t)
	g := &whoGame{}
	_, url := newServer(t, WithGame(g))
	c := dialWelcomed(t, url)
	writeText(t, c, []byte(`{"type":"ping"}`)) // the session exists once a reply comes
	read(t, c)
	if who, _ := g.seen(); len(who) != 1 || who[0] != (Who{}) {
		t.Errorf("who = %+v", who)
	}
}

// The connect limits come before signing in: a client over its connect
// rate costs no sign-in lookup.
func TestAuthAfterConnectLimits(t *testing.T) {
	integration(t)
	var mu sync.Mutex
	calls := 0
	_, url := newServer(t, WithGame(&whoGame{}), WithConnectRate(0.001, 2), WithAuth(func(*http.Request) (Who, int) {
		mu.Lock()
		calls++
		mu.Unlock()
		return Who{}, http.StatusUnauthorized
	}))
	for i, want := range []int{401, 401, 429, 429} {
		if got, _ := dialStatus(t, url); got != want {
			t.Errorf("dial %d: %d, want %d", i, got, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("auth called %d times, want 2 (not for connections the rate refused)", calls)
	}
}

// A nil session refuses the player: its messages arrive after the
// welcome, then the connection closes with 1008.
func TestNilSessionRefuses(t *testing.T) {
	integration(t)
	g := &whoGame{refuse: []Message{{Type: "error", Message: "Sign in to play."}}}
	_, url := newServer(t, WithGame(g))
	c := dialWelcomed(t, url)
	if m := read(t, c); m.Type != "error" || m.Message != "Sign in to play." {
		t.Errorf("refusal = %+v", m)
	}
	if _, status, closed := nextEvent(t, c); !closed || status != websocket.StatusPolicyViolation {
		t.Errorf("after the refusal: closed = %v, status %d; want 1008", closed, status)
	}

	// No messages at all: it still closes.
	g2 := &whoGame{refuse: []Message{}}
	_, url2 := newServer(t, WithGame(g2))
	c2 := dialWelcomed(t, url2)
	if _, status, closed := nextEvent(t, c2); !closed || status != websocket.StatusPolicyViolation {
		t.Errorf("silent refusal: closed = %v, status %d", closed, status)
	}
}

// replaceGame's sessions are Replacers: "replace" ends the session as if
// the player connected elsewhere, "lag" as if it fell behind.
type replaceGame struct{}

func (replaceGame) NewSession(string, Who) (Session, []Message) { return &replaceSession{}, nil }

type replaceSession struct {
	pushSession
	replaced bool
}

func (s *replaceSession) Replaced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replaced
}

func (s *replaceSession) Pending() ([]Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queue
	s.queue = nil
	return q, !s.lagged && !s.replaced
}

func (s *replaceSession) Handle(m Message) []Message {
	if m.Type != "replace" {
		return s.pushSession.Handle(m)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replaced = true
	s.queue = append(s.queue, Message{Type: "error", Message: "playing elsewhere"})
	go s.notify()
	return nil
}

// A replaced session's last message arrives, then it closes with
// StatusReplaced, which the client does not reconnect after; a Replacer
// that only fell behind still closes with 1008.
func TestReplacedClosesWith4001(t *testing.T) {
	integration(t)
	if StatusReplaced < 4000 || StatusReplaced > 4999 {
		t.Fatalf("StatusReplaced = %d, want an application code 4000-4999", StatusReplaced)
	}
	_, url := newServer(t, WithGame(replaceGame{}))
	c := dialWelcomed(t, url)
	writeText(t, c, []byte(`{"type":"replace"}`))
	if m := read(t, c); m.Type != "error" || m.Message != "playing elsewhere" {
		t.Errorf("last message = %+v", m)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != StatusReplaced {
		t.Errorf("close = %v, want %d", err, StatusReplaced)
	}

	c2 := dialWelcomed(t, url)
	writeText(t, c2, []byte(`{"type":"lag"}`))
	_, _, err = c2.Read(ctx)
	if !errors.As(err, &ce) || ce.Code != websocket.StatusPolicyViolation {
		t.Errorf("lagging Replacer: %v, want 1008", err)
	}
}
