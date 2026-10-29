package ws

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// The sign-in behind each connection: it is checked when the connection
// opens (WithAuth), again every so often (WithRecheck), and SignOut ends
// the connections of a sign-in that ended at once (a sign-out, a password
// change or reset). Closing a connection ends its game session as any
// other close does.

// StatusSignedOut closes a connection whose sign-in ended or no longer
// lets it play its world. The client asks the account API what is left
// (signed out, or a new role) before it connects again.
const StatusSignedOut websocket.StatusCode = 4002

// DefaultRecheck is how often an open connection's sign-in is checked
// again, for changes SignOut does not hear of: a password changed with
// the command line, a sign-in that expired, an account taken out of a
// world by another process.
const DefaultRecheck = time.Minute

// WithRecheck sets how often an open connection's sign-in is checked
// again with the WithAuth function; 0 or less never.
func WithRecheck(every time.Duration) Option { return func(s *Server) { s.recheck = every } }

// signins are the open connections by account, with the sign-in each was
// opened with.
type signins struct {
	mu    sync.Mutex
	conns map[int64]map[*websocket.Conn]string // account -> connection -> Who.Login
}

// watch tracks c, opened by who with r, until the returned func runs: it
// ends c when SignOut names its sign-in, and checks r's sign-in again
// every s.recheck.
func (s *Server) watch(c *websocket.Conn, r *http.Request, who Who) func() {
	if who.Account == 0 {
		return func() {}
	}
	s.signins.mu.Lock()
	if s.signins.conns == nil {
		s.signins.conns = map[int64]map[*websocket.Conn]string{}
	}
	if s.signins.conns[who.Account] == nil {
		s.signins.conns[who.Account] = map[*websocket.Conn]string{}
	}
	s.signins.conns[who.Account][c] = who.Login
	s.signins.mu.Unlock()
	if s.auth != nil {
		// A SignOut between the first check and now found nothing to
		// close: ask once more.
		if now, status := s.auth(r); signInChanged(who, now, status) {
			s.log.Info("websocket closed: its sign-in changed as it opened", "account", who.Account, "world", who.World, "status", status)
			go c.Close(StatusSignedOut, "sign-in changed")
		}
	}
	done := make(chan struct{})
	if s.auth != nil && s.recheck > 0 {
		// Only the request's cookie and query are read again, under a
		// context that does not end with the upgrade.
		r = r.WithContext(context.WithoutCancel(r.Context()))
		go s.recheckLoop(c, r, who, done)
	}
	return func() {
		close(done)
		s.signins.mu.Lock()
		defer s.signins.mu.Unlock()
		if conns := s.signins.conns[who.Account]; conns != nil {
			delete(conns, c)
			if len(conns) == 0 {
				delete(s.signins.conns, who.Account)
			}
		}
	}
}

// recheckLoop checks r's sign-in every s.recheck until done, and closes c
// once it no longer gives who. A server error (the database busy) is no
// answer: the next round asks again.
func (s *Server) recheckLoop(c *websocket.Conn, r *http.Request, who Who, done <-chan struct{}) {
	t := time.NewTicker(s.recheck)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		if now, status := s.auth(r); signInChanged(who, now, status) {
			s.log.Info("websocket closed: its sign-in changed", "account", who.Account, "world", who.World, "status", status)
			c.Close(StatusSignedOut, "sign-in changed")
			return
		}
	}
}

// signInChanged reports whether a check of the sign-in, answering now
// and status, no longer gives who. A server error (the database busy) is
// no answer.
func signInChanged(who, now Who, status int) bool {
	if status >= 500 {
		return false
	}
	return status != 0 || now.Account != who.Account || now.World != who.World || now.GM != who.GM || now.Login != who.Login
}

// SignOut closes the connections of account opened with the sign-in
// login (Who.Login), or with any of its sign-ins when login is "". It
// reports how many it closes.
func (s *Server) SignOut(account int64, login string) int {
	s.signins.mu.Lock()
	var ending []*websocket.Conn
	conns := s.signins.conns[account]
	for c, l := range conns {
		if login == "" || l == login {
			ending = append(ending, c)
			delete(conns, c) // closing now: a second SignOut does not count it again
		}
	}
	if len(conns) == 0 {
		delete(s.signins.conns, account)
	}
	s.signins.mu.Unlock()
	for _, c := range ending {
		go c.Close(StatusSignedOut, "signed out") // Close waits for the client's answer: never in the caller's request
	}
	return len(ending)
}
