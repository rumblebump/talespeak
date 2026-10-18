package ws

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

// seqGame's sessions push numbered texts from many goroutines at once, as
// timers of several players do, and remember their notify for after the
// connection is gone.
type seqGame struct {
	mu       sync.Mutex
	sessions []*seqSession
}

func (g *seqGame) NewSession(string) (Session, []Message) {
	s := &seqSession{}
	g.mu.Lock()
	g.sessions = append(g.sessions, s)
	g.mu.Unlock()
	return s, []Message{{Type: "text", Text: "first"}}
}

func (g *seqGame) last() *seqSession {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessions[len(g.sessions)-1]
}

type seqSession struct {
	mu     sync.Mutex
	next   int
	queue  []Message
	notify func()
	closed bool
}

func (s *seqSession) SetNotify(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notify = f
	if len(s.queue) > 0 {
		f()
	}
}

func (s *seqSession) Pending() ([]Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queue
	s.queue = nil
	return q, true
}

// push queues the next number and notifies, from the caller's goroutine.
func (s *seqSession) push() {
	s.mu.Lock()
	s.next++
	s.queue = append(s.queue, Message{Type: "text", Text: strconv.Itoa(s.next)})
	f := s.notify
	s.mu.Unlock()
	if f != nil {
		f()
	}
}

// Handle answers "burst" with a reply after pushing from 4 goroutines.
func (s *seqSession) Handle(m Message) []Message {
	if m.Type == "burst" {
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 25 {
					s.push()
				}
			}()
		}
		wg.Wait()
	}
	return []Message{{Type: "text", Text: "reply"}}
}

func (s *seqSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

// Pushed messages from many goroutines all arrive, in the order they were
// queued, with every reply whole.
func TestPushManyGoroutinesInOrder(t *testing.T) {
	integration(t)
	g := &seqGame{}
	_, url := newServer(t, WithGame(g))
	c := dialWelcomed(t, url)
	if m := read(t, c); m.Text != "first" {
		t.Fatalf("first = %+v", m)
	}
	const bursts = 3
	for range bursts {
		writeText(t, c, []byte(`{"type":"burst"}`))
	}
	next, replies := 1, 0
	for next <= bursts*100 || replies < bursts {
		m := read(t, c)
		switch m.Text {
		case "reply":
			replies++
		case strconv.Itoa(next):
			next++
		default:
			t.Fatalf("got %q, want %d or a reply", m.Text, next)
		}
	}
}

// A session that notifies after its connection closed neither blocks nor
// panics, and the session is closed once.
func TestNotifyAfterConnectionClosed(t *testing.T) {
	integration(t)
	g := &seqGame{}
	_, url := newServer(t, WithGame(g))
	c := dialWelcomed(t, url)
	read(t, c)
	s := g.last()
	c.Close(1000, "bye")
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session was not closed")
		}
		time.Sleep(time.Millisecond) // polling the server's goroutine, with a deadline
	}
	done := make(chan struct{})
	go func() {
		for range 100 {
			s.push()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("notify blocked after the connection closed")
	}
}

// Messages queued before the connection asked for them (before SetNotify)
// are sent as soon as it does.
func TestPushQueuedBeforeSetNotify(t *testing.T) {
	integration(t)
	_, url := newServer(t, WithGame(earlyGame{}))
	c := dialWelcomed(t, url)
	for _, want := range []string{"first", "early 1", "early 2"} {
		if m := read(t, c); m.Text != want {
			t.Fatalf("got %+v, want %q", m, want)
		}
	}
}

type earlyGame struct{}

func (earlyGame) NewSession(string) (Session, []Message) {
	s := &seqSession{}
	s.queue = []Message{{Type: "text", Text: "early 1"}, {Type: "text", Text: "early 2"}}
	return s, []Message{{Type: "text", Text: "first"}}
}

// Shutdown ends a connection whose session keeps pushing.
func TestShutdownWhilePushing(t *testing.T) {
	integration(t)
	g := &seqGame{}
	srv, url := newServer(t, WithGame(g))
	c := dialWelcomed(t, url)
	read(t, c)
	s := g.last()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.push()
			}
		}
	}()
	go func() { // keep reading, so the pushes flow until the close frame
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := srv.Shutdown(ctx)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
