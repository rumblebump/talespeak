package sqlite

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// The audit log keeps what game masters did, the newest first, at most
// MaxAuditEntries per world, through finishing the world and deleting the
// account.
func TestAuditLog(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	other, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPrivate, CreatedBy: ann.ID})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	add := func(world int64, action, detail string) {
		t.Helper()
		if err := s.AddAudit(ctx, store.AuditEntry{World: world, Account: ann.ID, Name: "Ann", Action: action, Detail: detail, At: at}); err != nil {
			t.Fatal(err)
		}
	}
	add(w.ID, "say", "hello")
	add(w.ID, "reset room", "cell")
	add(other.ID, "go", "hall")
	if err := s.AddAudit(ctx, store.AuditEntry{World: w.ID, Name: "cli", Action: "say", Detail: strings.Repeat("x", 2*store.MaxAuditDetail), At: at}); err != nil {
		t.Fatal(err)
	}
	l, err := s.AuditLog(ctx, w.ID, 10)
	if err != nil || len(l) != 3 {
		t.Fatalf("log = %+v, %v", l, err)
	}
	if l[0].Account != 0 || len(l[0].Detail) != store.MaxAuditDetail || l[1].Action != "reset room" || l[2].Detail != "hello" ||
		l[2].Account != ann.ID || l[2].Name != "Ann" || !l[2].At.Equal(at) || l[2].World != w.ID {
		t.Errorf("log = %+v", l)
	}
	if l, _ := s.AuditLog(ctx, w.ID, 1); len(l) != 1 || l[0].Name != "cli" {
		t.Errorf("limit 1 = %+v", l)
	}

	// Finishing the world keeps its log.
	if err := s.FinishWorld(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.AuditLog(ctx, other.ID, 10); len(l) != 1 {
		t.Errorf("finished world's log = %+v", l)
	}

	// The oldest go past MaxAuditEntries.
	for i := range store.MaxAuditEntries {
		add(w.ID, "say", strconv.Itoa(i))
	}
	l, _ = s.AuditLog(ctx, w.ID, 2*store.MaxAuditEntries)
	if len(l) != store.MaxAuditEntries || l[0].Detail != strconv.Itoa(store.MaxAuditEntries-1) || l[len(l)-1].Detail != "0" {
		t.Errorf("kept %d, newest %q, oldest %q", len(l), l[0].Detail, l[len(l)-1].Detail)
	}
	if l, _ := s.AuditLog(ctx, other.ID, 10); len(l) != 1 {
		t.Errorf("the other world lost its log: %+v", l)
	}
}

func TestRemoveMember(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	if ok, err := s.RemoveMember(ctx, w.ID, ann.ID); ok || err != nil {
		t.Errorf("no member: %v, %v", ok, err)
	}
	if err := s.AddMember(ctx, w.ID, ann.ID, store.MemberGM); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RemoveMember(ctx, w.ID, ann.ID); !ok || err != nil {
		t.Errorf("member: %v, %v", ok, err)
	}
	if _, err := s.Member(ctx, w.ID, ann.ID); err != store.ErrNotFound {
		t.Errorf("still a member: %v", err)
	}
}

// ListWorlds pages: offset skips, in the same order.
func TestListWorldsOffset(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	for range 5 {
		if _, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPublic, CreatedBy: ann.ID}); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := s.ListWorlds(ctx, 0, true, 0, 10)
	page, err := s.ListWorlds(ctx, 0, true, 2, 2)
	if err != nil || len(all) != 5 || len(page) != 2 || page[0].ID != all[2].ID || page[1].ID != all[3].ID {
		t.Errorf("page = %+v, all = %+v, %v", page, all, err)
	}
	if l, _ := s.ListWorlds(ctx, 0, true, 10, 2); len(l) != 0 {
		t.Errorf("past the end = %+v", l)
	}
	if l, _ := s.ListWorlds(ctx, 0, true, -3, 1); len(l) != 1 || l[0].ID != all[0].ID {
		t.Errorf("negative offset = %+v", l)
	}
}
