package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rumblebump/talespeak/internal/ws"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	New(Options{Version: "test-1"}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := get(t, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var h Health
	if err := json.NewDecoder(rec.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	if h != (Health{Status: "ok", Version: "test-1"}) {
		t.Errorf("body = %+v", h)
	}
}

func TestPages(t *testing.T) {
	tests := []struct {
		path     string
		code     int
		contains string
	}{
		{"/", http.StatusOK, "Talespeak"},
		{"/style.css", http.StatusOK, "--accent"},
		{"/missing", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := get(t, tt.path)
			if rec.Code != tt.code {
				t.Fatalf("status = %d, want %d", rec.Code, tt.code)
			}
			if !strings.Contains(rec.Body.String(), tt.contains) {
				t.Errorf("body does not contain %q", tt.contains)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	for _, path := range []string{"/", "/healthz", "/missing"} {
		rec := get(t, path)
		if got := rec.Header().Get("Content-Security-Policy"); got != ContentSecurityPolicy {
			t.Errorf("%s: CSP = %q", path, got)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s: unexpected CORS header %q", path, got)
		}
	}
}

func TestNoExternalReferences(t *testing.T) {
	// Offline first: the page may link to the source code, but must not load anything external.
	body := get(t, "/").Body.String()
	for _, attr := range []string{`src="http`, `href="//`, `src="//`, `url(http`} {
		if strings.Contains(body, attr) {
			t.Errorf("page loads an external resource: %s", attr)
		}
	}
	if strings.Contains(body, `rel="stylesheet" href="http`) {
		t.Error("external stylesheet")
	}
}

type fakeStore struct{ err error }

func (f fakeStore) Ping(context.Context) error { return f.err }

func TestHealthzChecksStore(t *testing.T) {
	tests := []struct {
		name   string
		store  Pinger
		code   int
		status string
	}{
		{"no store", nil, http.StatusOK, "ok"},
		{"store up", fakeStore{}, http.StatusOK, "ok"},
		{"store down", fakeStore{errors.New("disk gone")}, http.StatusServiceUnavailable, "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(Options{Version: "v", Store: tt.store}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			var h Health
			if err := json.NewDecoder(rec.Body).Decode(&h); err != nil {
				t.Fatal(err)
			}
			if rec.Code != tt.code || h.Status != tt.status {
				t.Errorf("got %d %q, want %d %q", rec.Code, h.Status, tt.code, tt.status)
			}
			if strings.Contains(rec.Body.String(), "disk gone") {
				t.Error("health response leaks the internal error")
			}
		})
	}
}

func TestWebSocketRoute(t *testing.T) {
	called := false
	ws := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true })
	rec := httptest.NewRecorder()
	New(Options{WebSocket: ws}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if !called {
		t.Error("/ws did not reach the WebSocket handler")
	}
	rec = httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("/ws without handler: %d, want 404", rec.Code)
	}
}

// integration skips tests that start a real (loopback) server under -short.
func integration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: starts a server")
	}
}

func checkSecurityHeaders(t *testing.T, what string, h http.Header) {
	t.Helper()
	want := map[string]string{
		"Content-Security-Policy":      ContentSecurityPolicy,
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Access-Control-Allow-Origin":  "",
	}
	for name, v := range want {
		if got := h.Get(name); got != v {
			t.Errorf("%s: %s = %q, want %q", what, name, got, v)
		}
	}
}

func TestSecurityHeadersEverywhere(t *testing.T) {
	tests := []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodGet, "/index.html", http.StatusMovedPermanently},
		{http.MethodGet, "/app.js", http.StatusOK},
		{http.MethodGet, "/style.css", http.StatusOK},
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodHead, "/healthz", http.StatusOK},
		{http.MethodHead, "/", http.StatusOK},
		{http.MethodGet, "/missing", http.StatusNotFound},
		{http.MethodPost, "/", http.StatusMethodNotAllowed},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/app.js", http.StatusMethodNotAllowed},
	}
	h := New(Options{Version: "v"})
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.code {
				t.Errorf("status = %d, want %d", rec.Code, tt.code)
			}
			checkSecurityHeaders(t, tt.method+" "+tt.path, rec.Header())
		})
	}
}

func TestStaticContentTypes(t *testing.T) {
	// With nosniff the browser trusts Content-Type, so a wrong type breaks the page.
	tests := map[string]string{
		"/":          "text/html",
		"/app.js":    "text/javascript",
		"/style.css": "text/css",
		"/healthz":   "application/json",
	}
	for path, want := range tests {
		rec := get(t, path)
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("%s: Content-Type = %q, want %s", path, got, want)
		}
	}
}

