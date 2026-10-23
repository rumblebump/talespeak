package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// One account creating worlds at once never gets past its own limit.
func TestCreateWorldConcurrentPerAccount(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	const n, per = 10, 2
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPrivate, CreatedBy: ann.ID, PerAccount: per})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, store.ErrAccountWorlds):
			t.Error(err)
		}
	}
	if ok != per || count(t, s, `SELECT count(*) FROM worlds WHERE created_by = ?`, ann.ID) != per {
		t.Errorf("%d worlds created, want %d", ok, per)
	}
	if n := count(t, s, `SELECT count(*) FROM world_members WHERE account_id = ? AND role = 'gm'`, ann.ID); n != per {
		t.Errorf("Ann is GM of %d worlds, want %d", n, per)
	}
}

// A refused world leaves nothing behind: no world row, no member row.
func TestCreateWorldRollsBack(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	for _, nw := range []store.NewWorld{
		{PackID: "demo", Visibility: "secret", CreatedBy: ann.ID},
		{PackID: "demo", Visibility: "", CreatedBy: ann.ID},
		{PackID: "demo", Visibility: store.VisibilityPublic, CreatedBy: 9999}, // no such account
	} {
		if _, err := s.CreateWorld(ctx, nw); err == nil {
			t.Errorf("CreateWorld(%+v) succeeded", nw)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM worlds`); n != 0 {
		t.Errorf("%d worlds after refusals", n)
	}
	if n := count(t, s, `SELECT count(*) FROM world_members`); n != 0 {
		t.Errorf("%d members after refusals", n)
	}
}

// A canceled context creates nothing.
func TestCreateWorldCanceled(t *testing.T) {
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPublic, CreatedBy: ann.ID}); err == nil {
		t.Error("created with a canceled context")
	}
	if _, err := s.ListWorlds(ctx, ann.ID, true, 0, 10); err == nil {
		t.Error("listed with a canceled context")
	}
	if n := count(t, s, `SELECT count(*) FROM worlds`); n != 0 {
		t.Errorf("%d worlds", n)
	}
}

// ListWorlds returns every field a card needs, never a finished world
// the account was a member of, and nothing with a limit of 0.
func TestListWorldsFields(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	w, err := s.CreateWorld(ctx, store.NewWorld{PackID: "test-pack", PackVersion: "2.1", Title: "Ann's <game>", Visibility: store.VisibilityPublic, CreatedBy: ann.ID})
	if err != nil {
		t.Fatal(err)
	}
	gone, _ := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPrivate, CreatedBy: ann.ID})
	if err := s.FinishWorld(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	l, err := s.ListWorlds(ctx, ann.ID, false, 0, 10)
	if err != nil || len(l) != 1 {
		t.Fatalf("ListWorlds = %+v, %v; want one world", l, err)
	}
	wi := l[0]
	if wi.World != w || wi.Creator != "Ann" || wi.Role != store.MemberGM || wi.Players != 0 || wi.PackVersion != "2.1" || wi.Title != "Ann's <game>" {
		t.Errorf("card = %+v, want %+v", wi, w)
	}
	if wi.Created.IsZero() || time.Since(wi.Created) > time.Minute || wi.Created.Location() != time.UTC {
		t.Errorf("created = %v", wi.Created)
	}
	if l, err := s.ListWorlds(ctx, ann.ID, true, 0, 0); err != nil || len(l) != 0 {
		t.Errorf("limit 0 = %d worlds, %v", len(l), err)
	}
	// An unknown account sees the public worlds only.
	if l, err := s.ListWorlds(ctx, 9999, false, 0, 10); err != nil || len(l) != 1 || l[0].Role != "" {
		t.Errorf("unknown account = %+v, %v", l, err)
	}
}

// A finished world's codes are gone with it, and a member's role stays
// recorded but makes nobody a GM any more.
func TestFinishCreatedWorld(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	bob := mustAccount(t, s, "Bob", store.RolePlayer)
	w, _ := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPrivate, CreatedBy: ann.ID})
	other, _ := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPrivate, CreatedBy: ann.ID})
	now := time.Now()
	for i, world := range []int64{w.ID, other.ID} {
		if err := s.CreateInvite(ctx, store.Invite{CodeHash: []byte{byte(i), 1}, WorldID: world, CreatedBy: ann.ID, Role: store.MemberPlayer, Uses: 5, Expires: now.Add(time.Hour)}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FinishWorld(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM invites WHERE world_id = ?`, w.ID); n != 0 {
		t.Errorf("%d codes of the finished world left", n)
	}
	if _, err := s.RedeemInvite(ctx, []byte{0, 1}, bob.ID, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("code of a finished world = %v", err)
	}
	if got, err := s.RedeemInvite(ctx, []byte{1, 1}, bob.ID, now); err != nil || got != other.ID {
		t.Errorf("code of the other world = %d, %v; want %d", got, err, other.ID)
	}
	if role, err := s.Member(ctx, w.ID, ann.ID); err != nil || role != store.MemberGM {
		t.Errorf("member of the finished world = %q, %v", role, err)
	}
}

// parseTime reads what strftime wrote, with or without fractions, and
// is the zero time for anything else.
func TestParseTime(t *testing.T) {
	for in, want := range map[string]time.Time{
		"2026-10-22T12:00:00.123Z": time.Date(2026, 10, 22, 12, 0, 0, 123e6, time.UTC),
		"2026-10-22T12:00:00Z":     time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		"2026-10-22 12:00:00":      {},
		"":                         {},
		"garbage":                  {},
	} {
		if got := parseTime(in); !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v, want %v", in, got, want)
		}
	}
}

// OpenWorld and the lobby keep each pack's main world apart, and a
// created world of the same pack never becomes one.
func TestOpenWorldPerPack(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	if _, err := s.CreateWorld(ctx, store.NewWorld{PackID: "other", Visibility: store.VisibilityPublic, CreatedBy: ann.ID}); err != nil {
		t.Fatal(err)
	}
	demo, err := s.OpenWorld(ctx, "demo", "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.OpenWorld(ctx, "other", "1", nil)
	if err != nil || other.ID == demo.ID || !other.Home || other.CreatedBy != 0 {
		t.Errorf("main world of another pack = %+v, %v", other, err)
	}
	// Finishing a main world starts a new one; its old id is not reused.
	if err := s.FinishWorld(ctx, demo.ID); err != nil {
		t.Fatal(err)
	}
	again, err := s.OpenWorld(ctx, "demo", "2", nil)
	if err != nil || again.ID == demo.ID || !again.Home || again.PackVersion != "2" {
		t.Errorf("after finishing the main world: %+v, %v", again, err)
	}
}
