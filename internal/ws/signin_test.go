package ws

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// loginAuth signs connections in by their "login" query parameter:
// account 7 with that login, or account 8 for login "other".
func loginAuth(r *http.Request) (Who, int) {
	l := r.URL.Query().Get("login")
	if l == "other" {
		return Who{Account: 8, Name: "Jo", Login: l}, 0
	}
	return Who{Account: 7, Name: "Mira", Login: l}, 0
}

// alive reports whether c still answers a ping.
func alive(t *testing.T, c *websocket.Conn) bool {
	t.Helper()
	writeText(t, c, []byte(`{"type":"ping"}`))
	_, _, closed := nextEvent(t, c)
	return !closed
}

// closedWith waits for the server to close c and returns the status.
func closedWith(t *testing.T, c *websocket.Conn) websocket.StatusCode {
	t.Helper()
	for {
		_, status, closed := nextEvent(t, c)
		if closed {
			return status
		}
	}
}

func TestSignOut(t *testing.T) {
	integration(t)
	g := newFakeGame()
	s, url := newServer(t, WithGame(g), WithAuth(loginAuth))
	a1 := dialWelcomed(t, url+"?login=a")
	a2 := dialWelcomed(t, url+"?login=a")
	b := dialWelcomed(t, url+"?login=b")
	other := dialWelcomed(t, url+"?login=other")
	for _, c := range []*websocket.Conn{a1, a2, b, other} {
		if !alive(t, c) {
			t.Fatal("a connection closed before any sign-out")
		}
	}
	if n := s.SignOut(7, "a"); n != 2 {
		t.Errorf("SignOut(7, a) closes %d, want 2", n)
	}
	for _, c := range []*websocket.Conn{a1, a2} {
		if status := closedWith(t, c); status != StatusSignedOut {
			t.Errorf("signed-out connection closed with %v, want %v", status, StatusSignedOut)
		}
	}
	if !alive(t, b) || !alive(t, other) {
		t.Fatal("SignOut closed another sign-in's connection")
	}
	if n := s.SignOut(7, ""); n != 1 {
		t.Errorf("SignOut(7, all) closes %d, want 1", n)
	}
	if status := closedWith(t, b); status != StatusSignedOut {
		t.Errorf("closed with %v", status)
	}
	if !alive(t, other) {
		t.Fatal("SignOut closed another account's connection")
	}
	if n := s.SignOut(99, ""); n != 0 {
		t.Errorf("SignOut(unknown) = %d", n)
	}
	// Closed connections leave the map.
	waitFor(t, func() bool {
		s.signins.mu.Lock()
		defer s.signins.mu.Unlock()
		return len(s.signins.conns) == 1 && len(s.signins.conns[8]) == 1
	})
}

// waitFor waits up to 5 s for ok.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5 s")
		}
		time.Sleep(5 * time.Millisecond) // polling, with a deadline
	}
}

// switchAuth answers what it is set to, and counts the calls.
type switchAuth struct {
	mu     sync.Mutex
	who    Who
	status int
	calls  int
}

func (a *switchAuth) auth(*http.Request) (Who, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.who, a.status
}

func (a *switchAuth) set(who Who, status int) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.who, a.status = who, status
	return a.calls
}

func (a *switchAuth) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func TestRecheck(t *testing.T) {
	integration(t)
	mira := Who{Account: 7, Name: "Mira", World: 1, Login: "a"}
	for _, c := range []struct {
		name   string
		who    Who
		status int
	}{
		{"signed out", Who{}, http.StatusUnauthorized},
		{"may no longer play", Who{}, http.StatusNotFound},
		{"made a game master", Who{Account: 7, Name: "Mira", World: 1, GM: true, Login: "a"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := &switchAuth{who: mira}
			g := newFakeGame()
			_, url := newServer(t, WithGame(g), WithAuth(a.auth), WithRecheck(5*time.Millisecond))
			conn := dialWelcomed(t, url)
			// A busy database is no answer: the connection stays.
			at := a.set(Who{}, http.StatusServiceUnavailable)
			waitFor(t, func() bool { return a.count() >= at+3 })
			if !alive(t, conn) {
				t.Fatal("a 503 on a recheck closed the connection")
			}
			a.set(mira, 0)
			at = a.count()
			waitFor(t, func() bool { return a.count() >= at+2 })
			if !alive(t, conn) {
				t.Fatal("an unchanged sign-in closed the connection")
			}
			a.set(c.who, c.status)
			if status := closedWith(t, conn); status != StatusSignedOut {
				t.Errorf("closed with %v, want %v", status, StatusSignedOut)
			}
			waitClosed(t, g)
		})
	}
}

func TestRecheckOff(t *testing.T) {
	integration(t)
	a := &switchAuth{who: Who{Account: 7, Login: "a"}}
	_, url := newServer(t, WithGame(newFakeGame()), WithAuth(a.auth), WithRecheck(0))
	conn := dialWelcomed(t, url)
	if !alive(t, conn) || a.count() != 1 {
		t.Fatalf("auth calls = %d, want only the one at the upgrade", a.count())
	}
}
