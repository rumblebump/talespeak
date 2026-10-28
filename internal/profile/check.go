// Package profile decides which of an account's profiles may play a
// world on a ruleset, binds the world's character to one, and moves a
// profile to a new major version of its ruleset with the ruleset's Lua
// migration (T-0010). See docs/rulesets.md.
package profile

import (
	"fmt"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/store"
)

// Fit is whether a profile may play a story.
type Fit struct {
	OK bool
	// Upgrade is the ruleset version a migration moves the profile to
	// first; "" when it needs none.
	Upgrade string
	// Reason is why it may not, a sentence the player reads.
	Reason string
}

// Check reports whether profile p, with its progress, may play story
// on ruleset, the ruleset version the story's worlds run. With
// continuing set, the stories it must have finished are checked too
// (not for a character that started the story already).
//
// p fits when it is on the same ruleset and its version can be read by
// ruleset (the same major version, or 0.minor line, and not newer); or
// when it is on an older major version and ruleset carries a migration
// into every major version from there.
func Check(story, ruleset *pack.Pack, cat *pack.Catalog, p store.Profile, progress []store.Progress, continuing bool) Fit {
	rules := ruleset.Title
	if p.Ruleset != ruleset.ID {
		return Fit{Reason: fmt.Sprintf("%s is a character of %s, and this story is for %s.", p.Name, p.Ruleset, rules)}
	}
	v, err := pack.ParseVersion(p.Version)
	if err != nil {
		return Fit{Reason: fmt.Sprintf("%s has no ruleset version this server can read.", p.Name)}
	}
	fit := Fit{OK: true}
	switch {
	case pack.Compatible(v).Contains(ruleset.V):
	case ruleset.V.Compare(v) < 0:
		return Fit{Reason: fmt.Sprintf("%s plays %s %s, and this world runs the older %s.", p.Name, rules, p.Version, ruleset.Version)}
	case !migratable(v, ruleset):
		return Fit{Reason: fmt.Sprintf("Nothing moves %s from %s %s to %s.", p.Name, rules, p.Version, ruleset.Version)}
	default:
		fit.Upgrade = ruleset.Version
	}
	if continuing {
		if why := Locked(story, cat, p.ID, progress); why != "" {
			return Fit{Reason: why}
		}
	}
	return fit
}

// migratable reports whether ruleset carries a migration into each major
// version after v's, up to its own. A 0.x version has none: its minor
// versions are the lines, and migrations go by major version.
func migratable(v pack.Version, ruleset *pack.Pack) bool {
	if v.Major == 0 || v.Major >= ruleset.V.Major {
		return false
	}
	for m := v.Major + 1; m <= ruleset.V.Major; m++ {
		if _, ok := ruleset.Migrations[m]; !ok {
			return false
		}
	}
	return true
}

// Locked is why the profile with progress may not start story, the
// first story it must finish first ("Finish Forest Start first."); ""
// when it may. Profile 0 is a new profile, which has finished nothing.
func Locked(story *pack.Pack, cat *pack.Catalog, profile int64, progress []store.Progress) string {
	for _, c := range story.Continues {
		if c.Completed && !finished(c, profile, progress) {
			return "Finish " + cat.Title(c.ID) + " first."
		}
	}
	return ""
}

// finished reports whether the profile completed a version of the story
// c names in c's range.
func finished(c pack.Continue, profile int64, progress []store.Progress) bool {
	pr, ok := played(c, profile, progress)
	return ok && pr.Status == store.ProgressCompleted
}

// played returns the profile's progress of the story c names, when it
// played a version in c's range.
func played(c pack.Continue, profile int64, progress []store.Progress) (store.Progress, bool) {
	for _, pr := range progress {
		if pr.Profile != profile || pr.Pack != c.ID {
			continue
		}
		v, err := pack.ParseVersion(pr.Version)
		return pr, err == nil && c.R.Contains(v)
	}
	return store.Progress{}, false
}
