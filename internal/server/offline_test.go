package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/pack"
)

// No response ever carries a CORS header, whatever the request asks for:
// another origin can neither read a response nor pass a preflight.
func TestNoCORSHeaders(t *testing.T) {
	p := &pack.Pack{ID: "t", Assets: map[string]*pack.Asset{}}
	sockets := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	h := New(Options{Version: "v", WebSocket: sockets, Assets: pack.AssetHandler(p)})
	preflight := http.Header{
		"Origin":                         {"https://evil.example"},
		"Access-Control-Request-Method":  {"GET"},
		"Access-Control-Request-Headers": {"content-type"},
	}
	simple := http.Header{"Origin": {"https://evil.example"}}
	nullOrigin := http.Header{"Origin": {"null"}}
	tests := []struct {
		method, path string
		header       http.Header
	}{
		{http.MethodOptions, "/", preflight},
		{http.MethodOptions, "/healthz", preflight},
		{http.MethodOptions, "/ws", preflight},
		{http.MethodOptions, "/packs/t/assets/a.png", preflight},
		{http.MethodOptions, "*", preflight},
		{http.MethodGet, "/", simple},
		{http.MethodGet, "/healthz", simple},
		{http.MethodGet, "/app.js", simple},
		{http.MethodGet, "/ws", simple},
		{http.MethodGet, "/packs/t/assets/a.png", simple},
		{http.MethodGet, "/healthz", nullOrigin},
		{http.MethodPost, "/healthz", simple},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://talespeak.test"+strings.TrimPrefix(tt.path, "*"), nil)
			if tt.path == "*" {
				req.RequestURI, req.URL.Path = "*", "*"
			}
			req.Header = tt.header.Clone()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			for name := range rec.Header() {
				if strings.HasPrefix(http.CanonicalHeaderKey(name), "Access-Control-") {
					t.Errorf("%s = %q", name, rec.Header().Values(name))
				}
			}
			if tt.method == http.MethodOptions && rec.Code < 400 {
				t.Errorf("preflight answered %d, want a refusal", rec.Code)
			}
			if got := rec.Header().Get("Content-Security-Policy"); got == "" {
				t.Error("no Content-Security-Policy")
			}
		})
	}
}

// Every header the Caddyfile sets is either sent by the engine (checked by
// TestCaddyfileMatchesEngineHeaders) or listed here with the reason the
// engine leaves it to Caddy. A header added to the Caddyfile alone fails.
func TestCaddyfileHasNoHeaderTheEngineLacks(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	caddyOnly := map[string]string{
		"Strict-Transport-Security": "only meaningful over HTTPS, which Caddy terminates",
		"-Server":                   "Caddy removes its own Server header; the engine sends none",
	}
	engine := get(t, "/").Header()
	var names []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "header {"):
			in = true
			continue
		case in && line == "}":
			in = false
		}
		if !in || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, _ := strings.Cut(line, " ")
		name = strings.TrimPrefix(name, "?")
		names = append(names, name)
		if _, ok := caddyOnly[name]; ok {
			continue
		}
		if engine.Get(name) == "" {
			t.Errorf("Caddyfile sets %s, the engine does not", name)
		}
	}
	if len(names) < 8 {
		t.Fatalf("read %d headers from the Caddyfile header block: %v; did its format change?", len(names), names)
	}
	if got := engine.Get("Server"); got != "" {
		t.Errorf("engine sends Server: %q", got)
	}
}