func TestOnlyStaticDirectoryServed(t *testing.T) {
	// The embedded FS is rooted at web/static; nothing outside it is reachable.
	for _, path := range []string{
		"/web.go",
		"/static/index.html",
		"/../web.go",
		"/%2e%2e/web.go",
		"/..%2fweb.go",
		"/.git/config",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.Path = path
		req.URL.RawPath = ""
		req.RequestURI = path
		New(Options{}).ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("%s: served (%d bytes), want not found", path, rec.Body.Len())
		}
		if strings.Contains(rec.Body.String(), "package web") {
			t.Errorf("%s: leaks Go source", path)
		}
	}
}

func TestSourceLink(t *testing.T) {
	// AGPL section 13: the page links to the running version's source.
	body := get(t, "/").Body.String()
	if !strings.Contains(body, `href="https://github.com/rumblebump/talespeak`) {
		t.Error("page has no link to the source code")
	}
	if !strings.Contains(body, "AGPL") {
		t.Error("page does not name the licence")
	}
}

func TestHealthzHeadersAndBody(t *testing.T) {
	for _, tt := range []struct {
		name  string
		store Pinger
	}{
		{"healthy", fakeStore{}},
		{"unhealthy", fakeStore{errors.New("down")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(Options{Version: "v1.2.3", Store: tt.store}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q", got)
			}
			var fields map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
				t.Fatalf("body %q: %v", rec.Body, err)
			}
			if len(fields) != 2 || fields["version"] != "v1.2.3" {
				t.Errorf("body = %v, want {status, version}", fields)
			}
		})
	}
}

// ctxStore records the context /healthz pings with.
type ctxStore struct {
	deadline time.Duration
	ok       bool
}

func (s *ctxStore) Ping(ctx context.Context) error {
	var d time.Time
	d, s.ok = ctx.Deadline()
	s.deadline = time.Until(d)
	return ctx.Err()
}

func TestHealthzPingHasDeadline(t *testing.T) {
	// A hung store must not hang the health check (Docker and Caddy would wait).
	st := &ctxStore{}
	rec := httptest.NewRecorder()
	New(Options{Store: st}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if !st.ok {
		t.Fatal("Ping context has no deadline")
	}
	if st.deadline <= 0 || st.deadline > 2*time.Second {
		t.Errorf("Ping deadline in %v, want within 2 s", st.deadline)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestHealthzCancelledRequest(t *testing.T) {
	// The request context is passed on: a client that went away cancels the ping.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	New(Options{Store: &ctxStore{}}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(ctx))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestWebSocketMethods(t *testing.T) {
	ws := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := New(Options{WebSocket: ws})
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/ws", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /ws: %d, want 405", m, rec.Code)
		}
	}
}

func TestWebSocketThroughServer(t *testing.T) {
	integration(t)
	sockets := ws.New("v9", slog.New(slog.NewTextHandler(io.Discard, nil)))
	hs := httptest.NewServer(New(Options{Version: "v9", WebSocket: sockets}))
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("upgrade status = %d", resp.StatusCode)
	}
	checkSecurityHeaders(t, "101 /ws", resp.Header)
	var m ws.Message
	if err := wsjson.Read(ctx, c, &m); err != nil || m.Type != "welcome" || m.Version != "v9" {
		t.Errorf("welcome = %+v, %v", m, err)
	}

	// The cross-origin refusal carries the headers too.
	_, resp, err = websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"https://evil.example"}},
	})
	if err == nil {
		t.Fatal("cross-origin upgrade succeeded")
	}
	if resp == nil {
		t.Fatal("no response to cross-origin upgrade")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin status = %d, want 403", resp.StatusCode)
	}
	checkSecurityHeaders(t, "403 /ws", resp.Header)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	go func() { // keep reading so the close handshake completes
		var m ws.Message
		wsjson.Read(ctx2, c, &m)
	}()
	if err := sockets.Shutdown(ctx2); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// TestCaddyfileMatchesEngineHeaders keeps deploy/Caddyfile and the engine in step,
// so a server without Caddy is exactly as strict as one behind it.
func TestCaddyfileMatchesEngineHeaders(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Content-Security-Policy":      ContentSecurityPolicy,
		"Permissions-Policy":           PermissionsPolicy,
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Embedder-Policy": "require-corp",
		"Cross-Origin-Resource-Policy": "same-origin",
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "same-origin",
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			got[name] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	rec := get(t, "/")
	for name, value := range want {
		if got[name] != value {
			t.Errorf("Caddyfile %s = %q, want %q", name, got[name], value)
		}
		if h := rec.Header().Get(name); h != value {
			t.Errorf("engine %s = %q, want %q", name, h, value)
		}
	}
}
