package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestEmails(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	mira := mustAccount(t, s, "Mira", store.RolePlayer)
	jo := mustAccount(t, s, "Jo", store.RolePlayer)
	if got, err := s.AccountEmail(ctx, mira.ID); err != nil || got != "" {
		t.Fatalf("AccountEmail before = %q, %v", got, err)
	}
	if _, err := s.AccountEmail(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AccountEmail(unknown) = %v", err)
	}
	if err := s.SetEmail(ctx, mira.ID, "Mira@Example.org"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AccountEmail(ctx, mira.ID); got != "Mira@Example.org" {
		t.Fatalf("AccountEmail = %q, want it as typed", got)
	}
	for _, typed := range []string{"mira@example.org", "MIRA@EXAMPLE.ORG"} {
		a, hash, err := s.AccountByEmail(ctx, typed)
		if err != nil || a != mira || hash != "hash-of-Mira" {
			t.Errorf("AccountByEmail(%q) = %+v, %q, %v", typed, a, hash, err)
		}
	}
	if err := s.SetEmail(ctx, jo.ID, "MIRA@example.org"); !errors.Is(err, store.ErrEmailTaken) {
		t.Fatalf("SetEmail(taken) = %v, want ErrEmailTaken", err)
	}
	if err := s.SetEmail(ctx, 9999, "x@example.org"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetEmail(unknown) = %v", err)
	}
	// Removing frees the address; two accounts without one do not clash.
	if err := s.SetEmail(ctx, mira.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AccountByEmail(ctx, "mira@example.org"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AccountByEmail after removing = %v", err)
	}
	if err := s.SetEmail(ctx, jo.ID, "mira@example.org"); err != nil {
		t.Fatalf("SetEmail(freed) = %v", err)
	}
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	mira := mustAccount(t, s, "Mira", store.RolePlayer)
	admin := mustAccount(t, s, "Carl", store.RoleAdmin)
	code := store.Token{Hash: hashOf("code"), Account: mira.ID, Kind: store.TokenCode, CreatedBy: admin.ID, Expires: t0.Add(time.Hour)}
	if err := s.CreateToken(ctx, code, nil, t0); err != nil {
		t.Fatal(err)
	}
	// The wrong kind, an unknown hash and an expired token are not found,
	// and leave the token in place.
	for _, c := range []struct {
		hash  []byte
		kinds []string
		now   time.Time
	}{
		{hashOf("code"), []string{store.TokenVerify}, t0},
		{hashOf("nope"), []string{store.TokenCode}, t0},
		{hashOf("code"), []string{store.TokenCode}, t0.Add(time.Hour)},
	} {
		if _, _, err := s.UseToken(ctx, c.hash, c.kinds, c.now); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UseToken(%v at %v) = %v, want ErrNotFound", c.kinds, c.now, err)
		}
	}
	got, acc, err := s.UseToken(ctx, hashOf("code"), []string{store.TokenReset, store.TokenCode}, t0)
	if err != nil || acc != mira || got.Kind != store.TokenCode || got.CreatedBy != admin.ID || !got.Expires.Equal(code.Expires) {
		t.Fatalf("UseToken = %+v, %+v, %v", got, acc, err)
	}
	if _, _, err := s.UseToken(ctx, hashOf("code"), []string{store.TokenCode}, t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UseToken twice = %v, want ErrNotFound", err)
	}

	// A new token replaces the account's other one of its kind only.
	for _, tk := range []store.Token{
		{Hash: hashOf("v1"), Account: mira.ID, Kind: store.TokenVerify, Email: "a@example.org", Expires: t0.Add(time.Hour)},
		{Hash: hashOf("c1"), Account: mira.ID, Kind: store.TokenCode, Expires: t0.Add(time.Hour)},
		{Hash: hashOf("v2"), Account: mira.ID, Kind: store.TokenVerify, Email: "b@example.org", Expires: t0.Add(time.Hour)},
	} {
		if err := s.CreateToken(ctx, tk, nil, t0); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.UseToken(ctx, hashOf("v1"), []string{store.TokenVerify}, t0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("replaced token still works: %v", err)
	}
	if v, _, err := s.UseToken(ctx, hashOf("v2"), []string{store.TokenVerify}, t0); err != nil || v.Email != "b@example.org" || v.CreatedBy != 0 {
		t.Errorf("UseToken(v2) = %+v, %v", v, err)
	}
	// A password change drops the reset codes and links, not the rest.
	if err := s.CreateToken(ctx, store.Token{Hash: hashOf("v3"), Account: mira.ID, Kind: store.TokenVerify, Expires: t0.Add(time.Hour)}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateToken(ctx, store.Token{Hash: hashOf("r1"), Account: mira.ID, Kind: store.TokenReset, Expires: t0.Add(time.Hour)}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(ctx, mira.ID, "new"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM account_tokens WHERE account_id = ?`, mira.ID); n != 1 {
		t.Errorf("tokens after a password change = %d, want only the verify link", n)
	}
	// Expired tokens of any account go at the next CreateToken.
	if err := s.CreateToken(ctx, store.Token{Hash: hashOf("old"), Account: admin.ID, Kind: store.TokenCode, Expires: t0.Add(time.Minute)}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateToken(ctx, store.Token{Hash: hashOf("j"), Account: mira.ID, Kind: store.TokenCode, Expires: t0.Add(3 * time.Hour)}, nil, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM account_tokens WHERE token_hash IN (?, ?)`, hashOf("old"), hashOf("v3")); n != 0 {
		t.Errorf("expired tokens kept: %d", n)
	}
}

func TestOutbox(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	mira := mustAccount(t, s, "Mira", store.RolePlayer)
	tk := store.Token{Hash: hashOf("r"), Account: mira.ID, Kind: store.TokenReset, Expires: t0.Add(time.Hour)}
	m := &store.Mail{To: "mira@example.org", Subject: "Reset", Body: "link", Expires: t0.Add(48 * time.Hour)}
	if err := s.CreateToken(ctx, tk, m, t0); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueMail(ctx, t0, 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("DueMail = %+v, %v", due, err)
	}
	got := due[0]
	if got.To != m.To || got.Subject != m.Subject || got.Body != m.Body || got.Attempts != 0 || !got.Next.Equal(t0) {
		t.Errorf("mail = %+v", got)
	}
	if !got.Expires.Equal(tk.Expires) {
		t.Errorf("mail expires %v, want with its token at %v", got.Expires, tk.Expires)
	}
	if err := s.RetryMail(ctx, got.ID, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.DueMail(ctx, t0, 10); len(due) != 0 {
		t.Errorf("a mail waiting for its retry is due: %+v", due)
	}
	due, _ = s.DueMail(ctx, t0.Add(time.Minute), 10)
	if len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("after a retry: %+v", due)
	}
	// Using the token takes its mail with it.
	if _, _, err := s.UseToken(ctx, tk.Hash, []string{store.TokenReset}, t0); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM mail_outbox`); n != 0 {
		t.Errorf("mail of a used token kept: %d", n)
	}
	// An expired mail is dropped unsent; DeleteMail removes a sent one.
	for i, h := range []string{"a", "b"} {
		tk := store.Token{Hash: hashOf(h), Account: mira.ID, Kind: []string{store.TokenReset, store.TokenVerify}[i], Expires: t0.Add(time.Hour)}
		if err := s.CreateToken(ctx, tk, &store.Mail{To: "x@example.org", Subject: "s", Body: "b", Expires: t0.Add(time.Duration(i+1) * time.Minute)}, t0); err != nil {
			t.Fatal(err)
		}
	}
	due, _ = s.DueMail(ctx, t0.Add(time.Minute), 10)
	if len(due) != 1 {
		t.Fatalf("due after the first expired = %+v", due)
	}
	if err := s.DeleteMail(ctx, due[0].ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM mail_outbox`); n != 0 {
		t.Errorf("outbox after delete = %d", n)
	}
}

func TestOutboxFull(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	mira := mustAccount(t, s, "Mira", store.RolePlayer)
	for i := range store.MaxOutbox {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO mail_outbox (to_addr, subject, body, next_at, expires_at) VALUES ('x', 's', 'b', ?, ?)`,
			t0.Unix()+int64(i), t0.Add(time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	tk := store.Token{Hash: hashOf("r"), Account: mira.ID, Kind: store.TokenReset, Expires: t0.Add(time.Hour)}
	if err := s.CreateToken(ctx, tk, &store.Mail{To: "x", Subject: "s", Body: "b"}, t0); !errors.Is(err, store.ErrOutboxFull) {
		t.Fatalf("CreateToken with a full outbox = %v, want ErrOutboxFull", err)
	}
	if n := count(t, s, `SELECT count(*) FROM account_tokens`); n != 0 {
		t.Errorf("token kept without its mail: %d", n)
	}
	due, err := s.DueMail(ctx, t0.Add(time.Minute), 5)
	if err != nil || len(due) != 5 || due[0].Next.After(due[4].Next) {
		t.Errorf("DueMail(limit 5) = %d mails, %v", len(due), err)
	}
}

func TestGMOf(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	gm := mustAccount(t, s, "Teacher", store.RolePlayer)
	kid := mustAccount(t, s, "Kid", store.RolePlayer)
	other := mustAccount(t, s, "Other", store.RolePlayer)
	w, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", PackVersion: "1.0.0", Visibility: store.VisibilityPrivate, CreatedBy: gm.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ctx, w.ID, kid.ID, store.MemberPlayer); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		gm, account int64
		want        bool
	}{{gm.ID, kid.ID, true}, {gm.ID, other.ID, false}, {kid.ID, gm.ID, false}} {
		if got, err := s.GMOf(ctx, c.gm, c.account); err != nil || got != c.want {
			t.Errorf("GMOf(%d, %d) = %v, %v, want %v", c.gm, c.account, got, err, c.want)
		}
	}
	if err := s.FinishWorld(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GMOf(ctx, gm.ID, kid.ID); got {
		t.Error("GMOf counts a finished world")
	}
}
