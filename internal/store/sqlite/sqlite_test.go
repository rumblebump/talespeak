package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// integration skips tests that use a real SQLite file under -short.
func integration(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: real SQLite file")
	}
}

func open(t *testing.T, path string) *Store {
	t.Helper()
	integration(t)
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
	integration(t)
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
	integration(t)
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

func TestConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "t.db"))

	const writers, writes = 16, 40
	var wg sync.WaitGroup
	errs := make(chan error, writers*writes*3)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range writes {
				key := fmt.Sprintf("w%d", w)
				if err := s.SetSetting(ctx, key, strconv.Itoa(i)); err != nil {
					errs <- fmt.Errorf("SetSetting %s: %w", key, err)
				}
				if err := s.SetSetting(ctx, "shared", key); err != nil {
					errs <- fmt.Errorf("SetSetting shared: %w", err)
				}
				if _, err := s.Setting(ctx, key); err != nil {
					errs <- fmt.Errorf("Setting %s: %w", key, err)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err) // SQLITE_BUSY here means busy_timeout or WAL is not in effect
	}

	for w := range writers {
		got, err := s.Setting(ctx, fmt.Sprintf("w%d", w))
		if err != nil || got != strconv.Itoa(writes-1) {
			t.Errorf("w%d = %q, %v; want %d", w, got, err, writes-1)
		}
	}
	got, err := s.Setting(ctx, "shared")
	if err != nil || !strings.HasPrefix(got, "w") {
		t.Errorf("shared = %q, %v", got, err)
	}
}

func TestTwoStoresOnOneFile(t *testing.T) {
	// Two handles on one file stand in for two processes (e.g. serve and a CLI
	// command): writes from both go through, and each sees the other's.
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
	a, b := open(t, path), open(t, path)

	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 100 {
				if err := s.SetSetting(ctx, fmt.Sprintf("s%d-%d", i, j), "v"); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for _, key := range []string{"s0-99", "s1-99"} {
		for name, s := range map[string]*Store{"a": a, "b": b} {
			if got, err := s.Setting(ctx, key); err != nil || got != "v" {
				t.Errorf("%s.Setting(%s) = %q, %v", name, key, got, err)
			}
		}
	}
}

func TestConcurrentOpenMigratesOnce(t *testing.T) {
	// Several handles opening one new file at once, as two processes starting
	// together would. Each round uses a fresh file; the race is in creating it.
	integration(t)
	for round := range 10 {
		concurrentOpen(t, filepath.Join(t.TempDir(), fmt.Sprintf("t%d.db", round)))
		if t.Failed() {
			return
		}
	}
}

func concurrentOpen(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	const n = 8
	stores := make([]*Store, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stores[i], errs[i] = Open(ctx, path)
		}()
	}
	wg.Wait()
	var ok *Store
	for i, err := range errs {
		if err != nil {
			t.Errorf("Open %d of %d on a new file: %v", i, n, err)
			continue
		}
		defer stores[i].Close()
		ok = stores[i]
	}
	if ok == nil {
		return
	}
	want := len(embeddedMigrations(t))
	var rows, distinct int
	if err := ok.db.QueryRowContext(ctx, `SELECT count(*), count(DISTINCT version) FROM schema_migrations`).Scan(&rows, &distinct); err != nil {
		t.Fatal(err)
	}
	if rows != want || distinct != want {
		t.Errorf("schema_migrations: %d rows, %d versions; want %d each", rows, distinct, want)
	}
	if err := ok.SetSetting(ctx, "k", "v"); err != nil {
		t.Error(err)
	}
}

func TestMigrationFailureRollsBack(t *testing.T) {
	integration(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")

	// A database where migration 0001 cannot run: its table already exists.
	raw, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE settings (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	if s, err := Open(ctx, path); err == nil {
		s.Close()
		t.Fatal("Open succeeded although migration 0001 fails")
	} else if !strings.Contains(err.Error(), "0001") {
		t.Errorf("error %q does not name the failing migration", err)
	}

	raw, err = sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var n int
	if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("failed migration recorded as applied (%d rows)", n)
	}
}

func TestOpenNotADatabase(t *testing.T) {
	integration(t)
	path := filepath.Join(t.TempDir(), "t.db")
	if err := os.WriteFile(path, bytes.Repeat([]byte("not a database "), 512), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(context.Background(), path); err == nil {
		s.Close()
		t.Error("Open of a non-SQLite file succeeded")
	}
}

func TestOpenPathIsDirectory(t *testing.T) {
	integration(t)
	if s, err := Open(context.Background(), t.TempDir()); err == nil {
		s.Close()
		t.Error("Open of a directory succeeded")
	}
}

func TestOpenSpecialCharactersInPath(t *testing.T) {
	// The path goes into a file: URI; characters with a meaning there must
	// not cut the name short or add query parameters.
	ctx := context.Background()
	for _, name := range []string{
		"a?b.db",
		"a#b.db",
		"a%20b.db",
		"x.db?_pragma=journal_mode(delete)",
		"ünïcødé.db",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			s := open(t, path)
			if err := s.SetSetting(ctx, "k", "v"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("database not created at the given path: %v", err)
			}
			var mode string
			if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
				t.Errorf("journal_mode = %q, %v; want wal", mode, err)
			}
		})
	}
}

func TestRelativePath(t *testing.T) {
	integration(t)
	t.Chdir(t.TempDir())
	s := open(t, "rel.db")
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("rel.db"); err != nil {
		t.Errorf("relative path: %v", err)
	}
}

