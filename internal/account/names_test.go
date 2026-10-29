package account

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

func TestValidName(t *testing.T) {
	for name, ok := range map[string]bool{
		"Mira": true, "abc": true, "a_b-c9": true, strings.Repeat("a", 20): true,
		"ab": false, strings.Repeat("a", 21): false, "": false,
		"9mira": false, "_mira": false, "-mira": false, "mi ra": false, "mira!": false,
		"mïra": false, "Мира": false, "mira\x00": false,
	} {
		if err := ValidName(name); (err == nil) != ok {
			t.Errorf("ValidName(%q) = %v, want ok = %v", name, err, ok)
		}
	}
}

func TestValidPassword(t *testing.T) {
	for _, tt := range []struct {
		pw string
		ok bool
	}{
		{strings.Repeat("x", MinPassword), true},
		{strings.Repeat("x", MinPassword-1), false},
		{strings.Repeat("x", MaxPassword), true},
		{strings.Repeat("x", MaxPassword+1), false},
		{"", false},
		{strings.Repeat("é", MinPassword/2), true}, // bytes, not letters
	} {
		if err := ValidPassword(tt.pw); (err == nil) != tt.ok {
			t.Errorf("ValidPassword(%d bytes) = %v, want ok = %v", len(tt.pw), err, tt.ok)
		}
	}
}

// fakeWorlds records the invite CreateInvite stores.
type fakeWorlds struct {
	store.Worlds
	got store.Invite
	err error
}

func (f *fakeWorlds) CreateInvite(_ context.Context, inv store.Invite, _ time.Time) error {
	f.got = inv
	return f.err
}

// CreateInvite shows a code of two groups of five that normalizeCode
// reads back, and stores only its hash, with the expiry in whole seconds.
func TestCreateInviteCode(t *testing.T) {
	f := &fakeWorlds{}
	exp := time.Date(2026, 10, 25, 12, 0, 0, 999, time.UTC)
	seen := map[string]bool{}
	for range 50 {
		code, gotExp, err := CreateInvite(context.Background(), f, 7, 3, store.MemberGM, 5, time.Now(), exp)
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 11 || code[5] != '-' {
			t.Fatalf("code = %q, want XXXXX-XXXXX", code)
		}
		for _, r := range strings.ReplaceAll(code, "-", "") {
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("code %q has %q, not in the alphabet", code, r)
			}
		}
		norm, ok := normalizeCode(code)
		if !ok || string(codeHash(norm)) != string(f.got.CodeHash) {
			t.Fatalf("stored hash is not the hash of the shown code %q", code)
		}
		if strings.Contains(string(f.got.CodeHash), norm) {
			t.Fatal("the code is stored in the clear")
		}
		if f.got.WorldID != 7 || f.got.CreatedBy != 3 || f.got.Role != store.MemberGM || f.got.Uses != 5 ||
			!f.got.Expires.Equal(exp.Truncate(time.Second)) || !gotExp.Equal(f.got.Expires) {
			t.Fatalf("stored invite = %+v, expires %v", f.got, gotExp)
		}
		if seen[code] {
			t.Fatalf("code %q came twice", code)
		}
		seen[code] = true
	}
	f.err = errors.New("disk full")
	if code, _, err := CreateInvite(context.Background(), f, 7, 3, store.MemberPlayer, 1, time.Now(), exp); err == nil || code != "" {
		t.Errorf("store error: code %q, err %v", code, err)
	}
}

// FuzzNormalizeCode: an accepted code is 10 symbols of the alphabet, and
// normalizing it again changes nothing; input over 40 bytes is refused.
func FuzzNormalizeCode(f *testing.F) {
	for _, s := range []string{"ABCDE-FGHJK", "abcde fghjk", "0OIl1-22222", "", "ABCDU-FGHJK", "ǅ", "ı", "ﬀ", strings.Repeat(" ", 41)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, ok := normalizeCode(s)
		if !ok {
			if got != "" {
				t.Fatalf("refused %q but returned %q", s, got)
			}
			return
		}
		if len(s) > 40 {
			t.Fatalf("accepted %d bytes", len(s))
		}
		if len(got) != codeLen {
			t.Fatalf("normalizeCode(%q) = %q, not %d symbols", s, got, codeLen)
		}
		for _, r := range got {
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("normalizeCode(%q) = %q, has %q", s, got, r)
			}
		}
		if again, ok := normalizeCode(got); !ok || again != got {
			t.Fatalf("normalizing %q again = %q, %v", got, again, ok)
		}
		shown := got[:5] + "-" + got[5:]
		if again, ok := normalizeCode(strings.ToLower(shown)); !ok || again != got {
			t.Fatalf("the shown form %q in lower case = %q, %v", shown, again, ok)
		}
	})
}

// Self-registration refuses staff names with a reason, and takes names
// that only look like them (script.ReservedName).
func TestRegisterStaffNames(t *testing.T) {
	f := newFixture(t, Config{})
	for _, name := range []string{"GM_Mira", "SysOp", "Mod_Anna", "TheGM", "Server-2"} {
		var e map[string]string
		if code := f.browser().post("/api/register", `{"name":"`+name+`","password":"correct horse"}`, &e); code != 400 || !strings.Contains(e["error"], "staff") {
			t.Errorf("%s: %d %v", name, code, e)
		}
	}
	for _, name := range []string{"Badminton", "Gemma", "Modest", "Systemic"} {
		f.signUp(name)
	}
}
