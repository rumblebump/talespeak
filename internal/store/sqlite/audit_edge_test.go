package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// The audit migration on a database that lacks it (one from before
// T-0011): reopening applies it once, keeps what the other tables hold,
// and a database that has it keeps its entries across a reopen.
func TestAuditMigrationReRun(t *testing.T) {
	integration(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	at := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	if err := s.AddAudit(ctx, store.AuditEntry{World: w.ID, Name: "cli", Action: "say", Detail: "kept", At: at}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = open(t, path)
	if l, err := s.AuditLog(ctx, w.ID, 10); err != nil || len(l) != 1 || l[0].Detail != "kept" {
		t.Fatalf("after reopen: %+v, %v", l, err)
	}
	// Back to the schema before 0005, as an older server left it.
	for _, q := range []string{`DROP TABLE audit_log`, `DELETE FROM schema_migrations WHERE version = 5`} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s = open(t, path)
	if l, err := s.AuditLog(ctx, w.ID, 10); err != nil || len(l) != 0 {
		t.Errorf("after the migration ran again: %+v, %v", l, err)
	}
	if got, err := s.World(ctx, w.ID); err != nil || got.ID != w.ID {
		t.Errorf("the world after the migration: %+v, %v", got, err)
	}
	if n := count(t, s, `SELECT count(*) FROM schema_migrations WHERE version = 5`); n != 1 {
		t.Errorf("version 5 recorded %d times", n)
	}
	for _, index := range []string{"audit_log_world", "audit_log_action"} {
		if n := count(t, s, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index); n != 1 {
			t.Errorf("index %s: %d", index, n)
		}
	}
	if err := s.AddAudit(ctx, store.AuditEntry{World: w.ID, Name: "cli", Action: "go", Detail: "hall", At: at}); err != nil {
		t.Errorf("AddAudit after the migration: %v", err)
	}
}

// An entry outlives its GM's account, with their name of then; it goes
// with its world only when the world row itself is deleted.
func TestAuditAccountAndWorldDeleted(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	w, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPrivate, CreatedBy: ann.ID})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	if err := s.AddAudit(ctx, store.AuditEntry{World: w.ID, Account: ann.ID, Name: "Ann", Action: "say", Detail: "hi", At: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, ann.ID); err != nil {
		t.Fatal(err)
	}
	if l, err := s.AuditLog(ctx, w.ID, 10); err != nil || len(l) != 1 || l[0].Account != 0 || l[0].Name != "Ann" {
		t.Errorf("after the account went: %+v, %v", l, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM worlds WHERE id = ?`, w.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM audit_log`); n != 0 {
		t.Errorf("%d entries of a deleted world", n)
	}
}

func TestAuditRefuses(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	at := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	if err := s.AddAudit(ctx, store.AuditEntry{World: 9999, Name: "cli", Action: "say", At: at}); err == nil {
		t.Error("an entry of no world was written")
	}
	if err := s.AddAudit(ctx, store.AuditEntry{World: w.ID, Account: 9999, Name: "ghost", Action: "say", At: at}); err == nil {
		t.Error("an entry of no account was written")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.AddAudit(canceled, store.AuditEntry{World: w.ID, Name: "cli", Action: "say", At: at}); err == nil {
		t.Error("AddAudit with a canceled context succeeded")
	}
	if _, err := s.AuditLog(canceled, w.ID, 10); err == nil {
		t.Error("AuditLog with a canceled context succeeded")
	}
	if n := count(t, s, `SELECT count(*) FROM audit_log`); n != 0 {
		t.Errorf("%d entries written", n)
	}
	if l, err := s.AuditLog(ctx, w.ID, -1); err != nil || len(l) != 0 {
		t.Errorf("limit -1 = %+v, %v", l, err)
	}
	if l, err := s.AuditLog(ctx, w.ID, 0); err != nil || len(l) != 0 {
		t.Errorf("limit 0 = %+v, %v", l, err)
	}
}

// Time is kept to the millisecond, in UTC.
func TestAuditTime(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	at := time.Date(2026, 10, 23, 8, 30, 15, 123456789, time.FixedZone("CEST", 2*3600))
	if err := s.AddAudit(ctx, store.AuditEntry{World: w.ID, Name: "cli", Action: "say", At: at}); err != nil {
		t.Fatal(err)
	}
	l, _ := s.AuditLog(ctx, w.ID, 1)
	if len(l) != 1 || !l[0].At.Equal(at.Truncate(time.Millisecond)) || l[0].At.Location() != time.UTC {
		t.Errorf("at = %v, want %v in UTC", l[0].At, at.Truncate(time.Millisecond))
	}
}

// Many game masters write at once (one world's lock each, one database):
// every entry is written, the log of each action stays within
// MaxAuditEntries, and ids keep the order within a writer.
func TestAuditConcurrent(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	var worlds []int64
	for range 4 {
		w, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPublic, CreatedBy: ann.ID})
		if err != nil {
			t.Fatal(err)
		}
		worlds = append(worlds, w.ID)
	}
	at := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	const each = 30
	var wg sync.WaitGroup
	errs := make(chan error, len(worlds)*each)
	for _, w := range worlds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				errs <- s.AddAudit(ctx, store.AuditEntry{World: w, Name: "gm", Action: "say", Detail: strconv.Itoa(i), At: at})
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AddAudit at once: %v", err)
		}
	}
	for _, w := range worlds {
		l, err := s.AuditLog(ctx, w, 2*each)
		if err != nil || len(l) != each {
			t.Fatalf("world %d: %d entries, %v", w, len(l), err)
		}
		for i, e := range l {
			if want := strconv.Itoa(each - 1 - i); e.Detail != want {
				t.Errorf("world %d entry %d = %q, want %q", w, i, e.Detail, want)
			}
		}
	}
}

// Pages of ListWorlds put together are the whole list, without gaps or
// repeats, for a player (own worlds first) as for an admin.
func TestListWorldsPagesCover(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	bob := mustAccount(t, s, "Bob", store.RolePlayer)
	for i := range 9 {
		by, vis := bob.ID, store.VisibilityPublic
		switch i % 3 {
		case 0:
			by = ann.ID // Ann's own, private: a member
			vis = store.VisibilityPrivate
		case 1:
			vis = store.VisibilityPrivate // Bob's private: Ann never sees it
		}
		if _, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: vis, CreatedBy: by}); err != nil {
			t.Fatal(err)
		}
	}
	for _, all := range []bool{false, true} {
		whole, err := s.ListWorlds(ctx, ann.ID, all, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{1, 2, 4} {
			var paged []int64
			for off := 0; off < len(whole)+size; off += size {
				l, err := s.ListWorlds(ctx, ann.ID, all, off, size)
				if err != nil {
					t.Fatal(err)
				}
				for _, w := range l {
					paged = append(paged, w.ID)
				}
			}
			if fmt.Sprint(paged) != fmt.Sprint(worldIDs(whole)) {
				t.Errorf("all %v, pages of %d: %v, want %v", all, size, paged, worldIDs(whole))
			}
		}
		if !all && len(whole) != 6 {
			t.Errorf("Ann sees %d worlds, want her 3 and Bob's 3 public", len(whole))
		}
	}
}

func worldIDs(list []store.WorldInfo) []int64 {
	var out []int64
	for _, w := range list {
		out = append(out, w.ID)
	}
	return out
}
