package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// fresh opens a new store in a temp dir.
func fresh(t *testing.T) *Store {
	t.Helper()
	return open(t, filepath.Join(t.TempDir(), "t.db"))
}

// hashOf stands in for the hash of a session token or invite code.
func hashOf(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func mustAccount(t *testing.T, s *Store, name, role string) store.Account {
	t.Helper()
	a, err := s.CreateAccount(context.Background(), name, "hash-of-"+name, role)
	if err != nil {
		t.Fatalf("CreateAccount(%q): %v", name, err)
	}
	return a
}

func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestAccountNamesUniqueInAnyCase(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	if a.ID == 0 || a.Name != "Mira" || a.Role != store.RolePlayer {
		t.Fatalf("CreateAccount = %+v", a)
	}
	for _, name := range []string{"Mira", "mira", "MIRA", "mIrA"} {
		if _, err := s.CreateAccount(ctx, name, "h", store.RolePlayer); !errors.Is(err, store.ErrNameTaken) {
			t.Errorf("CreateAccount(%q) = %v, want ErrNameTaken", name, err)
		}
	}
	for _, name := range []string{"Mira", "mira", "MIRA"} {
		got, hash, err := s.AccountByName(ctx, name)
		if err != nil || got != a || hash != "hash-of-Mira" {
			t.Errorf("AccountByName(%q) = %+v, %q, %v; want %+v with its hash", name, got, hash, err, a)
		}
	}
	if _, _, err := s.AccountByName(ctx, "Mir"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("AccountByName(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := s.CreateAccount(ctx, "Zed", "h", "wizard"); err == nil || errors.Is(err, store.ErrNameTaken) {
		t.Errorf("CreateAccount with an unknown role = %v, want a constraint error", err)
	}
	b := mustAccount(t, s, "Zed", store.RoleAdmin)
	if b.ID == a.ID {
		t.Errorf("two accounts share id %d", a.ID)
	}
}

func TestSetPasswordEndsSessions(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	b := mustAccount(t, s, "Zed", store.RolePlayer)
	exp := time.Now().Add(time.Hour)
	for i, acc := range []int64{a.ID, a.ID, b.ID} {
		if err := s.CreateLogin(ctx, hashOf(fmt.Sprint("t", i)), acc, time.Now(), exp, 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetPassword(ctx, a.ID, "new hash"); err != nil {
		t.Fatal(err)
	}
	if _, hash, _ := s.AccountByName(ctx, "mira"); hash != "new hash" {
		t.Errorf("hash = %q, want the new one", hash)
	}
	for i, want := range []error{store.ErrNotFound, store.ErrNotFound, nil} {
		if _, err := s.LoginAccount(ctx, hashOf(fmt.Sprint("t", i)), time.Now()); !errors.Is(err, want) {
			t.Errorf("session %d after SetPassword: %v, want %v", i, err, want)
		}
	}
	if err := s.SetPassword(ctx, 9999, "h"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetPassword(unknown) = %v, want ErrNotFound", err)
	}
}

func TestSetRole(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	for _, role := range []string{store.RoleAdmin, store.RolePlayer, store.RoleAdmin} {
		if err := s.SetRole(ctx, a.ID, role); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := s.AccountByName(ctx, "Mira"); got.Role != role {
			t.Errorf("role = %q, want %q", got.Role, role)
		}
	}
	if err := s.SetRole(ctx, a.ID, "wizard"); err == nil {
		t.Error("SetRole(wizard): want a constraint error")
	}
	if err := s.SetRole(ctx, 9999, store.RoleAdmin); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetRole(unknown) = %v, want ErrNotFound", err)
	}
}

// CreateLogin keeps the account's newest sessions, and drops every
// account's expired ones.
func TestCreateLoginKeepsNewest(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	b := mustAccount(t, s, "Zed", store.RolePlayer)
	base := time.Now().Add(time.Hour)
	if err := s.CreateLogin(ctx, hashOf("zed"), b.ID, time.Now(), base, 3); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := s.CreateLogin(ctx, hashOf(fmt.Sprint("m", i)), a.ID, time.Now(), base.Add(time.Duration(i)*time.Minute), 3); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 5 {
		_, err := s.LoginAccount(ctx, hashOf(fmt.Sprint("m", i)), time.Now())
		if want := i >= 2; (err == nil) != want {
			t.Errorf("session m%d: err = %v, want kept = %v", i, err, want)
		}
	}
	if got, err := s.LoginAccount(ctx, hashOf("zed"), time.Now()); err != nil || got != b {
		t.Errorf("another account's session = %+v, %v; want it kept", got, err)
	}
	if n := count(t, s, `SELECT count(*) FROM logins WHERE account_id = ?`, a.ID); n != 3 {
		t.Errorf("sessions kept = %d, want 3", n)
	}

	// Expired sessions of any account go with the next sign-in.
	if _, err := s.db.ExecContext(ctx, `UPDATE logins SET expires_at = ? WHERE token_hash = ?`, time.Now().Add(-time.Second).Unix(), hashOf("zed")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLogin(ctx, hashOf("m9"), a.ID, time.Now(), base.Add(time.Hour), 3); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM logins WHERE account_id = ?`, b.ID); n != 0 {
		t.Errorf("expired sessions left = %d, want 0", n)
	}

	if err := s.CreateLogin(ctx, hashOf("m9"), a.ID, time.Now(), base, 3); err == nil {
		t.Error("CreateLogin with a token hash in use: want an error")
	}
	if err := s.CreateLogin(ctx, hashOf("x"), 9999, time.Now(), base, 3); err == nil {
		t.Error("CreateLogin for an unknown account: want a foreign key error")
	}
}

func TestLoginAccountExpiry(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	tok := hashOf("t")
	if err := s.CreateLogin(ctx, tok, a.ID, time.Now(), exp, 10); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		now  time.Time
		want error
	}{
		{exp.Add(-time.Hour), nil},
		{exp.Add(-time.Second), nil},
		{exp, store.ErrNotFound}, // a session ends at its expiry
		{exp.Add(time.Second), store.ErrNotFound},
	} {
		got, err := s.LoginAccount(ctx, tok, tt.now)
		if !errors.Is(err, tt.want) || (err == nil && got != a) {
			t.Errorf("LoginAccount at expiry%+v = %+v, %v; want %v", tt.now.Sub(exp), got, err, tt.want)
		}
	}
	if _, err := s.LoginAccount(ctx, hashOf("other"), exp.Add(-time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown token: %v", err)
	}
	// A role change shows in the next lookup.
	if err := s.SetRole(ctx, a.ID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LoginAccount(ctx, tok, exp.Add(-time.Hour)); got.Role != store.RoleAdmin {
		t.Errorf("role after SetRole = %q", got.Role)
	}
	if err := s.DeleteLogin(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginAccount(ctx, tok, exp.Add(-time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after DeleteLogin: %v", err)
	}
	if err := s.DeleteLogin(ctx, tok); err != nil {
		t.Errorf("DeleteLogin twice: %v", err)
	}
}

func TestOpenWorld(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, err := s.OpenWorld(ctx, "demo", "1.0", nil)
	if err != nil || w.ID == 0 || w.PackID != "demo" || w.PackVersion != "1.0" {
		t.Fatalf("OpenWorld = %+v, %v", w, err)
	}
	// OpenWorld leaves the version as it is: the hub records the version
	// a world runs when it loads.
	again, err := s.OpenWorld(ctx, "demo", "1.1", nil)
	if err != nil || again.ID != w.ID || again.PackVersion != "1.0" {
		t.Errorf("OpenWorld again = %+v, %v; want world %d at 1.0", again, err, w.ID)
	}
	if err := s.SetPackVersion(ctx, w.ID, "1.1"); err != nil {
		t.Fatal(err)
	}
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT pack_version FROM worlds WHERE id = ?`, w.ID).Scan(&v); err != nil || v != "1.1" {
		t.Errorf("stored version = %q, %v", v, err)
	}
	if err := s.SetPackVersion(ctx, 999, "1.1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetPackVersion of no world: %v", err)
	}
	other, err := s.OpenWorld(ctx, "other", "", nil)
	if err != nil || other.ID == w.ID {
		t.Errorf("another pack = %+v, %v; want its own world", other, err)
	}
}

// Finishing a world deletes what it kept; the pack's next world starts
// empty, with a new id, and its characters get new ids too.
func TestFinishWorld(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	other, _ := s.OpenWorld(ctx, "other", "", nil)
	c, err := s.Character(ctx, w.ID, a.ID, "Mira")
	if err != nil {
		t.Fatal(err)
	}
	oc, err := s.Character(ctx, other.ID, a.ID, "Mira")
	if err != nil {
		t.Fatal(err)
	}
	put := []store.Change{{Key: "k", Value: []byte("v")}}
	for _, sv := range []store.Save{
		{World: w.ID, WorldState: put, Character: c.ID, PlayerState: put, Room: "hall"},
		{World: other.ID, WorldState: put, Character: oc.ID, PlayerState: put},
	} {
		if err := s.Save(ctx, sv); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddMember(ctx, w.ID, a.ID, store.MemberGM); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInvite(ctx, store.Invite{CodeHash: hashOf("code"), WorldID: w.ID, Role: store.MemberPlayer, Uses: 5, Expires: time.Now().Add(time.Hour)}, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := s.FinishWorld(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if vals, err := s.WorldState(ctx, w.ID); err != nil || len(vals) != 0 {
		t.Errorf("world state after finish = %v, %v", vals, err)
	}
	if vals, err := s.PlayerState(ctx, c.ID); err != nil || len(vals) != 0 {
		t.Errorf("player state after finish = %v, %v", vals, err)
	}
	if _, err := s.RedeemInvite(ctx, hashOf("code"), a.ID, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("invite after finish: %v, want ErrNotFound", err)
	}
	// The other pack's world is untouched.
	if vals, _ := s.WorldState(ctx, other.ID); string(vals["k"]) != "v" {
		t.Errorf("other world's state = %v", vals)
	}
	if vals, _ := s.PlayerState(ctx, oc.ID); string(vals["k"]) != "v" {
		t.Errorf("other world's player state = %v", vals)
	}

	if err := s.FinishWorld(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FinishWorld twice = %v, want ErrNotFound", err)
	}
	if err := s.FinishWorld(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FinishWorld(unknown) = %v, want ErrNotFound", err)
	}

	next, err := s.OpenWorld(ctx, "demo", "", nil)
	if err != nil || next.ID <= other.ID || next.ID == w.ID {
		t.Fatalf("OpenWorld after finish = %+v, %v; want a new id after %d", next, err, other.ID)
	}
	if vals, _ := s.WorldState(ctx, next.ID); len(vals) != 0 {
		t.Errorf("new world inherits state %v", vals)
	}
	if _, err := s.Member(ctx, next.ID, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("membership carried into the new world: %v", err)
	}
	nc, err := s.Character(ctx, next.ID, a.ID, "Mira")
	if err != nil || nc.ID == c.ID || nc.ID == oc.ID || nc.Room != "" {
		t.Errorf("character in the new world = %+v, %v; want a new id and no room", nc, err)
	}
}

func TestMembers(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	if _, err := s.Member(ctx, w.ID, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Member before AddMember = %v, want ErrNotFound", err)
	}
	for _, role := range []string{store.MemberPlayer, store.MemberGM, store.MemberPlayer} {
		if err := s.AddMember(ctx, w.ID, a.ID, role); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Member(ctx, w.ID, a.ID); err != nil || got != role {
			t.Errorf("Member = %q, %v; want %q", got, err, role)
		}
	}
	if err := s.AddMember(ctx, w.ID, a.ID, "owner"); err == nil {
		t.Error("AddMember(owner): want a constraint error")
	}
	if err := s.AddMember(ctx, 9999, a.ID, store.MemberPlayer); err == nil {
		t.Error("AddMember to an unknown world: want a foreign key error")
	}
}

func TestRedeemInvite(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	gm := mustAccount(t, s, "Gwen", store.RolePlayer)
	p1 := mustAccount(t, s, "Ann", store.RolePlayer)
	p2 := mustAccount(t, s, "Bob", store.RolePlayer)
	p3 := mustAccount(t, s, "Cat", store.RolePlayer)
	now := time.Now().Truncate(time.Second)
	uses := func(code string) int {
		return count(t, s, `SELECT uses_left FROM invites WHERE code_hash = ?`, hashOf(code))
	}
	invite := func(code, role string, n int, exp time.Time) {
		t.Helper()
		if err := s.CreateInvite(ctx, store.Invite{CodeHash: hashOf(code), WorldID: w.ID, CreatedBy: gm.ID, Role: role, Uses: n, Expires: exp}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	redeem := func(code string, acc store.Account, at time.Time) error {
		t.Helper()
		world, err := s.RedeemInvite(ctx, hashOf(code), acc.ID, at)
		if err == nil && world != w.ID {
			t.Errorf("RedeemInvite(%q) joined world %d, want %d", code, world, w.ID)
		}
		return err
	}
	role := func(acc store.Account) string {
		r, _ := s.Member(ctx, w.ID, acc.ID)
		return r
	}

	invite("players", store.MemberPlayer, 2, now.Add(time.Hour))
	invite("gms", store.MemberGM, 1, now.Add(time.Hour))
	if err := s.AddMember(ctx, w.ID, gm.ID, store.MemberGM); err != nil {
		t.Fatal(err)
	}

	if err := redeem("unknown", p1, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown code: %v", err)
	}
	if err := redeem("players", p1, now); err != nil || role(p1) != store.MemberPlayer || uses("players") != 1 {
		t.Errorf("first use: %v, role %q, uses left %d", err, role(p1), uses("players"))
	}
	// A member with the invite's role already uses nothing.
	if err := redeem("players", p1, now); err != nil || uses("players") != 1 {
		t.Errorf("same account again: %v, uses left %d; want 1", err, uses("players"))
	}
	// A GM redeeming a player invite keeps gm and uses nothing.
	if err := redeem("players", gm, now); err != nil || role(gm) != store.MemberGM || uses("players") != 1 {
		t.Errorf("gm redeems a player invite: %v, role %q, uses left %d", err, role(gm), uses("players"))
	}
	// A player redeeming a GM invite becomes one.
	if err := redeem("gms", p1, now); err != nil || role(p1) != store.MemberGM || uses("gms") != 0 {
		t.Errorf("player redeems a gm invite: %v, role %q, uses left %d", err, role(p1), uses("gms"))
	}
	if err := redeem("gms", p2, now); !errors.Is(err, store.ErrNotFound) || role(p2) != "" {
		t.Errorf("used up gm invite: %v, role %q", err, role(p2))
	}
	if err := redeem("players", p2, now); err != nil || uses("players") != 0 {
		t.Errorf("last use: %v, uses left %d", err, uses("players"))
	}
	if err := redeem("players", p3, now); !errors.Is(err, store.ErrNotFound) || role(p3) != "" {
		t.Errorf("used up: %v, role %q", err, role(p3))
	}

	// A code joins its own world, never another.
	other, _ := s.OpenWorld(ctx, "other", "", nil)
	if err := s.CreateInvite(ctx, store.Invite{CodeHash: hashOf("there"), WorldID: other.ID, Role: store.MemberPlayer, Uses: 1, Expires: now.Add(time.Hour)}, now); err != nil {
		t.Fatal(err)
	}
	if world, err := s.RedeemInvite(ctx, hashOf("there"), p3.ID, now); err != nil || world != other.ID || role(p3) != "" {
		t.Errorf("code of another world: world %d, %v, role here %q; want world %d", world, err, role(p3), other.ID)
	}

	invite("soon", store.MemberPlayer, 5, now.Add(time.Minute))
	if err := redeem("soon", p3, now.Add(time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("at its expiry: %v, want ErrNotFound", err)
	}
	if err := redeem("soon", p3, now.Add(time.Minute-time.Second)); err != nil || role(p3) != store.MemberPlayer {
		t.Errorf("just before its expiry: %v, role %q", err, role(p3))
	}
}

// CreateInvite drops invites that expired or are used up. The command
// line's invites have no maker.
func TestCreateInvitePrunes(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	a := mustAccount(t, s, "Ann", store.RolePlayer)
	for _, inv := range []store.Invite{
		{CodeHash: hashOf("old"), WorldID: w.ID, Role: store.MemberPlayer, Uses: 1, Expires: time.Now().Add(-time.Minute)},
		{CodeHash: hashOf("used"), WorldID: w.ID, Role: store.MemberPlayer, Uses: 1, Expires: time.Now().Add(time.Hour)},
	} {
		if err := s.CreateInvite(ctx, inv, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RedeemInvite(ctx, hashOf("used"), a.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInvite(ctx, store.Invite{CodeHash: hashOf("new"), WorldID: w.ID, Role: store.MemberGM, Uses: 3, Expires: time.Now().Add(time.Hour)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM invites`); n != 1 {
		t.Errorf("invites = %d, want only the new one", n)
	}
	if n := count(t, s, `SELECT count(*) FROM invites WHERE code_hash = ? AND created_by IS NULL`, hashOf("new")); n != 1 {
		t.Error("an invite from the command line has a maker")
	}
	for _, bad := range []store.Invite{
		{CodeHash: hashOf("new"), WorldID: w.ID, Role: store.MemberPlayer, Uses: 1, Expires: time.Now().Add(time.Hour)}, // code in use
		{CodeHash: hashOf("neg"), WorldID: w.ID, Role: store.MemberPlayer, Uses: -1, Expires: time.Now().Add(time.Hour)},
		{CodeHash: hashOf("role"), WorldID: w.ID, Role: "owner", Uses: 1, Expires: time.Now().Add(time.Hour)},
		{CodeHash: hashOf("world"), WorldID: 9999, Role: store.MemberPlayer, Uses: 1, Expires: time.Now().Add(time.Hour)},
	} {
		if err := s.CreateInvite(ctx, bad, time.Now()); err == nil {
			t.Errorf("CreateInvite(%+v): want an error", bad)
		}
	}
}

// Two accounts redeeming a one-use code at once: exactly one gets in.
func TestRedeemInviteConcurrent(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	const n = 8
	var accs []store.Account
	for i := range n {
		accs = append(accs, mustAccount(t, s, fmt.Sprint("p", i), store.RolePlayer))
	}
	for round := range 5 {
		code := hashOf(fmt.Sprint("one use ", round))
		if err := s.CreateInvite(ctx, store.Invite{CodeHash: code, WorldID: w.ID, Role: store.MemberPlayer, Uses: 1, Expires: time.Now().Add(time.Hour)}, time.Now()); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, n)
		start := make(chan struct{})
		for _, a := range accs {
			go func() {
				<-start
				_, err := s.RedeemInvite(ctx, code, a.ID, time.Now())
				errs <- err
			}()
		}
		close(start)
		ok := 0
		for range n {
			switch err := <-errs; {
			case err == nil:
				ok++
			case !errors.Is(err, store.ErrNotFound):
				t.Errorf("round %d: %v", round, err)
			}
		}
		members := count(t, s, `SELECT count(*) FROM world_members WHERE world_id = ?`, w.ID)
		if ok != 1 || members != 1 || count(t, s, `SELECT uses_left FROM invites WHERE code_hash = ?`, code) != 0 {
			t.Fatalf("round %d: %d redeemed a one-use code, %d members; want 1 and 1", round, ok, members)
		}
		// The next round starts with no members again.
		if _, err := s.db.ExecContext(ctx, `DELETE FROM world_members`); err != nil {
			t.Fatal(err)
		}
	}
}

// Character creates one character per account and world, and then always
// returns it: same id, first name, latest room.
func TestCharacter(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	b := mustAccount(t, s, "Zed", store.RolePlayer)
	c, err := s.Character(ctx, w.ID, a.ID, "Mira")
	if err != nil || c.ID == 0 || c.Name != "Mira" || c.Room != "" {
		t.Fatalf("Character = %+v, %v", c, err)
	}
	if err := s.Save(ctx, store.Save{World: w.ID, Character: c.ID, Room: "hall"}); err != nil {
		t.Fatal(err)
	}
	again, err := s.Character(ctx, w.ID, a.ID, "Renamed")
	if err != nil || again.ID != c.ID || again.Name != "Mira" || again.Room != "hall" {
		t.Errorf("Character again = %+v, %v; want id %d, name Mira, room hall", again, err, c.ID)
	}
	zed, err := s.Character(ctx, w.ID, b.ID, "Zed")
	if err != nil || zed.ID == c.ID {
		t.Errorf("another account's character = %+v, %v", zed, err)
	}
	// Ids never repeat, also after a character is gone.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM characters WHERE id = ?`, zed.ID); err != nil {
		t.Fatal(err)
	}
	z2, err := s.Character(ctx, w.ID, b.ID, "Zed")
	if err != nil || z2.ID <= zed.ID {
		t.Errorf("character after delete = %+v, %v; want an id after %d", z2, err, zed.ID)
	}
	if _, err := s.Character(ctx, 9999, a.ID, "Mira"); err == nil {
		t.Error("Character in an unknown world: want a foreign key error")
	}
}

func TestSave(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	a := mustAccount(t, s, "Mira", store.RolePlayer)
	c, _ := s.Character(ctx, w.ID, a.ID, "Mira")
	room := func() string {
		got, _ := s.Character(ctx, w.ID, a.ID, "Mira")
		return got.Room
	}

	if vals, err := s.WorldState(ctx, w.ID); err != nil || vals == nil || len(vals) != 0 {
		t.Errorf("empty WorldState = %v, %v; want an empty map", vals, err)
	}
	if vals, err := s.PlayerState(ctx, c.ID); err != nil || vals == nil || len(vals) != 0 {
		t.Errorf("empty PlayerState = %v, %v; want an empty map", vals, err)
	}
	if err := s.Save(ctx, store.Save{World: w.ID}); err != nil {
		t.Errorf("empty save: %v", err)
	}
	if !(store.Save{World: 1, Character: 2}).Empty() || (store.Save{Room: "r"}).Empty() ||
		(store.Save{WorldState: []store.Change{{Key: "k"}}}).Empty() || (store.Save{PlayerState: []store.Change{{Key: "k"}}}).Empty() {
		t.Error("Save.Empty is wrong")
	}

	if err := s.Save(ctx, store.Save{
		World:       w.ID,
		WorldState:  []store.Change{{Key: "door", Value: []byte("open")}, {Key: "bell", Value: []byte("1")}},
		Character:   c.ID,
		PlayerState: []store.Change{{Key: "nail", Value: []byte{0, 1, 2}}, {Key: "hat", Value: []byte("red")}},
		Room:        "hall",
	}); err != nil {
		t.Fatal(err)
	}
	// Replace one key, delete another, keep the room.
	if err := s.Save(ctx, store.Save{
		World:       w.ID,
		WorldState:  []store.Change{{Key: "door", Value: []byte("shut")}, {Key: "bell", Value: nil}, {Key: "never", Value: nil}},
		Character:   c.ID,
		PlayerState: []store.Change{{Key: "hat", Value: nil}},
	}); err != nil {
		t.Fatal(err)
	}
	wv, _ := s.WorldState(ctx, w.ID)
	pv, _ := s.PlayerState(ctx, c.ID)
	if len(wv) != 1 || string(wv["door"]) != "shut" {
		t.Errorf("world state = %q", wv)
	}
	if len(pv) != 1 || !bytes.Equal(pv["nail"], []byte{0, 1, 2}) {
		t.Errorf("player state = %q", pv)
	}
	if got := room(); got != "hall" {
		t.Errorf("room = %q, want hall kept", got)
	}
	// A room with no character changes nothing.
	if err := s.Save(ctx, store.Save{World: w.ID, Room: "cellar"}); err != nil || room() != "hall" {
		t.Errorf("room without a character: %v, room %q", err, room())
	}

	// All or nothing: a player state change that fails (no such character)
	// takes the world state and room changes of the same save with it.
	err := s.Save(ctx, store.Save{
		World:       w.ID,
		WorldState:  []store.Change{{Key: "door", Value: []byte("broken")}},
		Character:   9999,
		PlayerState: []store.Change{{Key: "nail", Value: []byte("x")}},
		Room:        "cellar",
	})
	if err == nil {
		t.Fatal("save for an unknown character: want an error")
	}
	if wv, _ := s.WorldState(ctx, w.ID); string(wv["door"]) != "shut" {
		t.Errorf("a failed save wrote world state: %q", wv)
	}
	err = s.Save(ctx, store.Save{
		World:       9999,
		WorldState:  []store.Change{{Key: "door", Value: []byte("x")}},
		Character:   c.ID,
		PlayerState: []store.Change{{Key: "nail", Value: []byte("x")}},
		Room:        "cellar",
	})
	if err == nil {
		t.Fatal("save for an unknown world: want an error")
	}
	if pv, _ := s.PlayerState(ctx, c.ID); !bytes.Equal(pv["nail"], []byte{0, 1, 2}) || room() != "hall" {
		t.Errorf("a failed save wrote player state or room: %q, room %q", pv, room())
	}
	// A cancelled context writes nothing.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Save(cctx, store.Save{World: w.ID, WorldState: []store.Change{{Key: "door", Value: []byte("x")}}}); err == nil {
		t.Error("save with a cancelled context: want an error")
	}
}
