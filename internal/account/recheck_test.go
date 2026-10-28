package account

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The WebSocket's recheck (ws.WithRecheck) against the real sign-in check
// (Service.Auth): changes no sign-out reaches, made in the database by
// another process (the command line, a second server), close the game
// connections they concern, and only those.

// idleGame is a ws.Game whose sessions answer nothing.
type idleGame struct{}

type idleSession struct{}

func (idleGame) NewSession(string, ws.Who) (ws.Session, []ws.Message) { return idleSession{}, nil }
func (idleSession) Handle(ws.Message) []ws.Message                    { return nil }
func (idleSession) Close()                                            {}

// recheckServer serves the WebSocket with f's sign-in check, checked
// again every few milliseconds, and returns its ws:// URL.
func recheckServer(t *testing.T, f *fixture) string {
	t.Helper()
	srv := ws.New("test", quiet, ws.WithGame(idleGame{}), ws.WithAuth(f.svc.Auth), ws.WithRecheck(10*time.Millisecond))
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return "ws" + strings.TrimPrefix(hs.URL, "http")
}

// play opens b's game connection to path and returns a channel with the
// status it closes with.
func (b *browser) play(t *testing.T, url, path string) <-chan websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	h := http.Header{}
	h.Set("Cookie", b.cookie.String())
	c, _, err := websocket.Dial(ctx, url+path, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
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

func closesWith(t *testing.T, what string, c <-chan websocket.StatusCode, want websocket.StatusCode) {
	t.Helper()
	select {
	case got := <-c:
		if got != want {
			t.Errorf("%s closed with %v, want %v", what, got, want)
		}
	case <-time.After(10 * time.Second):
		t.Errorf("%s stays open", what)
	}
}

func staysOpen(t *testing.T, what string, c <-chan websocket.StatusCode) {
	t.Helper()
	select {
	case got := <-c:
		t.Errorf("%s closed: %v", what, got)
	case <-time.After(100 * time.Millisecond): // ten rechecks
	}
}

func TestRecheckEndsChangedSignIns(t *testing.T) {
	f := newFixture(t, Config{})
	url := recheckServer(t, f)
	ctx := context.Background()
	ann, bob, cy := f.signUp("ann"), f.signUp("bob"), f.signUp("cyra")
	made, _ := ann.create(`{"pack":"demo","visibility":"private"}`)
	world := f.ws(made.World.ID)
	for _, b := range []*browser{bob, cy} {
		if code := b.post("/api/join", `{"code":"`+made.Invite.Code+`"}`, nil); code != 200 {
			t.Fatal(code)
		}
	}
	annGame, bobGame, cyGame, cyMain := ann.play(t, url, world), bob.play(t, url, world), cy.play(t, url, world), cy.play(t, url, f.ws(0))
	staysOpen(t, "an unchanged sign-in", annGame)

	// A password set elsewhere ends every sign-in of the account.
	hash, err := Hash(ctx, "set elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetPassword(ctx, accountID(t, f, "bob"), hash); err != nil {
		t.Fatal(err)
	}
	closesWith(t, "bob's game after a new password", bobGame, ws.StatusSignedOut)

	// Taken out of the private world elsewhere: that game ends, the main
	// world's plays on.
	if _, err := f.st.RemoveMember(ctx, made.World.ID, accountID(t, f, "cyra")); err != nil {
		t.Fatal(err)
	}
	closesWith(t, "cy's game in a world she left", cyGame, ws.StatusSignedOut)
	staysOpen(t, "cy's game in the main world", cyMain)

	// Demoted elsewhere: the game master's game ends, to come back as a
	// player.
	if err := f.st.AddMember(ctx, made.World.ID, accountID(t, f, "ann"), store.MemberPlayer); err != nil {
		t.Fatal(err)
	}
	closesWith(t, "ann's game after a demotion", annGame, ws.StatusSignedOut)
	again := ann.play(t, url, world)
	staysOpen(t, "ann as a player", again)
}
