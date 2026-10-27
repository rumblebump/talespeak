package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rumblebump/talespeak/internal/ws"
)

// TestServeSignOutEndsGame checks the wiring of serve: signing out in the
// browser ends that sign-in's open game at once (the account service tells
// the WebSocket server), and the other sign-in's game plays on.
func TestServeSignOutEndsGame(t *testing.T) {
	integration(t)
	addr := freeAddr(t)
	t.Setenv("TALESPEAK_DATA", filepath.Join(t.TempDir(), "data"))
	errc := make(chan error, 1)
	go func() { errc <- run([]string{"serve", "-listen", addr}) }()
	deadline := time.After(15 * time.Second)
	for healthcheck("http://"+addr+"/healthz") != nil {
		select {
		case err := <-errc:
			t.Fatalf("serve exited early: %v", err)
		case <-deadline:
			t.Fatal("serve did not become healthy")
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer func() {
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		<-errc
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first := signUp(t, addr, "mira")
	resp, err := http.Post("http://"+addr+"/api/login", "application/json", strings.NewReader(`{"name":"mira","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	second := http.Header{}
	second.Set("Cookie", resp.Cookies()[0].String())

	// play opens a game and reports how it closes.
	play := func(h http.Header) <-chan websocket.StatusCode {
		c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws?world=1", &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.CloseNow() })
		if _, _, err := c.Read(ctx); err != nil { // welcome
			t.Fatal(err)
		}
		closed := make(chan websocket.StatusCode, 1)
		go func() {
			for {
				if _, _, err := c.Read(ctx); err != nil {
					closed <- websocket.CloseStatus(err)
					return
				}
			}
		}()
		return closed
	}
	firstGame, secondGame := play(first), play(second)

	req, _ := http.NewRequest("POST", "http://"+addr+"/api/logout", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", first.Get("Cookie"))
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("logout: %v %v", resp, err)
	}
	resp.Body.Close()
	select {
	case got := <-firstGame:
		if got != ws.StatusSignedOut {
			t.Errorf("signed-out game closed with %v, want %v", got, ws.StatusSignedOut)
		}
	case <-ctx.Done():
		t.Fatal("the signed-out game stays open")
	}
	select {
	case got := <-secondGame:
		t.Errorf("the other sign-in's game closed: %v", got)
	default:
	}
}