func TestUseAfterClose(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "t.db"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(ctx); err == nil {
		t.Error("Ping after Close: want error")
	}
	if _, err := s.Setting(ctx, "k"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("Setting after Close = %v, want a non-NotFound error", err)
	}
	if err := s.SetSetting(ctx, "k", "v"); err == nil {
		t.Error("SetSetting after Close: want error")
	}
}

func TestCancelledContext(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "t.db"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Ping(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Ping = %v, want context.Canceled", err)
	}
	if _, err := s.Setting(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Setting = %v, want context.Canceled", err)
	}
	if err := s.SetSetting(ctx, "k", "v"); !errors.Is(err, context.Canceled) {
		t.Errorf("SetSetting = %v, want context.Canceled", err)
	}
	if _, err := Open(ctx, filepath.Join(t.TempDir(), "u.db")); err == nil {
		t.Error("Open with a cancelled context: want error")
	}
}

func TestSettingValues(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "t.db"))
	tests := []struct{ key, value string }{
		{"", "empty key"},
		{"unicode", "Grüße, 世界, 🐉"},
		{"quotes", `it's "quoted"`},
		{"'; DROP TABLE settings; --", "injection"},
		{"newlines", "a\nb\r\nc"},
		{"nul", "a\x00b"},
		{"large", strings.Repeat("x", 1<<20)},
		{"Case", "upper"},
		{"case", "lower"},
	}
	for _, tt := range tests {
		if err := s.SetSetting(ctx, tt.key, tt.value); err != nil {
			t.Fatalf("SetSetting(%q): %v", tt.key, err)
		}
	}
	for _, tt := range tests {
		got, err := s.Setting(ctx, tt.key)
		if err != nil || got != tt.value {
			t.Errorf("Setting(%q) = %q (%d bytes), %v; want %d bytes", tt.key, trunc(got), len(got), err, len(tt.value))
		}
	}
}

func trunc(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

func TestSetSettingUpdatesTimestamp(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "t.db"))
	if err := s.SetSetting(ctx, "k", "a"); err != nil {
		t.Fatal(err)
	}
	const old = "2000-01-01T00:00:00.000Z"
	if _, err := s.db.ExecContext(ctx, `UPDATE settings SET updated_at = ? WHERE key = 'k'`, old); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "k", "b"); err != nil {
		t.Fatal(err)
	}
	var at string
	if err := s.db.QueryRowContext(ctx, `SELECT updated_at FROM settings WHERE key = 'k'`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	ts, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		t.Fatalf("updated_at %q is not RFC 3339: %v", at, err)
	}
	if at == old || time.Since(ts) > time.Hour || time.Since(ts) < -time.Hour {
		t.Errorf("updated_at = %q after update, want about now", at)
	}
}

// embeddedMigrations returns the embedded migration files in apply order.
func embeddedMigrations(t *testing.T) []string {
	t.Helper()
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func TestEmbeddedMigrationsAreNumberedInOrder(t *testing.T) {
	// File name order is apply order and a version that already ran is skipped,
	// so two files with one version, or 10_x.sql sorting before 2_x.sql, would
	// silently skip or reorder a migration.
	files := embeddedMigrations(t)
	if len(files) == 0 {
		t.Fatal("no embedded migrations")
	}
	for i, f := range files {
		name := strings.TrimPrefix(f, "migrations/")
		if len(name) < 6 || name[4] != '_' || strings.Trim(name[:4], "0123456789") != "" {
			t.Errorf("%s: want NNNN_name.sql", name)
		}
		v, err := migrationVersion(f)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if v != i+1 {
			t.Errorf("%s: version %d at position %d, want %d (no gaps or duplicates)", name, v, i+1, i+1)
		}
	}
}

func TestAllMigrationsRecorded(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "t.db"))
	rows, err := s.db.QueryContext(ctx, `SELECT version, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		var at string
		if err := rows.Scan(&v, &at); err != nil {
			t.Fatal(err)
		}
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
			t.Errorf("version %d: applied_at %q is not RFC 3339", v, at)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := len(embeddedMigrations(t)); len(versions) != want {
		t.Errorf("recorded versions %v, want %d", versions, want)
	}
}

func TestSettingsTableIsStrict(t *testing.T) {
	// STRICT tables reject values of the wrong type instead of storing them.
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "t.db"))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('k', x'00ff')`); err == nil {
		t.Error("blob accepted into a TEXT column")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('k', NULL)`); err == nil {
		t.Error("NULL accepted into value NOT NULL")
	}
}

func FuzzMigrationVersion(f *testing.F) {
	for _, seed := range []string{
		"migrations/0001_settings.sql",
		"migrations/0012_x_y.sql",
		"migrations/settings.sql",
		"migrations/abcd_x.sql",
		"migrations/0000_x.sql",
		"migrations/-1_x.sql",
		"migrations/_x.sql",
		"migrations/99999999999999999999_x.sql",
		"0001_no_prefix.sql",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, file string) {
		v, err := migrationVersion(file)
		if err != nil {
			if v != 0 {
				t.Fatalf("migrationVersion(%q) = %d with error %v", file, v, err)
			}
			return
		}
		if v <= 0 {
			t.Fatalf("migrationVersion(%q) = %d without error", file, v)
		}
		name := strings.TrimPrefix(file, "migrations/")
		num, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Fatalf("migrationVersion(%q) accepted a name without '_'", file)
		}
		if n, err := strconv.Atoi(num); err != nil || n != v {
			t.Fatalf("migrationVersion(%q) = %d, prefix %q parses as %d, %v", file, v, num, n, err)
		}
	})
}
