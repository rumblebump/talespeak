package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// CreateWorld adds a world with its creator as GM, within the per-account
// and server limits; only unfinished worlds count.
func TestCreateWorld(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	home, _ := s.OpenWorld(ctx, "demo", "", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	bob := mustAccount(t, s, "Bob", store.RolePlayer)
	create := func(by store.Account, per, total int) (store.World, error) {
		return s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Title: "Ann's game", Visibility: store.VisibilityPrivate,
			CreatedBy: by.ID, PerAccount: per, Total: total})
	}
	w, err := create(ann, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if w.ID == home.ID || w.PackID != "demo" || w.Title != "Ann's game" || w.Visibility != store.VisibilityPrivate ||
		w.CreatedBy != ann.ID || w.Home || w.Finished || time.Since(w.Created) > time.Minute {
		t.Errorf("created %+v", w)
	}
	if r, err := s.Member(ctx, w.ID, ann.ID); err != nil || r != store.MemberGM {
		t.Errorf("creator's role = %q, %v; want gm", r, err)
	}
	if _, err := create(ann, 2, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := create(ann, 2, 10); !errors.Is(err, store.ErrAccountWorlds) {
		t.Errorf("third world with a limit of 2 = %v, want ErrAccountWorlds", err)
	}
	if _, err := create(bob, 2, 4); err != nil { // the main world, two of Ann's and this one
		t.Fatal(err)
	}
	if _, err := create(bob, 2, 4); !errors.Is(err, store.ErrServerWorlds) {
		t.Errorf("fifth world with a server limit of 4 = %v, want ErrServerWorlds", err)
	}
	if err := s.FinishWorld(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := create(ann, 2, 0); err != nil {
		t.Errorf("a finished world still counts: %v", err)
	}
	if _, err := create(ann, 0, 0); err != nil {
		t.Errorf("no limits: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM worlds WHERE created_by = ? AND finished_at IS NULL`, ann.ID); n != 3 {
		t.Errorf("Ann's unfinished worlds = %d, want 3", n)
	}
	// The main world stays the main world: OpenWorld never returns a
	// created one.
	if again, err := s.OpenWorld(ctx, "demo", "", nil); err != nil || again.ID != home.ID || !again.Home {
		t.Errorf("OpenWorld = %+v, %v; want the main world %d", again, err, home.ID)
	}
	if _, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: "secret", CreatedBy: ann.ID}); err == nil {
		t.Error("a world with an unknown visibility was created")
	}
}

// Many accounts creating worlds at once never get past the server limit.
func TestCreateWorldConcurrent(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	const n, total = 10, 4
	var accs []store.Account
	for i := range n {
		accs = append(accs, mustAccount(t, s, fmt.Sprint("p", i), store.RolePlayer))
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, a := range accs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPublic, CreatedBy: a.ID, PerAccount: 1, Total: total})
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
		case !errors.Is(err, store.ErrServerWorlds):
			t.Error(err)
		}
	}
	if ok != total || count(t, s, `SELECT count(*) FROM worlds`) != total {
		t.Errorf("%d worlds created, want %d", ok, total)
	}
}

