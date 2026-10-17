package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
