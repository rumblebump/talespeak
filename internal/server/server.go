// Package server is the HTTP side of the engine: the embedded web client,
// the health endpoint and the security headers every response carries.
package server

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/rumblebump/talespeak/web"
)

// ContentSecurityPolicy matches deploy/Caddyfile, so a server without Caddy is
// just as strict: the browser may load nothing from another origin.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'; " +
	"img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self'; connect-src 'self'; " +
	"worker-src 'self' blob:; manifest-src 'self'; object-src 'none'; frame-ancestors 'none'; " +
	"base-uri 'self'; form-action 'self'"

// Health is the body of GET /healthz.
type Health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// New returns the engine's HTTP handler.
func New(version string) http.Handler {
	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err) // the embedded path is fixed at build time
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(Health{Status: "ok", Version: version})
	})
	mux.Handle("GET /", http.FileServerFS(static))

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
