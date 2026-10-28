package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// Email addresses, one-time codes and the mail outbox (T-0013).

// emailKey is what email addresses are unique by: the address in lower
// case.
func emailKey(email string) string { return strings.ToLower(email) }

// AccountEmail returns the account's confirmed address, "" when it has
// none, or store.ErrNotFound.
func (s *Store) AccountEmail(ctx context.Context, account int64) (string, error) {
	var email sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT email FROM accounts WHERE id = ?`, account).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return email.String, err
}

// AccountByEmail returns the account that confirmed email and its
// password hash, or store.ErrNotFound.
func (s *Store) AccountByEmail(ctx context.Context, email string) (store.Account, string, error) {
	var a store.Account
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, role, password_hash FROM accounts WHERE email_key = ?`, emailKey(email)).
		Scan(&a.ID, &a.Name, &a.Role, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Account{}, "", store.ErrNotFound
	}
	return a, hash, err
}

// SetEmail sets the account's confirmed address ("" removes it), or
// returns store.ErrEmailTaken.
func (s *Store) SetEmail(ctx context.Context, account int64, email string) error {
	var addr, key any // NULL removes it
	if email != "" {
		addr, key = email, emailKey(email)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE accounts SET email = ?, email_key = ? WHERE id = ?`, addr, key, account)
	if isUnique(err) {
		return store.ErrEmailTaken
	}
	if err != nil {
		return err
	}
	return oneRow(res)
}

// GMOf reports whether gm is a game master of an unfinished world that
// account is a member of.
func (s *Store) GMOf(ctx context.Context, gm, account int64) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM world_members g
		JOIN world_members m ON m.world_id = g.world_id JOIN worlds w ON w.id = g.world_id
		WHERE g.account_id = ? AND g.role = 'gm' AND m.account_id = ? AND w.finished_at IS NULL)`, gm, account).Scan(&ok)
	return ok, err
}

// CreateToken stores t in place of the account's other token of its kind,
// drops the tokens expired at now, and with m puts the mail into the
// outbox, or returns store.ErrOutboxFull.
func (s *Store) CreateToken(ctx context.Context, t store.Token, m *store.Mail, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Their mails go with them (ON DELETE CASCADE).
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_tokens WHERE expires_at <= ? OR (account_id = ? AND kind = ?)`,
		now.Unix(), t.Account, t.Kind); err != nil {
		return err
	}
	var by any // NULL: the account asked itself
	if t.CreatedBy != 0 {
		by = t.CreatedBy
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_tokens (token_hash, account_id, kind, email, created_by, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`, t.Hash, t.Account, t.Kind, t.Email, by, t.Expires.Unix()); err != nil {
		return err
	}
	if m != nil {
		mail := *m
		if mail.Expires.IsZero() || mail.Expires.After(t.Expires) {
			mail.Expires = t.Expires // a link never arrives after it stopped working
		}
		if err := addMail(ctx, tx, mail, t.Hash, t.Kind, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// addMail puts m into the outbox, tied to the token with hash (nil for
// none) of kind: it goes when the token goes.
func addMail(ctx context.Context, tx *sql.Tx, m store.Mail, hash []byte, kind string, now time.Time) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mail_outbox`).Scan(&n); err != nil {
		return err
	}
	if n >= store.MaxOutbox {
		return store.ErrOutboxFull
	}
	if kind == store.TokenVerify {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mail_outbox o JOIN account_tokens t ON t.token_hash = o.token_hash
			WHERE t.kind = ?`, store.TokenVerify).Scan(&n); err != nil {
			return err
		}
		if n >= store.MaxVerifyOutbox {
			return store.ErrOutboxFull
		}
	}
	next := m.Next
	if next.IsZero() {
		next = now
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO mail_outbox (token_hash, to_addr, subject, body, next_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`, hash, m.To, m.Subject, m.Body, next.Unix(), m.Expires.Unix())
	return err
}

// UseToken deletes and returns the unexpired token with hash of one of
// kinds, with its account, or returns store.ErrNotFound.
func (s *Store) UseToken(ctx context.Context, hash []byte, kinds []string, now time.Time) (store.Token, store.Account, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Token{}, store.Account{}, err
	}
	defer tx.Rollback()
	var t store.Token
	var a store.Account
	var by sql.NullInt64
	var expires int64
	err = tx.QueryRowContext(ctx, `SELECT t.account_id, t.kind, t.email, t.created_by, t.expires_at, a.name, a.role
		FROM account_tokens t JOIN accounts a ON a.id = t.account_id WHERE t.token_hash = ?`, hash).
		Scan(&t.Account, &t.Kind, &t.Email, &by, &expires, &a.Name, &a.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Token{}, store.Account{}, store.ErrNotFound
	}
	if err != nil {
		return store.Token{}, store.Account{}, err
	}
	known := false
	for _, k := range kinds {
		known = known || k == t.Kind
	}
	if !known || expires <= now.Unix() {
		return store.Token{}, store.Account{}, store.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_tokens WHERE token_hash = ?`, hash); err != nil {
		return store.Token{}, store.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.Token{}, store.Account{}, err
	}
	t.Hash, t.CreatedBy, t.Expires = hash, by.Int64, time.Unix(expires, 0)
	a.ID = t.Account
	return t, a, nil
}

// DueMail drops the mails expired at now and returns at most limit of
// those due, the oldest first.
func (s *Store) DueMail(ctx context.Context, now time.Time, limit int) ([]store.Mail, error) {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM mail_outbox WHERE expires_at <= ?`, now.Unix()); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, to_addr, subject, body, attempts, next_at, expires_at FROM mail_outbox
		WHERE next_at <= ? ORDER BY next_at, id LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []store.Mail
	for rows.Next() {
		var m store.Mail
		var next, expires int64
		if err := rows.Scan(&m.ID, &m.To, &m.Subject, &m.Body, &m.Attempts, &next, &expires); err != nil {
			return nil, err
		}
		m.Next, m.Expires = time.Unix(next, 0), time.Unix(expires, 0)
		list = append(list, m)
	}
	return list, rows.Err()
}

// DeleteMail removes a mail from the outbox.
func (s *Store) DeleteMail(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mail_outbox WHERE id = ?`, id)
	return err
}

// RetryMail counts a failed try and sets when to try next.
func (s *Store) RetryMail(ctx context.Context, id int64, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE mail_outbox SET attempts = attempts + 1, next_at = ? WHERE id = ?`, next.Unix(), id)
	return err
}
