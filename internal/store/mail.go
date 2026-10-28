package store

import (
	"context"
	"errors"
	"time"
)

// Email addresses, one-time codes and the mail outbox (T-0013).

// Token kinds.
const (
	TokenCode   = "code"   // a reset code an admin or game master made
	TokenReset  = "reset"  // a reset link sent by mail
	TokenVerify = "verify" // a link that confirms an email address
)

// Token is a one-time code for an account. Only a hash of it is stored.
type Token struct {
	Hash      []byte
	Account   int64
	Kind      string // TokenCode, TokenReset or TokenVerify
	Email     string // TokenVerify: the address it confirms
	CreatedBy int64  // the admin or game master who made it; 0 when the account asked itself
	Expires   time.Time
}

// Mail is a mail in the outbox.
type Mail struct {
	ID       int64
	To       string
	Subject  string
	Body     string
	Attempts int       // tries that failed so far
	Next     time.Time // when to try next
	Expires  time.Time // when it is dropped unsent
}

// MaxOutbox is how many mails may wait in the outbox, so a flood of
// requests cannot fill the disk while the mail server is down.
const MaxOutbox = 1000

// MaxVerifyOutbox is how many of them may be links that confirm an
// address: anyone may ask for those, so the rest stays free for reset
// links.
const MaxVerifyOutbox = MaxOutbox / 2

var (
	// ErrEmailTaken refuses an address another account has confirmed.
	ErrEmailTaken = errors.New("store: email address taken")
	// ErrOutboxFull refuses a mail past MaxOutbox (MaxVerifyOutbox for
	// a confirmation link).
	ErrOutboxFull = errors.New("store: outbox full")
)

// Emails are the accounts' confirmed email addresses.
type Emails interface {
	// AccountEmail returns the account's confirmed address, "" when it
	// has none, or ErrNotFound.
	AccountEmail(ctx context.Context, account int64) (string, error)
	// AccountByEmail returns the account that confirmed email (in any
	// letter case) and its password hash, or ErrNotFound.
	AccountByEmail(ctx context.Context, email string) (Account, string, error)
	// SetEmail sets the account's confirmed address ("" removes it), or
	// returns ErrEmailTaken.
	SetEmail(ctx context.Context, account int64, email string) error
}

// Tokens are one-time codes.
type Tokens interface {
	// CreateToken stores t in place of the account's other token of its
	// kind, and drops the tokens expired at now. With m, the mail goes
	// into the outbox in the same transaction and goes with t; it returns
	// ErrOutboxFull when MaxOutbox mails wait already.
	CreateToken(ctx context.Context, t Token, m *Mail, now time.Time) error
	// UseToken deletes the token with hash when it is of one of kinds and
	// unexpired at now, and returns it with its account; else ErrNotFound.
	UseToken(ctx context.Context, hash []byte, kinds []string, now time.Time) (Token, Account, error)
}

// Outbox is the mail waiting to be sent.
type Outbox interface {
	// DueMail drops the mails expired at now and returns at most limit
	// of those due, the oldest first.
	DueMail(ctx context.Context, now time.Time, limit int) ([]Mail, error)
	// DeleteMail removes a mail: it was sent, or it is given up.
	DeleteMail(ctx context.Context, id int64) error
	// RetryMail counts a failed try of a mail and sets when to try next.
	RetryMail(ctx context.Context, id int64, next time.Time) error
}
