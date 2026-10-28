package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// TestVerifyMailsLeaveRoomForResets: confirmation links, which anyone may
// ask for, fill at most half the outbox, so reset links still go out.
func TestVerifyMailsLeaveRoomForResets(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	mira := mustAccount(t, s, "Mira", store.RolePlayer)
	for i := range store.MaxVerifyOutbox {
		h := hashOf(fmt.Sprint("v", i))
		if _, err := s.db.ExecContext(ctx, `INSERT INTO account_tokens (token_hash, account_id, kind, email, expires_at) VALUES (?, ?, 'verify', 'x@example.org', ?)`,
			h, mira.ID, t0.Add(time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO mail_outbox (token_hash, to_addr, subject, body, next_at, expires_at) VALUES (?, 'x', 's', 'b', ?, ?)`,
			h, t0.Unix(), t0.Add(time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	m := &store.Mail{To: "x", Subject: "s", Body: "b"}
	verify := store.Token{Hash: hashOf("verify"), Account: ann.ID, Kind: store.TokenVerify, Email: "ann@example.org", Expires: t0.Add(time.Hour)}
	if err := s.CreateToken(ctx, verify, m, t0); !errors.Is(err, store.ErrOutboxFull) {
		t.Fatalf("one confirmation link too many = %v, want ErrOutboxFull", err)
	}
	reset := store.Token{Hash: hashOf("reset"), Account: ann.ID, Kind: store.TokenReset, Expires: t0.Add(time.Hour)}
	if err := s.CreateToken(ctx, reset, m, t0); err != nil {
		t.Fatalf("a reset link with half the outbox free = %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM mail_outbox`); n != store.MaxVerifyOutbox+1 {
		t.Errorf("outbox = %d mails, want %d", n, store.MaxVerifyOutbox+1)
	}
}
