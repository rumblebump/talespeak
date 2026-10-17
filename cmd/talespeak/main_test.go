package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rumblebump/talespeak/internal/server"
)

// integration skips tests that start servers or open SQLite files under -short.
func integration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: starts a server")
	}
}

func TestHealthURL(t *testing.T) {
	tests := map[string]string{
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/healthz",
		"localhost:8080": "http://localhost:8080/healthz",
		"[::]:8080":      "http://127.0.0.1:8080/healthz",
		"8080":           "http://127.0.0.1:8080/healthz",
		"127.0.0.1:80":   "http://127.0.0.1:80/healthz",
		"[::1]:8080":     "http://[::1]:8080/healthz",
		"10.0.0.5:8080":  "http://10.0.0.5:8080/healthz",
	}
	for listen, want := range tests {
		if got := healthURL(listen); got != want {
			t.Errorf("healthURL(%q) = %q, want %q", listen, got, want)
		}
	}
}

func TestHealthcheck(t *testing.T) {
	integration(t)
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

func TestHealthcheckRejects(t *testing.T) {
	integration(t)
	tests := []struct {
		name string
		code int
		body string
	}{
		{"store down", http.StatusServiceUnavailable, `{"status":"unavailable","version":"v"}`},
		{"200 but not ok", http.StatusOK, `{"status":"unavailable","version":"v"}`},
		{"503 with ok body", http.StatusServiceUnavailable, `{"status":"ok","version":"v"}`},
		{"not JSON", http.StatusOK, `<html>ok</html>`},
		{"empty body", http.StatusOK, ``},
		{"no status field", http.StatusOK, `{"version":"v"}`},
		{"not found", http.StatusNotFound, `404 page not found`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.code)
				io.WriteString(w, tt.body)
			}))
			defer hs.Close()
			if err := healthcheck(hs.URL + "/healthz"); err == nil {
				t.Errorf("%d %q: want error", tt.code, tt.body)
			}
		})
	}
}

func TestHealthcheckNoServer(t *testing.T) {
	integration(t)
	hs := httptest.NewServer(http.NotFoundHandler())
	url := hs.URL + "/healthz"
	hs.Close() // nothing listens there any more
	if err := healthcheck(url); err == nil {
		t.Error("closed server: want error")
	}
}

func TestHealthcheckCommandUsesListenEnv(t *testing.T) {
	integration(t)
	hs := httptest.NewServer(server.New(server.Options{Version: "test"}))
	defer hs.Close()
	t.Setenv("TALESPEAK_LISTEN", strings.TrimPrefix(hs.URL, "http://"))
	if err := run([]string{"healthcheck"}); err != nil {
		t.Errorf("healthcheck command: %v", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	for _, args := range [][]string{{"nope"}, {""}, {"SERVE"}, {"-listen", ":8080"}} {
		err := run(args)
		if err == nil {
			t.Errorf("run(%q): want error for unknown command", args)
			continue
		}
		if !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("run(%q) = %v, want unknown command", args, err)
		}
	}
}

// captureStdout runs fn and returns what it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	return <-done
}

func TestVersionCommand(t *testing.T) {
	orig := version
	version = "v1.2.3-test"
	defer func() { version = orig }()
	var err error
	out := captureStdout(t, func() { err = run([]string{"version"}) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "v1.2.3-test\n" {
		t.Errorf("version output = %q", out)
	}
}

func TestServeFlagErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		is   error
	}{
		{"unknown flag", []string{"-bogus"}, nil},
		{"missing value", []string{"-listen"}, nil},
		{"help", []string{"-h"}, flag.ErrHelp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Point the data directory at a place serve must never create.
			never := filepath.Join(t.TempDir(), "never")
			err := serve(append([]string{"-data", never}, tt.args...))
			if err == nil {
				t.Fatal("want error")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("err = %v, want %v", err, tt.is)
			}
			if _, err := os.Stat(never); !os.IsNotExist(err) {
				t.Errorf("data directory created despite bad flags")
			}
		})
	}
}

func TestServeDataDirIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := serve([]string{"-data", filepath.Join(file, "sub"), "-listen", "127.0.0.1:0"})
	if err == nil || !strings.Contains(err.Error(), "data directory") {
		t.Errorf("err = %v, want a data directory error", err)
	}
}

func TestServeBadListenAddress(t *testing.T) {
	integration(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	for _, listen := range []string{"127.0.0.1:notaport", busy.Addr().String()} {
		t.Run(listen, func(t *testing.T) {
			errc := make(chan error, 1)
			go func() { errc <- serve([]string{"-data", t.TempDir(), "-listen", listen}) }()
			select {
			case err := <-errc:
				if err == nil {
					t.Error("want error")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("serve did not fail on an unusable listen address")
			}
		})
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// TestServeRunsAndShutsDownOnSignal starts the real serve command, checks the
// health endpoint and a WebSocket, then stops it with SIGINT as Docker would
// (SIGTERM is handled the same way).
func TestServeRunsAndShutsDownOnSignal(t *testing.T) {
	integration(t)
	data := filepath.Join(t.TempDir(), "nested", "data")
	addr := freeAddr(t)
	t.Setenv("TALESPEAK_DATA", data)
	t.Setenv("TALESPEAK_LISTEN", "127.0.0.1:notaport") // the -listen flag must win

	errc := make(chan error, 1)
	go func() { errc <- run([]string{"serve", "-listen", addr}) }()

	// Poll until healthy. Healthy also means the signal handler is installed,
	// so the SIGINT below cannot kill the test binary.
	url := "http://" + addr + "/healthz"
	deadline := time.After(15 * time.Second)
	for healthcheck(url) != nil {
		select {
		case err := <-errc:
			t.Fatalf("serve exited early: %v", err)
		case <-deadline:
			t.Fatal("serve did not become healthy")
		case <-time.After(20 * time.Millisecond):
		}
	}

	t.Setenv("TALESPEAK_LISTEN", addr)
	if err := run([]string{"healthcheck"}); err != nil {
		t.Errorf("healthcheck command: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "talespeak.db")); err != nil {
		t.Errorf("database not created in TALESPEAK_DATA: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, _, err := c.Read(ctx); err != nil { // welcome
		t.Fatal(err)
	}
	closed := make(chan websocket.StatusCode, 1)
	go func() {
		_, _, err := c.Read(ctx)
		closed <- websocket.CloseStatus(err)
	}()

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("serve after SIGINT: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("serve did not stop after SIGINT")
	}
	if got := <-closed; got != websocket.StatusGoingAway {
		t.Errorf("WebSocket close status on shutdown = %v, want going away", got)
	}
	if healthcheck(url) == nil {
		t.Error("server still answers after shutdown")
	}
}
