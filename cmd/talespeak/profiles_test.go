package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/store"
)

const rulesetTest = "../../content/ruleset-test"

// The ruleset test packs load; the lobby offers the stories, not the
// rulesets; a story whose ruleset is missing, or a ruleset as the main
// pack, stops the server.
func TestLoadRulesetPacks(t *testing.T) {
	ps, err := loadPacks(env(map[string]string{"TALESPEAK_PACKS": rulesetTest}), pack.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range ps.lobby() {
		ids = append(ids, p.ID)
	}
	if got := strings.Join(ids, " "); got != "demo dragon-isle forest-start sky-tower" {
		t.Errorf("lobby packs = %q", got)
	}
	if r := ps.ruleset(ps.cat.Newest("sky-tower")); r == nil || r.Version != "2.0.0" {
		t.Errorf("sky-tower ruleset = %v", r)
	}
	if r := ps.ruleset(ps.cat.Newest("demo")); r != nil {
		t.Errorf("demo ruleset = %v", r)
	}

	only := t.TempDir()
	if err := os.CopyFS(filepath.Join(only, "sky-tower"), os.DirFS(filepath.Join(rulesetTest, "sky-tower"))); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(only, "tiny-rules"), os.DirFS(filepath.Join(rulesetTest, "tiny-rules"))); err != nil {
		t.Fatal(err)
	}
	_, err = loadPacks(env(map[string]string{"TALESPEAK_PACKS": only}), pack.DefaultLimits)
	if err == nil || !strings.Contains(err.Error(), "sky-tower 1.0.0 needs ruleset tiny-rules ^2.0") {
		t.Errorf("missing ruleset: %v", err)
	}
	_, err = loadPacks(env(map[string]string{"TALESPEAK_PACK": filepath.Join(rulesetTest, "tiny-rules")}), pack.DefaultLimits)
	if err == nil || !strings.Contains(err.Error(), "is a ruleset pack") {
		t.Errorf("ruleset as the main pack: %v", err)
	}
}

func runProfile(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := profileCmd(args, &out)
	return out.String(), err
}

func TestProfileCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TALESPEAK_DATA", dir)
	db := openDB(t, dir)
	ctx := context.Background()
	ann, err := db.CreateAccount(ctx, "Ann", "x", store.RolePlayer)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := runProfile(t, "list", "ann"); err != nil || out != "Ann has no profiles\n" {
		t.Errorf("list empty = %q, %v", out, err)
	}
	p, err := db.CreateProfile(ctx, ann.ID, "Hero", "tiny-rules", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateProfile(ctx, p.ID, 0, "1.0.0", "2.0.0", map[string][]byte{"stats": {2}}); err != nil {
		t.Fatal(err)
	}
	if out, err := runProfile(t, "list", "Ann"); err != nil || out != "1  Hero  (tiny-rules 2.0.0, undo goes back to 1.0.0)\n" {
		t.Errorf("list = %q, %v", out, err)
	}
	if out, err := runProfile(t, "undo", "1"); err != nil || out != "Hero is back on tiny-rules 1.0.0, as before its last migration\n" {
		t.Errorf("undo = %q, %v", out, err)
	}
	if _, err := runProfile(t, "undo", "1"); err == nil || !strings.Contains(err.Error(), "no migration to undo") {
		t.Errorf("undo twice = %v", err)
	}
	for _, args := range [][]string{{}, {"drop", "1"}, {"undo"}, {"undo", "x"}, {"undo", "-3"}, {"list", "nobody"}, {"list", "a", "b"}} {
		if _, err := runProfile(t, args...); err == nil {
			t.Errorf("%q: no error", args)
		}
	}
}
