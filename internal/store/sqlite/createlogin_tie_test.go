package sqlite

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// The session CreateLogin just made survives its own pruning, also when
// the account's other sessions expire in the same second.
func TestCreateLoginKeepsTheNewSession(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	exp := time.Now().Add(30 * 24 * time.Hour)
	for i := range 10 {
		if err := s.CreateLogin(ctx, bytes.Repeat([]byte{byte(0x80 + i)}, 32), a.ID, time.Now(), exp, 10); err != nil {
			t.Fatal(err)
		}
	}
	newest := bytes.Repeat([]byte{0x01}, 32)
	if err := s.CreateLogin(ctx, newest, a.ID, time.Now(), exp, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginAccount(ctx, newest, time.Now()); err != nil {
		t.Errorf("the session just made was pruned: %v", err)
	}
}
