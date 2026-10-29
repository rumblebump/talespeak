package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/mail/mailtest"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
)

// memOutbox is store.Outbox in memory.
type memOutbox struct {
	mu    sync.Mutex
	mails map[int64]*store.Mail
}

func (o *memOutbox) add(m store.Mail) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.mails == nil {
		o.mails = map[int64]*store.Mail{}
	}
	m.ID = int64(len(o.mails) + 1)
	o.mails[m.ID] = &m
}

func (o *memOutbox) DueMail(_ context.Context, now time.Time, limit int) ([]store.Mail, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var due []store.Mail
	for id, m := range o.mails {
		if !m.Expires.After(now) {
			delete(o.mails, id)
		} else if !m.Next.After(now) && len(due) < limit {
			due = append(due, *m)
		}
	}
	return due, nil
}

func (o *memOutbox) DeleteMail(_ context.Context, id int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.mails, id)
	return nil
}

func (o *memOutbox) RetryMail(_ context.Context, id int64, next time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if m := o.mails[id]; m != nil {
		m.Attempts++
		m.Next = next
	}
	return nil
}

func (o *memOutbox) left() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.mails)
}

func TestOutboxRetries(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	st := &memOutbox{}
	st.add(store.Mail{To: "mira@example.org", Subject: "s", Body: "b", Next: now, Expires: now.Add(24 * time.Hour)})
	var logs bytes.Buffer
	failing := true
	tries := 0
	o := NewOutbox(st, func(context.Context, Message) error {
		tries++
		if failing {
			return errors.New("550 <MIRA@example.org>: try later")
		}
		return nil
	}, func() time.Time { return now }, slog.New(slog.NewTextHandler(&logs, nil)))
	ctx := context.Background()

	// Each failure waits twice as long as the one before.
	for i, wait := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		if sent := o.Flush(ctx); sent != 0 || tries != i+1 {
			t.Fatalf("try %d: sent %d, tries %d", i+1, sent, tries)
		}
		if o.Flush(ctx); tries != i+1 {
			t.Fatalf("a mail waiting for its retry went again")
		}
		now = now.Add(wait)
	}
	failing = false
	if sent := o.Flush(ctx); sent != 1 || st.left() != 0 {
		t.Fatalf("after the server recovered: sent %d, left %d", sent, st.left())
	}
	if strings.Contains(strings.ToLower(logs.String()), "mira@example.org") {
		t.Errorf("the address reached the log:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "[address]") {
		t.Errorf("the server's answer is missing from the log:\n%s", logs.String())
	}
}

func TestOutboxGivesUp(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	st := &memOutbox{}
	st.add(store.Mail{To: "mira@example.org", Next: now, Expires: now.Add(30 * 24 * time.Hour)})
	tries := 0
	o := NewOutbox(st, func(context.Context, Message) error { tries++; return errors.New("down") }, func() time.Time { return now }, quietLog())
	for range MaxAttempts + 2 {
		o.Flush(context.Background())
		now = now.Add(MaxRetryWait)
	}
	if tries != MaxAttempts || st.left() != 0 {
		t.Fatalf("tries %d, left %d; want %d tries, then given up", tries, st.left(), MaxAttempts)
	}
}

func TestOutboxDropsExpired(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	st := &memOutbox{}
	st.add(store.Mail{To: "mira@example.org", Next: now, Expires: now.Add(time.Hour)})
	o := NewOutbox(st, func(context.Context, Message) error { t.Error("an expired mail went out"); return nil }, func() time.Time { return now.Add(time.Hour) }, quietLog())
	if sent := o.Flush(context.Background()); sent != 0 || st.left() != 0 {
		t.Fatalf("sent %d, left %d", sent, st.left())
	}
}

// TestOutboxSQLite sends mails from a real store through the fake SMTP
// server with Run: one waiting at the start, one announced with Kick.
func TestOutboxSQLite(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real SQLite file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := db.CreateAccount(ctx, "Mira", "h", store.RolePlayer)
	if err != nil {
		t.Fatal(err)
	}
	queue := func(kind string) {
		t.Helper()
		now := time.Now()
		tk := store.Token{Hash: []byte(kind + strings.Repeat("x", 31)), Account: a.ID, Kind: kind, Expires: now.Add(time.Hour)}
		if err := db.CreateToken(ctx, tk, &store.Mail{To: "mira@example.org", Subject: kind, Body: "b"}, now); err != nil {
			t.Fatal(err)
		}
	}
	srv := mailtest.Start(t, mailtest.Options{User: "talespeak", Password: "secret"})
	cfg := config(srv, TLSStart)
	sent := make(chan error, 2)
	o := NewOutbox(db, func(ctx context.Context, m Message) error {
		err := Send(ctx, cfg, m)
		sent <- err
		return err
	}, nil, quietLog())
	queue(store.TokenReset)
	done := make(chan struct{})
	go func() { o.Run(ctx); close(done) }()
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	queue(store.TokenVerify)
	o.Kick()
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if got := srv.Mails(); len(got) != 2 {
		t.Fatalf("mails = %d", len(got))
	}
	due, err := db.DueMail(context.Background(), time.Now().Add(time.Hour), 10)
	if err != nil || len(due) != 0 {
		t.Errorf("outbox after sending = %+v, %v", due, err)
	}
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
