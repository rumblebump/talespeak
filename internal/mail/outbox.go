package mail

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// Outbox timing.
const (
	// PollEvery is how often the outbox looks for mail that is due when
	// nobody kicks it: retries and mails a restart left behind.
	PollEvery = 30 * time.Second
	// FirstRetry is the wait after the first failed try; it doubles with
	// every try up to MaxRetryWait.
	FirstRetry   = time.Minute
	MaxRetryWait = 30 * time.Minute
	// MaxAttempts is how many tries a mail gets before it is given up.
	// Most mails expire with their link (one hour) before that.
	MaxAttempts = 8
	// batch is how many mails one round sends at most.
	batch = 20
)

// Sender delivers one mail; Send with a Config is the real one.
type Sender func(ctx context.Context, m Message) error

// Outbox sends the mails waiting in the store, one at a time, outside any
// request: Kick after adding one, and Run sends it soon.
type Outbox struct {
	st   store.Outbox
	send Sender
	now  func() time.Time
	log  *slog.Logger
	kick chan struct{}
}

// NewOutbox returns an Outbox that sends with send. now is the clock (nil
// is time.Now).
func NewOutbox(st store.Outbox, send Sender, now func() time.Time, log *slog.Logger) *Outbox {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Outbox{st: st, send: send, now: now, log: log, kick: make(chan struct{}, 1)}
}

// Kick tells Run that a mail is waiting. It never blocks; a nil Outbox
// ignores it.
func (o *Outbox) Kick() {
	if o == nil {
		return
	}
	select {
	case o.kick <- struct{}{}:
	default: // a round is due already
	}
}

// Run sends mail as it comes until ctx ends.
func (o *Outbox) Run(ctx context.Context) {
	t := time.NewTicker(PollEvery)
	defer t.Stop()
	for {
		o.Flush(ctx)
		select {
		case <-ctx.Done():
			return
		case <-o.kick:
		case <-t.C:
		}
	}
}

// Flush sends the mails due now and reports how many went. A mail that
// fails waits for its next try, longer after each, and is given up after
// MaxAttempts. Addresses are never logged, only the mail's number.
func (o *Outbox) Flush(ctx context.Context) int {
	sent := 0
	for round := 0; round < 10; round++ { // more than this waits for the next kick or tick
		due, err := o.st.DueMail(ctx, o.now(), batch)
		if err != nil {
			o.log.Error("outbox not read", "err", err)
			return sent
		}
		for _, m := range due {
			if ctx.Err() != nil {
				return sent
			}
			err := o.send(ctx, Message{To: m.To, Subject: m.Subject, Body: m.Body})
			why := scrub(err, m.To) // a server's answer often names the address
			switch {
			case err == nil:
				sent++
				o.log.Info("mail sent", "mail", m.ID)
				if err := o.st.DeleteMail(ctx, m.ID); err != nil {
					o.log.Error("sent mail not removed from the outbox", "mail", m.ID, "err", err)
					return sent // else it would go again at once
				}
			case m.Attempts+1 >= MaxAttempts:
				o.log.Warn("mail given up", "mail", m.ID, "attempts", m.Attempts+1, "err", why)
				if err := o.st.DeleteMail(ctx, m.ID); err != nil {
					o.log.Error("mail not removed from the outbox", "mail", m.ID, "err", err)
				}
			default:
				wait := min(FirstRetry<<m.Attempts, MaxRetryWait)
				o.log.Warn("mail not sent, trying again later", "mail", m.ID, "attempt", m.Attempts+1, "in", wait, "err", why)
				if err := o.st.RetryMail(ctx, m.ID, o.now().Add(wait)); err != nil {
					o.log.Error("mail retry not recorded", "mail", m.ID, "err", err)
					return sent
				}
			}
		}
		if len(due) < batch {
			return sent
		}
	}
	return sent
}

// scrub is err's text without the address to, which is never logged.
func scrub(err error, to string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if to == "" {
		return msg
	}
	// Lower case in ASCII only, byte by byte, so both keep their offsets.
	ascii := func(s string) string {
		b := []byte(s)
		for i, c := range b {
			if c >= 'A' && c <= 'Z' {
				b[i] = c + 'a' - 'A'
			}
		}
		return string(b)
	}
	lower, key := ascii(msg), ascii(to)
	var b strings.Builder
	for {
		i := strings.Index(lower, key)
		if i < 0 {
			b.WriteString(msg)
			return b.String()
		}
		b.WriteString(msg[:i] + "[address]")
		msg, lower = msg[i+len(key):], lower[i+len(key):]
	}
}
