// Package server is the HTTP side of the engine: the embedded web client,
// the health endpoint and the security headers every response carries.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/rumblebump/talespeak/web"
)

// ContentSecurityPolicy matches deploy/Caddyfile, so a server without Caddy is
// just as strict: the browser may load nothing from another origin.
// A test reads deploy/Caddyfile and fails when the two drift apart.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'; " +
	"img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self'; connect-src 'self'; " +
	"worker-src 'self' blob:; manifest-src 'self'; object-src 'none'; frame-ancestors 'none'; " +
	"base-uri 'self'; form-action 'self'"

// PermissionsPolicy allows only the microphone (speech input), and only for this site.
const PermissionsPolicy = "camera=(), geolocation=(), payment=(), usb=(), microphone=(self)"

// Health is the body of GET /healthz.
type Health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Pinger is the part of the store the health check needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options configure the HTTP handler.
type Options struct {
	Version string
	// Store is checked by /healthz; nil means there is no store to check.
	Store Pinger
	// WebSocket serves /ws; nil leaves /ws unrouted.
	WebSocket http.Handler
	// Assets serves pack images under /packs/; nil leaves it unrouted.
	Assets http.Handler
	// Hosts are the host names served (see checkHost); empty serves all.
	Hosts []string
}

// New returns the engine's HTTP handler.
func New(opts Options) http.Handler {
	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err) // the embedded path is fixed at build time
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		h := Health{Status: "ok", Version: opts.Version}
		if opts.Store != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := opts.Store.Ping(ctx); err != nil {
				h.Status = "unavailable"
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		}
		_ = json.NewEncoder(w).Encode(h)
	})
	if opts.WebSocket != nil {
		mux.Handle("GET /ws", opts.WebSocket)
	}
	if opts.Assets != nil {
		mux.Handle("GET /packs/", opts.Assets)
	}
	mux.Handle("GET /", http.FileServerFS(static))

	return securityHeaders(checkHost(opts.Hosts, mux))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Embedder-Policy", "require-corp")
		h.Set("Permissions-Policy", PermissionsPolicy)
		next.ServeHTTP(w, r)
	})
}
