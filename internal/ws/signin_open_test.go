package ws

import (
	"net/http"
	"sync/atomic"
	"testing"
)

// TestSignOutWhileOpening: a sign-in that ends between the check at the
// upgrade and the connection being watched (a SignOut then finds nothing
// to close) still ends the connection, without waiting for a recheck.
func TestSignOutWhileOpening(t *testing.T) {
	integration(t)
	var calls atomic.Int32
	auth := func(*http.Request) (Who, int) {
		if calls.Add(1) == 1 {
			return Who{Account: 7, Login: "a"}, 0
		}
		return Who{}, http.StatusUnauthorized // signed out meanwhile
	}
	_, url := newServer(t, WithGame(newFakeGame()), WithAuth(auth), WithRecheck(0))
	conn := dial(t, url)
	if status := closedWith(t, conn); status != StatusSignedOut {
		t.Errorf("closed with %v, want %v", status, StatusSignedOut)
	}
}
