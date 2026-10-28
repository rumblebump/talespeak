package mail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// failingOutbox is a memOutbox whose writes fail on request.
type failingOutbox struct {
	memOutbox
	due, del, retry error
}

func (o *failingOutbox) DueMail(ctx context.Context, now time.Time, limit int) ([]store.Mail, error) {
	if o.due != nil {
		return nil, o.due
	}
	return o.memOutbox.DueMail(ctx, now, limit)
}

func (o *failingOutbox) DeleteMail(ctx context.Context, id int64) error {
	if o.del != nil {
		return o.del
	}
	return o.memOutbox.DeleteMail(ctx, id)
}

func (o *failingOutbox) RetryMail(ctx context.Context, id int64, next time.Time) error {
	if o.retry != nil {
		return o.retry
	}
	return o.memOutbox.RetryMail(ctx, id, next)
}

// A store that fails stops the round: a mail sent but not removed, or a
// retry not recorded, would otherwise go again at once in a loop. No log
// line names an address.
func TestOutboxStoreFails(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	down := errors.New("database is locked")
	for _, tc := range []struct {
		name           string
		due, del, retr error // what the store fails with
		sendErr        error
		wantSent       int
		wantTries      int
		wantLog        string
	}{
		{"not read", down, nil, nil, nil, 0, 0, "outbox not read"},
		{"sent, not removed", nil, down, nil, nil, 1, 1, "sent mail not removed"},
		{"retry not recorded", nil, nil, down, errors.New("550 no such user mira@example.org"), 0, 1, "mail retry not recorded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &failingOutbox{due: tc.due, del: tc.del, retry: tc.retr}
			for range 3 {
				st.add(store.Mail{To: "mira@example.org", Next: now, Expires: now.Add(time.Hour)})
			}
			var log bytes.Buffer
			tries := 0
			o := NewOutbox(st, func(context.Context, Message) error { tries++; return tc.sendErr }, func() time.Time { return now },
				slog.New(slog.NewTextHandler(&log, nil)))
			if sent := o.Flush(context.Background()); sent != tc.wantSent || tries != tc.wantTries {
				t.Errorf("sent %d in %d tries, want %d in %d", sent, tries, tc.wantSent, tc.wantTries)
			}
			if !strings.Contains(log.String(), tc.wantLog) || strings.Contains(log.String(), "mira@") {
				t.Errorf("log = %q", log.String())
			}
		})
	}
	// A mail given up that cannot be removed is only logged: the round
	// goes on with the next.
	st := &failingOutbox{del: down}
	st.add(store.Mail{To: "a@example.org", Next: now, Expires: now.Add(time.Hour), Attempts: MaxAttempts - 1})
	st.add(store.Mail{To: "b@example.org", Next: now, Expires: now.Add(time.Hour), Attempts: MaxAttempts - 1})
	tries := 0
	o := NewOutbox(st, func(context.Context, Message) error { tries++; return errors.New("down") }, func() time.Time { return now }, quietLog())
	if sent := o.Flush(context.Background()); sent != 0 || tries != 2 {
		t.Errorf("given up: sent %d in %d tries", sent, tries)
	}
}

// A round sends at most ten batches; the rest waits for the next kick or
// tick. A context that ends stops the round between mails.
func TestOutboxRoundLimits(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	st := &memOutbox{}
	for range 10*batch + 5 {
		st.add(store.Mail{To: "x@example.org", Next: now, Expires: now.Add(time.Hour)})
	}
	o := NewOutbox(st, func(context.Context, Message) error { return nil }, func() time.Time { return now }, quietLog())
	if sent := o.Flush(context.Background()); sent != 10*batch || st.left() != 5 {
		t.Errorf("first round sent %d, left %d", sent, st.left())
	}
	if sent := o.Flush(context.Background()); sent != 5 || st.left() != 0 {
		t.Errorf("second round sent %d, left %d", sent, st.left())
	}

	for range 3 {
		st.add(store.Mail{To: "x@example.org", Next: now, Expires: now.Add(time.Hour)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	o = NewOutbox(st, func(context.Context, Message) error { cancel(); return nil }, func() time.Time { return now }, quietLog())
	if sent := o.Flush(ctx); sent != 1 || st.left() != 2 {
		t.Errorf("cancelled round sent %d, left %d", sent, st.left())
	}
}

// Kick never blocks, also when a round is due already, and a nil Outbox
// (mail off) ignores it.
func TestOutboxKick(t *testing.T) {
	var none *Outbox
	none.Kick()
	o := NewOutbox(&memOutbox{}, nil, nil, nil)
	o.Kick()
	o.Kick()
	if len(o.kick) != 1 {
		t.Errorf("kicks waiting = %d", len(o.kick))
	}
	if o.now == nil || o.log == nil {
		t.Error("NewOutbox left the clock or the log nil")
	}
}
