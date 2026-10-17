// Package sqlite is the SQLite backend of store.Store. It uses a pure-Go
// SQLite build, so the binary stays CGO_ENABLED=0.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver" // registers the "sqlite3" driver

	"github.com/rumblebump/talespeak/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is a store.Store backed by one SQLite file.
type Store struct {
	db *sql.DB
}

var _ store.Store = (*Store)(nil)

// Open opens (or creates) the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite: empty path")
	}
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() +
		"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	s := &Store{db: db}
	if err := s.enableWAL(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// enableWAL switches the file to write-ahead logging, which is stored in the
// file itself. busy_timeout does not cover this switch, so two processes
// opening a new file at once retry here instead of failing.
func (s *Store) enableWAL(ctx context.Context) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var mode string
		err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode = wal`).Scan(&mode)
		if err == nil && mode == "wal" {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("journal mode is %q", mode)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("sqlite: enable WAL: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// migrate applies every embedded migration that has not run yet, each in its
// own transaction, in file name order.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	) STRICT`); err != nil {
		return fmt.Errorf("sqlite: migrations table: %w", err)
	}

	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	seen := map[int]string{}
	for _, f := range files {
		version, err := migrationVersion(f)
		if err != nil {
			return err
		}
		if other, dup := seen[version]; dup {
			return fmt.Errorf("sqlite: migrations %s and %s share version %d", other, f, version)
		}
		seen[version] = f
		if err := s.apply(ctx, version, f); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, version int, file string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	body, err := migrations.ReadFile(file)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("sqlite: migration %s: %w", file, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
		return err
	}
	return tx.Commit()
}

// migrationVersion reads 12 from "migrations/0012_name.sql". The number must
// be exactly four digits.
func migrationVersion(file string) (int, error) {
	name := strings.TrimPrefix(file, "migrations/")
	num, _, ok := strings.Cut(name, "_")
	digits := len(num) == 4 && strings.Trim(num, "0123456789") == ""
	v, err := strconv.Atoi(num)
	if !ok || !digits || err != nil || v <= 0 {
		return 0, fmt.Errorf("sqlite: bad migration name %q (want NNNN_name.sql)", name)
	}
	return v, nil
}

// Ping checks that the database answers a query.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}

// Setting returns the value stored for key, or store.ErrNotFound.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return v, err
}

// SetSetting creates or replaces the value for key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value,
		updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`, key, value)
	return err
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}