// ListWorlds lists the unfinished public worlds and the account's own,
// with its role, the creator and the number of characters; all lists the
// private ones of others too.
func TestListWorlds(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	home, _ := s.OpenWorld(ctx, "demo", "", nil)
	if err := s.SetVisibility(ctx, home.ID, store.VisibilityPublic); err != nil {
		t.Fatal(err)
	}
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	bob := mustAccount(t, s, "Bob", store.RolePlayer)
	cat := mustAccount(t, s, "Cat", store.RolePlayer)
	mk := func(by store.Account, vis string) store.World {
		w, err := s.CreateWorld(ctx, store.NewWorld{PackID: "test-pack", Visibility: vis, CreatedBy: by.ID})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	pub := mk(ann, store.VisibilityPublic)
	priv := mk(ann, store.VisibilityPrivate)
	gone := mk(ann, store.VisibilityPublic)
	if err := s.FinishWorld(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ctx, priv.ID, bob.ID, store.MemberPlayer); err != nil {
		t.Fatal(err)
	}
	for _, a := range []store.Account{ann, bob} {
		if _, err := s.Character(ctx, priv.ID, a.ID, a.Name); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(list []store.WorldInfo) []int64 {
		var out []int64
		for _, w := range list {
			out = append(out, w.ID)
		}
		return out
	}
	list := func(a store.Account, all bool) []store.WorldInfo {
		t.Helper()
		l, err := s.ListWorlds(ctx, a.ID, all, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if got := ids(list(cat, false)); fmt.Sprint(got) != fmt.Sprint([]int64{pub.ID, home.ID}) {
		t.Errorf("Cat sees %v, want the public ones %v", got, []int64{pub.ID, home.ID})
	}
	bobs := list(bob, false)
	if got := ids(bobs); fmt.Sprint(got) != fmt.Sprint([]int64{priv.ID, pub.ID, home.ID}) {
		t.Errorf("Bob sees %v", got)
	}
	if p := bobs[0]; p.Role != store.MemberPlayer || p.Creator != "Ann" || p.Players != 2 || p.Visibility != store.VisibilityPrivate || p.PackID != "test-pack" {
		t.Errorf("Bob's card of the private world = %+v", p)
	}
	if h := bobs[2]; h.Role != "" || h.Creator != "" || !h.Home || h.Players != 0 {
		t.Errorf("Bob's card of the main world = %+v", h)
	}
	if got := ids(list(cat, true)); fmt.Sprint(got) != fmt.Sprint([]int64{priv.ID, pub.ID, home.ID}) {
		t.Errorf("all = %v, want every unfinished world", got)
	}
	if l, _ := s.ListWorlds(ctx, cat.ID, true, 0, 1); len(l) != 1 || l[0].ID != priv.ID {
		t.Errorf("limit 1 = %v, want the newest", ids(l))
	}
	// The account's own worlds come first, so newer public ones never push
	// them out of a short list.
	later := mk(cat, store.VisibilityPublic)
	if l, _ := s.ListWorlds(ctx, bob.ID, false, 0, 1); len(l) != 1 || l[0].ID != priv.ID {
		t.Errorf("Bob's list of 1 = %v, want his own world %d", ids(l), priv.ID)
	}
	if got := ids(list(cat, false)); fmt.Sprint(got) != fmt.Sprint([]int64{later.ID, pub.ID, home.ID}) {
		t.Errorf("Cat sees %v, want hers first", got)
	}
	// A deleted creator leaves the world without one.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, ann.ID); err != nil {
		t.Fatal(err)
	}
	if w, err := s.World(ctx, pub.ID); err != nil || w.CreatedBy != 0 {
		t.Errorf("world of a deleted account = %+v, %v", w, err)
	}
}

// IsGM is true for a GM of an unfinished world only.
func TestIsGM(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	home, _ := s.OpenWorld(ctx, "demo", "", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	bob := mustAccount(t, s, "Bob", store.RolePlayer)
	is := func(a store.Account) bool {
		t.Helper()
		ok, err := s.IsGM(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if is(ann) {
		t.Error("a new account is a GM")
	}
	_ = s.AddMember(ctx, home.ID, bob.ID, store.MemberPlayer)
	w, _ := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", Visibility: store.VisibilityPrivate, CreatedBy: ann.ID})
	if !is(ann) || is(bob) {
		t.Errorf("IsGM: Ann %v, Bob %v; want true, false", is(ann), is(bob))
	}
	if err := s.FinishWorld(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if is(ann) {
		t.Error("the GM of a finished world is still a GM")
	}
}

// World returns any world by id, finished ones too.
func TestWorld(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	home, _ := s.OpenWorld(ctx, "demo", "1.0", nil)
	w, err := s.World(ctx, home.ID)
	if err != nil || w != home || !w.Home || w.PackVersion != "1.0" || w.Visibility != store.VisibilityPrivate {
		t.Errorf("World = %+v, %v; want %+v", w, err, home)
	}
	if err := s.SetVisibility(ctx, home.ID, store.VisibilityPublic); err != nil {
		t.Fatal(err)
	}
	_ = s.FinishWorld(ctx, home.ID)
	if w, err := s.World(ctx, home.ID); err != nil || !w.Finished || w.Visibility != store.VisibilityPublic {
		t.Errorf("finished World = %+v, %v", w, err)
	}
	if _, err := s.World(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("World(unknown) = %v, want ErrNotFound", err)
	}
	if err := s.SetVisibility(ctx, 9999, store.VisibilityPublic); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetVisibility(unknown) = %v, want ErrNotFound", err)
	}
	if err := s.SetVisibility(ctx, home.ID, "secret"); err == nil {
		t.Error("SetVisibility took an unknown visibility")
	}
}

// A database from before the lobby keeps its world as the main world, with
// its state, and private until the server sets its visibility.
func TestMigrateWorldsToLobby(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite3", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"migrations/0001_settings.sql", "migrations/0002_accounts_worlds.sql"} {
		body, err := migrations.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	for _, q := range []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))) STRICT`,
		`INSERT INTO schema_migrations (version) VALUES (1), (2)`,
		`INSERT INTO worlds (pack_id, pack_version) VALUES ('demo', '')`,
		`INSERT INTO world_state (world_id, key, value) VALUES (1, 'door', X'01')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	s := open(t, path)
	w, err := s.OpenWorld(ctx, "demo", "", nil)
	if err != nil || w.ID != 1 || !w.Home || w.Visibility != store.VisibilityPrivate || w.Title != "" || w.CreatedBy != 0 {
		t.Fatalf("old world after the migration = %+v, %v", w, err)
	}
	if vals, _ := s.WorldState(ctx, 1); len(vals) != 1 {
		t.Errorf("old world's state = %v", vals)
	}
}

// A world keeps at most MaxLiveInvites invites that work; used up and
// expired ones make room again.
func TestInviteCap(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	other, err := s.CreateWorld(ctx, store.NewWorld{PackID: "test-pack", Visibility: store.VisibilityPrivate, CreatedBy: mustAccount(t, s, "Ann", store.RolePlayer).ID})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	add := func(world int64, n int, expires time.Time) error {
		inv := store.Invite{CodeHash: []byte(fmt.Sprintf("%d-%d-%d", world, n, expires.Unix())), WorldID: world, Role: store.MemberPlayer, Uses: 1, Expires: expires}
		return s.CreateInvite(ctx, inv, now)
	}
	for i := range store.MaxLiveInvites - 1 {
		if err := add(w.ID, i, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := add(w.ID, 999, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := add(w.ID, 1000, now.Add(time.Hour)); !errors.Is(err, store.ErrInvites) {
		t.Fatalf("one past the cap = %v", err)
	}
	if err := add(other.ID, 0, now.Add(time.Hour)); err != nil {
		t.Errorf("another world's invite = %v", err)
	}
	now = now.Add(2 * time.Minute) // the short one expired
	if err := add(w.ID, 1001, now.Add(time.Hour)); err != nil {
		t.Errorf("after one expired = %v", err)
	}
}
