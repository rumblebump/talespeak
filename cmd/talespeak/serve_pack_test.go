package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// serve stops at startup, before listening, when the limits or the pack
// are wrong, and says which.
func TestServeRefusesBadConfig(t *testing.T) {
	integration(t)
	missing := filepath.Join(t.TempDir(), "no-such-pack")
	badPack := t.TempDir()
	if err := os.WriteFile(filepath.Join(badPack, "pack.yaml"), []byte("id: BAD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"bad host", map[string]string{"TALESPEAK_HOSTS": "https://a.example"}, []string{"TALESPEAK_HOSTS:", "https://a.example"}},
		{"bad limit", map[string]string{"TALESPEAK_LUA_TIME": "forever"}, []string{"limits:", `TALESPEAK_LUA_TIME="forever"`}},
		{"two bad limits", map[string]string{"TALESPEAK_MAX_CONNS": "0", "TALESPEAK_MESSAGE_RATE": "x"}, []string{"TALESPEAK_MAX_CONNS=", "TALESPEAK_MESSAGE_RATE="}},
		{"missing pack folder", map[string]string{"TALESPEAK_PACK": missing}, []string{"pack " + missing, "pack.yaml"}},
		{"broken pack", map[string]string{"TALESPEAK_PACK": badPack}, []string{"pack " + badPack, "pack.yaml"}},
		// The demo's images are about 2.5 KiB each.
		{"image limit below the demo's images", map[string]string{"TALESPEAK_PACK_IMAGE_MAX": "1KiB", "TALESPEAK_PACK_IMAGES_TOTAL": "1KiB"}, []string{"demo pack", "assets/wall-map-", "larger than 1024 bytes"}},
		{"images total below the demo's", map[string]string{"TALESPEAK_PACK_IMAGE_MAX": "3KiB", "TALESPEAK_PACK_IMAGES_TOTAL": "4KiB"}, []string{"demo pack", "more than 4096 bytes in all"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			addr := freeAddr(t)
			errc := make(chan error, 1)
			go func() { errc <- serve([]string{"-data", t.TempDir(), "-listen", addr}) }()
			var err error
			select {
			case err = <-errc:
			case <-time.After(10 * time.Second):
				t.Fatal("serve did not stop on a bad configuration")
			}
			if err == nil {
				t.Fatal("serve = nil error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// TALESPEAK_PACK runs that folder's main world instead of the demo's, and
// its images are served under /packs/<id>/1.0.0/assets/.
func TestServeRunsTalespeakPack(t *testing.T) {
	integration(t)
	dir, err := filepath.Abs("../../content/test-pack")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALESPEAK_PACK", dir)
	addr := freeAddr(t)
	errc := make(chan error, 1)
	go func() { errc <- serve([]string{"-data", t.TempDir(), "-listen", addr}) }()

	// Healthy also means the signal handler is installed (see
	// TestServeRunsAndShutsDownOnSignal).
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

	resp, err := http.Get("http://" + addr + "/packs/test-pack/1.0.0/assets/square.png")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(string(body), "\x89PNG") {
		t.Errorf("square.png = %d %q, %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	}
	// The demo stays installed, for new worlds.
	if resp, err := http.Get("http://" + addr + "/packs/demo/1.1.0/assets/wall-map-clear.png"); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("the demo's image next to TALESPEAK_PACK: %v %v", resp.Status, err)
	} else {
		resp.Body.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws?world=1", &websocket.DialOptions{HTTPHeader: signUp(t, addr, "mira")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, _, err := c.Read(ctx); err != nil { // welcome
		t.Fatal(err)
	}
	if _, data, err := c.Read(ctx); err != nil || !strings.HasPrefix(string(data), `{"type":"room","id":"hall"`) {
		t.Fatalf("after welcome: %s, %v; want the test pack's hall", data, err)
	}
	c.Close(websocket.StatusNormalClosure, "")

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
}
