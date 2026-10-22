package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/account"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
)

func TestLoadAccess(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want access
		errs []string // each must be in the error; nil: no error
	}{
		{"defaults", nil, access{"public", "self", "open", "everyone"}, nil},
		{"public", map[string]string{"TALESPEAK_MODE": "public"}, access{"public", "self", "open", "everyone"}, nil},
		{"edu defaults", map[string]string{"TALESPEAK_MODE": "edu"}, access{"edu", "off", "invite", "gm"}, nil},
		{"edu with registration", map[string]string{"TALESPEAK_MODE": "edu", "TALESPEAK_REGISTRATION": "self"}, access{"edu", "self", "invite", "gm"}, nil},
		{"edu invite said twice", map[string]string{"TALESPEAK_MODE": "edu", "TALESPEAK_WORLD_ACCESS": "invite"}, access{"edu", "off", "invite", "gm"}, nil},
		{"public closed", map[string]string{"TALESPEAK_REGISTRATION": "off", "TALESPEAK_WORLD_ACCESS": "invite"}, access{"public", "off", "invite", "gm"}, nil},
		{"public closed, creating open", map[string]string{"TALESPEAK_WORLD_ACCESS": "invite", "TALESPEAK_CREATE_WORLDS": "everyone"}, access{"public", "self", "invite", "everyone"}, nil},
		{"edu open refused", map[string]string{"TALESPEAK_MODE": "edu", "TALESPEAK_WORLD_ACCESS": "open"}, access{}, []string{"edu worlds are always invite only"}},
		{"unknown mode", map[string]string{"TALESPEAK_MODE": "school"}, access{}, []string{`TALESPEAK_MODE="school"`}},
		{"mode is case sensitive", map[string]string{"TALESPEAK_MODE": "EDU"}, access{}, []string{`TALESPEAK_MODE="EDU"`}},
		{"unknown registration", map[string]string{"TALESPEAK_REGISTRATION": "maybe"}, access{}, []string{`TALESPEAK_REGISTRATION="maybe"`}},
		{"unknown access", map[string]string{"TALESPEAK_WORLD_ACCESS": "closed"}, access{}, []string{`TALESPEAK_WORLD_ACCESS="closed"`}},
		{"public gm only", map[string]string{"TALESPEAK_CREATE_WORLDS": "gm"}, access{"public", "self", "open", "gm"}, nil},
		{"edu gm said twice", map[string]string{"TALESPEAK_MODE": "edu", "TALESPEAK_CREATE_WORLDS": "gm"}, access{"edu", "off", "invite", "gm"}, nil},
		{"edu everyone refused", map[string]string{"TALESPEAK_MODE": "edu", "TALESPEAK_CREATE_WORLDS": "everyone"}, access{}, []string{"only admins and game masters create worlds"}},
		{"unknown create", map[string]string{"TALESPEAK_CREATE_WORLDS": "all"}, access{}, []string{`TALESPEAK_CREATE_WORLDS="all"`}},
		{"both reported", map[string]string{"TALESPEAK_REGISTRATION": "x", "TALESPEAK_WORLD_ACCESS": "y"}, access{},
			[]string{`TALESPEAK_REGISTRATION="x"`, `TALESPEAK_WORLD_ACCESS="y"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadAccess(env(tt.env))
			if tt.errs == nil {
				if err != nil || got != tt.want {
					t.Errorf("loadAccess = %+v, %v; want %+v", got, err, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("loadAccess = %+v, want an error", got)
			}
			for _, want := range tt.errs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// Every mode the settings allow keeps edu worlds invite only.
func TestLoadAccessEduAlwaysInvite(t *testing.T) {
	for _, reg := range []string{"", "self", "off"} {
		for _, acc := range []string{"", "open", "invite"} {
			a, err := loadAccess(env(map[string]string{"TALESPEAK_MODE": "edu", "TALESPEAK_REGISTRATION": reg, "TALESPEAK_WORLD_ACCESS": acc}))
			if err == nil && a.world != account.AccessInvite {
				t.Errorf("edu with registration %q, access %q: world = %q", reg, acc, a.world)
			}
		}
	}
}

func openDB(t *testing.T, dir string) *sqlite.Store {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "talespeak.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func runAccount(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := accountCmd(args, strings.NewReader(stdin), &out)
	return out.String(), err
}

func checkPassword(t *testing.T, db *sqlite.Store, name, password string) bool {
	t.Helper()
	_, hash, err := db.AccountByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := account.Verify(context.Background(), hash, password)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestAccountAdd(t *testing.T) {
	integration(t)
	dir := t.TempDir()
	out, err := runAccount(t, "correct horse\n", "add", "-data", dir, "Mira")
	if err != nil || out != "added Mira (player)\n" {
		t.Fatalf("add = %q, %v", out, err)
	}
	if out, err := runAccount(t, "correct horse\r\n", "add", "-data", dir, "-admin", "Zed"); err != nil || out != "added Zed (admin)\n" {
		t.Fatalf("add -admin = %q, %v", out, err)
	}
	db := openDB(t, dir)
	if !checkPassword(t, db, "mira", "correct horse") || !checkPassword(t, db, "zed", "correct horse") {
		t.Error("the stored hash does not match the password on standard input (without its line end)")
	}
	if a, _, _ := db.AccountByName(context.Background(), "zed"); a.Role != store.RoleAdmin {
		t.Errorf("zed role = %q", a.Role)
	}
	if _, err := runAccount(t, "correct horse\n", "add", "-data", dir, "MIRA"); err == nil || !strings.Contains(err.Error(), "taken") {
		t.Errorf("same name in other case: %v", err)
	}

	// Only the first line is the password; spaces in it are kept.
	if _, err := runAccount(t, "  two words  \nsecond line\n", "add", "-data", dir, "Ann"); err != nil {
		t.Fatal(err)
	}
	if !checkPassword(t, db, "Ann", "  two words  ") {
		t.Error("password is not the whole first line")
	}
	// No line end at all is fine.
	if _, err := runAccount(t, "correct horse", "add", "-data", dir, "Bob"); err != nil || !checkPassword(t, db, "Bob", "correct horse") {
		t.Errorf("password without a line end: %v", err)
	}
	if _, err := runAccount(t, strings.Repeat("x", account.MaxPassword)+"\n", "add", "-data", dir, "Cat"); err != nil {
		t.Errorf("password of %d bytes: %v", account.MaxPassword, err)
	}

	for _, tt := range []struct {
		name, stdin string
		args        []string
		want        string
	}{
		{"short password", "short\n", []string{"add", "-data", dir, "Dan"}, "at least 8"},
		{"empty stdin", "", []string{"add", "-data", dir, "Dan"}, "at least 8"},
		{"long password", strings.Repeat("x", account.MaxPassword+1) + "\n", []string{"add", "-data", dir, "Dan"}, "at most 128"},
		{"huge password", strings.Repeat("x", 1<<20), []string{"add", "-data", dir, "Dan"}, "at most 128"},
		{"bad name", "correct horse\n", []string{"add", "-data", dir, "1dan"}, "start with a letter"},
		{"flag after the name", "correct horse\n", []string{"add", "-data", dir, "Dan", "-admin"}, "want 1 argument"},
		{"two names", "correct horse\n", []string{"add", "-data", dir, "Dan", "Eve"}, "want 1 argument"},
		{"no name", "correct horse\n", []string{"add", "-data", dir}, "want 1 argument"},
	} {
		if _, err := runAccount(t, tt.stdin, tt.args...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want %q", tt.name, err, tt.want)
		}
	}
	if _, _, err := db.AccountByName(context.Background(), "Dan"); err == nil {
		t.Error("a refused add made the account")
	}
}

func TestAccountPasswordAndRole(t *testing.T) {
	integration(t)
	dir := t.TempDir()
	if _, err := runAccount(t, "correct horse\n", "add", "-data", dir, "Mira"); err != nil {
		t.Fatal(err)
	}
	db := openDB(t, dir)
	ctx := context.Background()
	a, _, _ := db.AccountByName(ctx, "Mira")
	token := bytes.Repeat([]byte{1}, 32)
	if err := db.CreateLogin(ctx, token, a.ID, time.Now(), time.Now().Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}

	out, err := runAccount(t, "battery staple\n", "password", "-data", dir, "mira")
	if err != nil || !strings.Contains(out, "changed the password of Mira") {
		t.Fatalf("password = %q, %v", out, err)
	}
	if !checkPassword(t, db, "Mira", "battery staple") || checkPassword(t, db, "Mira", "correct horse") {
		t.Error("the password did not change")
	}
	if _, err := db.LoginAccount(ctx, token, time.Now()); err == nil {
		t.Error("a password change left a session")
	}
	if _, err := runAccount(t, "short\n", "password", "-data", dir, "Mira"); err == nil || !checkPassword(t, db, "Mira", "battery staple") {
		t.Errorf("a short new password: %v", err)
	}
	if _, err := runAccount(t, "battery staple\n", "password", "-data", dir, "nobody"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("password of an unknown account: %v", err)
	}

	for _, tt := range []struct {
		role, out string
	}{{"admin", "Mira is now admin\n"}, {"player", "Mira is now player\n"}, {"admin", "Mira is now admin\n"}} {
		out, err := runAccount(t, "", "role", "-data", dir, "MIRA", tt.role)
		if err != nil || out != tt.out {
			t.Errorf("role %s = %q, %v", tt.role, out, err)
		}
		if got, _, _ := db.AccountByName(ctx, "Mira"); got.Role != tt.role {
			t.Errorf("stored role = %q, want %q", got.Role, tt.role)
		}
	}
	for _, args := range [][]string{
		{"role", "-data", dir, "Mira", "gm"},
		{"role", "-data", dir, "Mira", "Admin"},
		{"role", "-data", dir, "nobody", "admin"},
		{"role", "-data", dir, "Mira"},
	} {
		if _, err := runAccount(t, "", args...); err == nil {
			t.Errorf("%q: want an error", args)
		}
	}
}

// Bad sub-commands fail before touching the data directory.
func TestAccountBadArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never")
	for _, tt := range []struct {
		args []string
		want string
	}{
		{nil, "want add, password or role"},
		{[]string{"frob", "-data", dir, "x"}, `unknown command "frob"`},
		{[]string{"delete", "-data", dir, "x"}, "unknown command"},
		{[]string{"add", "-data", dir, "-bogus", "x"}, "bogus"},
	} {
		if _, err := runAccount(t, "", tt.args...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: %v, want %q", tt.args, err, tt.want)
		}
	}
	if err := run([]string{"account"}); err == nil {
		t.Error("run account: want an error")
	}
	if m, _ := filepath.Glob(dir); len(m) != 0 {
		t.Error("a bad command made the data directory")
	}
}

// TALESPEAK_DATA is the default data directory.
func TestAccountDataFromEnv(t *testing.T) {
	integration(t)
	dir := t.TempDir()
	t.Setenv("TALESPEAK_DATA", dir)
	if _, err := runAccount(t, "correct horse\n", "add", "Mira"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openDB(t, dir).AccountByName(context.Background(), "Mira"); err != nil {
		t.Errorf("account not in TALESPEAK_DATA: %v", err)
	}
}

var codeLine = regexp.MustCompile(`^([0-9A-Z]{5}-[0-9A-Z]{5})  \((player|gm) of world (\d+), (\S+), (\d+) use\(s\), expires (\S+)\)\n$`)

// joinWith registers name on a test API of the database and joins with
// code; it returns the status and the role the account then has in the
// world the code joined.
func joinWith(t *testing.T, db *sqlite.Store, name, code string) (int, string) {
	t.Helper()
	ctx := context.Background()
	svc := account.New(account.Config{Store: db, Registration: account.RegistrationSelf, Packs: []account.Pack{{ID: "demo", Title: "Demo"}},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/register", "application/json", strings.NewReader(`{"name":"`+name+`","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/join", strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(resp.Cookies()[0])
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var me account.Me
	_ = json.NewDecoder(resp.Body).Decode(&me)
	if me.Joined == 0 {
		return resp.StatusCode, ""
	}
	a, _, _ := db.AccountByName(ctx, name)
	role, _ := db.Member(ctx, me.Joined, a.ID)
	return resp.StatusCode, role
}

// talespeak invite prints a code to the world the server runs, which then
// lets accounts in.
func TestInviteCmd(t *testing.T) {
	integration(t)
	t.Setenv("TALESPEAK_PACK", "")
	dir := t.TempDir()
	var out bytes.Buffer
	if err := inviteCmd([]string{"-data", dir, "-uses", "2", "-days", "3"}, &out); err != nil {
		t.Fatal(err)
	}
	m := codeLine.FindStringSubmatch(out.String())
	if m == nil || m[2] != "player" || m[3] != "1" || m[4] != "demo" || m[5] != "2" {
		t.Fatalf("output = %q", out.String())
	}
	exp, err := time.Parse(time.RFC3339, m[6])
	if err != nil || exp.Sub(time.Now()) < 71*time.Hour || exp.Sub(time.Now()) > 73*time.Hour {
		t.Errorf("expires %q (%v), want in 3 days", m[6], err)
	}
	db := openDB(t, dir)
	for i, name := range []string{"Ann", "Bob", "Cat"} {
		status, role := joinWith(t, db, name, m[1])
		if want := 200; i == 2 {
			if status != 400 {
				t.Errorf("third use of a two-use code: %d", status)
			}
		} else if status != want || role != "player" {
			t.Errorf("%s joins: %d, role %q", name, status, role)
		}
	}

	out.Reset()
	if err := inviteCmd([]string{"-data", dir, "-gm"}, &out); err != nil {
		t.Fatal(err)
	}
	m = codeLine.FindStringSubmatch(out.String())
	if m == nil || m[2] != "gm" || m[5] != "1" {
		t.Fatalf("gm output = %q", out.String())
	}
	if status, role := joinWith(t, db, "Gwen", m[1]); status != 200 || role != "gm" {
		t.Errorf("gm joins: %d, role %q", status, role)
	}

	// -world names another world.
	ctx := context.Background()
	gwen, _, _ := db.AccountByName(ctx, "Gwen")
	w, err := db.CreateWorld(ctx, store.NewWorld{PackID: "test-pack", Visibility: store.VisibilityPrivate, CreatedBy: gwen.ID})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := inviteCmd([]string{"-data", dir, "-world", strconv.FormatInt(w.ID, 10)}, &out); err != nil {
		t.Fatal(err)
	}
	m = codeLine.FindStringSubmatch(out.String())
	if m == nil || m[3] != strconv.FormatInt(w.ID, 10) || m[4] != "test-pack" {
		t.Fatalf("-world output = %q", out.String())
	}
	if status, role := joinWith(t, db, "Hal", m[1]); status != 200 || role != "player" {
		t.Errorf("joins the other world: %d, role %q", status, role)
	}
	if err := db.FinishWorld(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{strconv.FormatInt(w.ID, 10), "9999"} {
		out.Reset()
		if err := inviteCmd([]string{"-data", dir, "-world", id}, &out); err == nil || !strings.Contains(err.Error(), "does not exist or has ended") || out.Len() != 0 {
			t.Errorf("-world %s: %v, %q", id, err, out.String())
		}
	}
}

func TestInviteCmdRefuses(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-data", dir, "-uses", "0"}, "-uses from 1 to 100"},
		{[]string{"-data", dir, "-uses", "101"}, "-uses from 1 to 100"},
		{[]string{"-data", dir, "-days", "0"}, "-days from 1 to 30"},
		{[]string{"-data", dir, "-days", "31"}, "-days from 1 to 30"},
		{[]string{"-data", dir, "-days", "-1"}, "-days from 1 to 30"},
		{[]string{"-data", dir, "extra"}, "unexpected arguments"},
		{[]string{"-data", dir, "-uses", "lots"}, "invalid value"},
		{[]string{"-data", dir, "-world", "-1"}, "-uses from 1 to 100"},
		{[]string{"-data", dir, "-world", "x"}, "invalid value"},
	} {
		var out bytes.Buffer
		if err := inviteCmd(tt.args, &out); err == nil || !strings.Contains(err.Error(), tt.want) || out.Len() != 0 {
			t.Errorf("%q: %v, output %q; want %q", tt.args, err, out.String(), tt.want)
		}
	}
	integration(t)
	t.Setenv("TALESPEAK_PACK", filepath.Join(dir, "no-such-pack"))
	var out bytes.Buffer
	if err := inviteCmd([]string{"-data", dir}, &out); err == nil || out.Len() != 0 {
		t.Errorf("missing pack: %v, %q", err, out.String())
	}
	t.Setenv("TALESPEAK_PACK", "")
	t.Setenv("TALESPEAK_LUA_TIME", "forever")
	if err := inviteCmd([]string{"-data", dir}, &out); err == nil || !strings.Contains(err.Error(), "limits:") {
		t.Errorf("bad limits: %v", err)
	}
}
