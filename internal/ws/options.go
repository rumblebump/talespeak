package ws

import (
	"net/http"
	"time"
)

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

// WithAuth makes every connection sign in: auth tells who sent r before
// the upgrade, or refuses it with an HTTP status (401, 403) when status is
// not 0.
func WithAuth(auth func(r *http.Request) (who Who, status int)) Option {
	return func(s *Server) { s.auth = auth }
}

// WithGame sets what connections play. Without one, the server answers only
// ping.
func WithGame(g Game) Option { return func(s *Server) { s.game = g } }

// WithRateLimit sets the per-connection message rate (per second) and burst.
func WithRateLimit(rate float64, burst int) Option {
	return func(s *Server) { s.rate, s.burst = rate, burst }
}
