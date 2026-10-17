package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rumblebump/talespeak/internal/server"
)

func TestHealthURL(t *testing.T) {
	tests := map[string]string{
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/healthz",
		"localhost:8080": "http://localhost:8080/healthz",
		"[::]:8080":      "http://127.0.0.1:8080/healthz",
	}
	for listen, want := range tests {
		if got := healthURL(listen); got != want {
			t.Errorf("healthURL(%q) = %q, want %q", listen, got, want)
		}
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(server.New(server.Options{Version: "test"}))
	defer ok.Close()
	if err := healthcheck(ok.URL + "/healthz"); err != nil {
		t.Errorf("healthy server: %v", err)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"status":"starting"}`, http.StatusServiceUnavailable)
	}))
	defer broken.Close()
	if err := healthcheck(broken.URL); err == nil {
		t.Error("unhealthy server: want error")
	}
}

func TestUnknownCommand(t *testing.T) {
	if err := run([]string{"nope"}); err == nil {
		t.Error("want error for unknown command")
	}
}
