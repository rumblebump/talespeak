package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"

	"github.com/rumblebump/talespeak/internal/store"
)

// nameKey is what account names are unique by: the name in lower case.
func nameKey(name string) string { return strings.ToLower(name) }

// CreateAccount adds an account, or returns store.ErrNameTaken.
func (s *Store) CreateAccount(ctx context.Context, name, passwordHash, role string) (store.Account, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO accounts (name, name_key, password_hash, role) VALUES (?, ?, ?, ?)`,
		name, nameKey(name), passwordHash, role)
	if isUnique(err) {
		return store.Account{}, store.ErrNameTaken
	}
	if err != nil {
		return store.Account{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return store.Account{}, err
	}
	return store.Account{ID: id, Name: name, Role: role}, nil
}

// isUnique reports whether err is a UNIQUE or PRIMARY KEY constraint
// failure.
func isUnique(err error) bool {
	var e *sqlite3.Error
	return errors.As(err, &e) && (e.ExtendedCode() == sqlite3.CONSTRAINT_UNIQUE || e.ExtendedCode() == sqlite3.CONSTRAINT_PRIMARYKEY)
}

// AccountByName returns the account named name in any letter case and its
// password hash, or store.ErrNotFound.
func (s *Store) AccountByName(ctx context.Context, name string) (store.Account, string, error) {
	var a store.Account
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, role, password_hash FROM accounts WHERE name_key = ?`, nameKey(name)).
		Scan(&a.ID, &a.Name, &a.Role, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Account{}, "", store.ErrNotFound
	}
	return a, hash, err
}

// SetPassword replaces the account's password hash, ends all its sign-in
// sessions and drops its reset codes and links.
func (s *Store) SetPassword(ctx context.Context, account int64, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE accounts SET password_hash = ? WHERE id = ?`, passwordHash, account)
	if err != nil {
		return err
	}
	if err := oneRow(res); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM logins WHERE account_id = ?`, account); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_tokens WHERE account_id = ? AND kind IN ('code', 'reset')`, account); err != nil {
		return err
	}
	return tx.Commit()
}

// SetRole changes the account's role.
func (s *Store) SetRole(ctx context.Context, account int64, role string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE accounts SET role = ? WHERE id = ?`, role, account)
	if err != nil {
		return err
	}
	return oneRow(res)
}

// oneRow turns an update that changed nothing into store.ErrNotFound.
func oneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// CreateLogin stores a sign-in session, drops those expired at now, and
// keeps at most keep sessions of the account: the new one and the newest
// others.
func (s *Store) CreateLogin(ctx context.Context, tokenHash []byte, account int64, now, expires time.Time, keep int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM logins WHERE expires_at <= ?`, now.Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO logins (token_hash, account_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash, account, expires.Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM logins WHERE account_id = ? AND token_hash != ? AND token_hash NOT IN
		(SELECT token_hash FROM logins WHERE account_id = ? AND token_hash != ? ORDER BY expires_at DESC, token_hash LIMIT ?)`,
		account, tokenHash, account, tokenHash, max(keep-1, 0)); err != nil {
		return err
	}
	return tx.Commit()
}

// LoginAccount returns the account of an unexpired session, or
// store.ErrNotFound.
func (s *Store) LoginAccount(ctx context.Context, tokenHash []byte, now time.Time) (store.Account, error) {
	var a store.Account
	err := s.db.QueryRowContext(ctx, `SELECT a.id, a.name, a.role FROM logins l JOIN accounts a ON a.id = l.account_id
		WHERE l.token_hash = ? AND l.expires_at > ?`, tokenHash, now.Unix()).Scan(&a.ID, &a.Name, &a.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Account{}, store.ErrNotFound
	}
	return a, err
}

// DeleteLogin ends a sign-in session.
func (s *Store) DeleteLogin(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM logins WHERE token_hash = ?`, tokenHash)
	return err
}
