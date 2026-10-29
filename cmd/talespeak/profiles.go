package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	"github.com/rumblebump/talespeak/internal/hub"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/profile"
	"github.com/rumblebump/talespeak/internal/store"
)

// Profiles and rulesets (T-0010, docs/rulesets.md): the ruleset a story
// runs on, the profile service of the lobby, and `talespeak profile`.

// ruleset is the ruleset version story's worlds run on; nil for a story
// without one. Startup checked that one is installed (Catalog.Check).
func (ps packs) ruleset(story *pack.Pack) *pack.Pack {
	r, _ := ps.cat.Ruleset(story)
	return r
}

// profiles is the lobby's profile service.
func profiles(db store.Profiles, ps packs, worlds *hub.Hub, lim limits, log *slog.Logger) *profile.Service {
	return profile.New(profile.Config{Store: db, Packs: ps.cat, Runs: worlds.Pack, Limits: lim.script, Log: log})
}

// profileCmd runs "talespeak profile list NAME" and "talespeak profile
// undo ID".
func profileCmd(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("profile: want list or undo")
	}
	sub, args := args[0], args[1:]
	fl := flag.NewFlagSet("profile "+sub, flag.ContinueOnError)
	data := fl.String("data", envOr("TALESPEAK_DATA", "./data"), "data directory")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if sub != "list" && sub != "undo" {
		return fmt.Errorf("profile: unknown command %q (want list or undo)", sub)
	}
	if fl.NArg() != 1 {
		return fmt.Errorf("profile %s: want 1 argument, got %q", sub, fl.Args())
	}
	ctx := context.Background()
	db, err := openData(ctx, *data)
	if err != nil {
		return err
	}
	defer db.Close()
	switch sub {
	case "list":
		a, _, err := db.AccountByName(ctx, fl.Arg(0))
		if err != nil {
			return fmt.Errorf("profile list %q: %w", fl.Arg(0), err)
		}
		list, err := db.AccountProfiles(ctx, a.ID)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Fprintf(stdout, "%s has no profiles\n", a.Name)
		}
		for _, p := range list {
			undo := ""
			if p.Undo != "" {
				undo = ", undo goes back to " + p.Undo
			}
			fmt.Fprintf(stdout, "%d  %s  (%s %s%s)\n", p.ID, p.Name, p.Ruleset, p.Version, undo)
		}
	case "undo":
		id, err := strconv.ParseInt(fl.Arg(0), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("profile undo: want a profile id from `talespeak profile list`, got %q", fl.Arg(0))
		}
		p, err := db.UndoProfile(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("profile undo: profile %d has no migration to undo", id)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s is back on %s %s, as before its last migration\n", p.Name, p.Ruleset, p.Version)
	}
	return nil
}
