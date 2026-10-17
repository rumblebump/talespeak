package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
)

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "t.db"))

	if _, err := s.Setting(ctx, "mode"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing key: err = %v, want ErrNotFound", err)
	}
	for _, v := range []string{"edu", "public", ""} {
		if err := s.SetSetting(ctx, "mode", v); err != nil {
			t.Fatal(err)
		}
		got, err := s.Setting(ctx, "mode")
		if err != nil || got != v {
			t.Fatalf("Setting = %q, %v; want %q", got, err, v)
		}
	}
}

func TestReopenKeepsDataAndSkipsMigrations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2 := open(t, path)
	if got, err := s2.Setting(ctx, "k"); err != nil || got != "v" {
		t.Fatalf("after reopen: %q, %v", got, err)
	}
	var n int
	if err := s2.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("schema_migrations rows = %d, want 1", n)
	}
}

func TestPingAndPragmas(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "with space.db"))
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v; want wal", mode, err)
	}
	var fk int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d, %v; want 1", fk, err)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Error("empty path: want error")
	}
	if _, err := Open(context.Background(), filepath.Join(t.TempDir(), "missing-dir", "t.db")); err == nil {
		t.Error("missing directory: want error")
	}
}

func TestMigrationVersion(t *testing.T) {
	tests := []struct {
		file string
		want int
		ok   bool
	}{
		{"migrations/0001_settings.sql", 1, true},
		{"migrations/0012_x_y.sql", 12, true},
		{"migrations/settings.sql", 0, false},
		{"migrations/abcd_x.sql", 0, false},
		{"migrations/0000_x.sql", 0, false},
	}
	for _, tt := range tests {
		got, err := migrationVersion(tt.file)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("migrationVersion(%q) = %d, %v", tt.file, got, err)
		}
	}
}
