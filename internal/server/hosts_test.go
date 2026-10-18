package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseHosts(t *testing.T) {
	got, err := ParseHosts(" Play.Example.org., stage.ts.spitz.is ,,")
	if err != nil || len(got) != 2 || got[0] != "play.example.org" || got[1] != "stage.ts.spitz.is" {
		t.Errorf("ParseHosts = %v, %v", got, err)
	}
	if got, err := ParseHosts(""); err != nil || got != nil {
		t.Errorf("empty = %v, %v", got, err)
	}
	for _, bad := range []string{"https://a.example", "a.example:443", "a_b.example", "-a.example", "a..example", "*.example", "a b"} {
		if _, err := ParseHosts(bad); err == nil {
			t.Errorf("ParseHosts(%q) accepted", bad)
		}
	}
}

func TestCheckHost(t *testing.T) {
	h := New(Options{Hosts: []string{"play.example.org"}})
	for host, want := range map[string]int{
		"play.example.org":      http.StatusOK,
		"PLAY.example.org.":     http.StatusOK,
		"play.example.org:8080": http.StatusOK,
		"127.0.0.1:8080":        http.StatusOK,
		"[::1]:8080":            http.StatusOK,
		"[::1]":                 http.StatusOK,
		"localhost:8080":        http.StatusOK,
		"evil.example":          http.StatusMisdirectedRequest,
		"evil.example:8080":     http.StatusMisdirectedRequest,
		"play.example.org.evil": http.StatusMisdirectedRequest,
		"":                      http.StatusMisdirectedRequest,
	} {
		r := httptest.NewRequest("GET", "/healthz", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %q: %d, want %d", host, w.Code, want)
		}
		// Refused or not, the response carries the security headers.
		if w.Header().Get("Content-Security-Policy") != ContentSecurityPolicy {
			t.Errorf("Host %q: no CSP", host)
		}
	}
	// No hosts: every name is served.
	r := httptest.NewRequest("GET", "/healthz", nil)
	r.Host = "anything.example"
	w := httptest.NewRecorder()
	New(Options{}).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("no hosts: %d", w.Code)
	}
}
