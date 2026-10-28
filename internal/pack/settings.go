package pack

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Settings (T-0018): choices a pack offers for each world, such as what
// happens when a player dies. The game master picks them in the lobby
// when the world is created and changes them with /set; scripts read them
// with state.game.get(name). See docs/content-format.md.

// Limits of a pack's settings, part of the pack format.
const (
	MaxSettings       = 20
	MaxSettingChoices = 20
	// MaxSettingName and MaxChoiceBytes bound a setting's name and each
	// of its choices: one word each, as /set NAME VALUE takes them.
	MaxSettingName = 32
	MaxChoiceBytes = 32
	// MaxSettingLabel bounds the label the lobby shows.
	MaxSettingLabel = 100
)

// Setting is one of a pack's settings in pack.yaml.
type Setting struct {
	// Name is the key in state.game: 1 to MaxSettingName of a-z, 0-9 and
	// _, starting with a letter.
	Name string `yaml:"name"`
	// Label is what the lobby shows, one line.
	Label string `yaml:"label"`
	// Choices are the values the setting takes, 2 to MaxSettingChoices
	// words of a-z, 0-9, _ and -.
	Choices []string `yaml:"choices"`
	// Default is the choice of a world nobody picked one for; "" is the
	// first choice.
	Default string `yaml:"default"`
}

var (
	settingName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	choiceWord  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// ValidChoice reports whether v is one of the setting's choices.
func (s Setting) ValidChoice(v string) bool { return slices.Contains(s.Choices, v) }

// Setting returns the pack's setting name.
func (p *Pack) Setting(name string) (Setting, bool) {
	for _, s := range p.Settings {
		if s.Name == name {
			return s, true
		}
	}
	return Setting{}, false
}

// SettingChoices are the choices of every setting by name, for
// script.State.Choices.
func (p *Pack) SettingChoices() map[string][]string {
	if len(p.Settings) == 0 {
		return nil
	}
	m := make(map[string][]string, len(p.Settings))
	for _, s := range p.Settings {
		m[s.Name] = s.Choices
	}
	return m
}

// checkSettings checks the settings of pack.yaml and sets each missing
// default to the first choice. It quotes no more of a bad value than a
// good one could be.
func checkSettings(list []Setting) []error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("pack.yaml: settings: "+format, args...))
	}
	if len(list) > MaxSettings {
		fail("%d settings, at most %d", len(list), MaxSettings)
		return errs
	}
	seen := map[string]bool{}
	for i := range list {
		s := &list[i]
		if len(s.Name) > MaxSettingName || !settingName.MatchString(s.Name) || seen[s.Name] {
			fail("name %q must be unique and 1 to %d of a-z, 0-9 and _, starting with a letter", cut(s.Name, MaxSettingName), MaxSettingName)
			continue
		}
		seen[s.Name] = true
		if strings.TrimSpace(s.Label) == "" || len(s.Label) > MaxSettingLabel || !utf8.ValidString(s.Label) || strings.ContainsAny(s.Label, "\r\n") {
			fail("%s: label must be one line of 1 to %d bytes", s.Name, MaxSettingLabel)
		}
		if len(s.Choices) < 2 || len(s.Choices) > MaxSettingChoices {
			fail("%s: 2 to %d choices", s.Name, MaxSettingChoices)
			continue
		}
		bad := false
		for j, c := range s.Choices {
			if len(c) > MaxChoiceBytes || !choiceWord.MatchString(c) || slices.Contains(s.Choices[:j], c) {
				fail("%s: choice %q must be unique and 1 to %d of a-z, 0-9, _ and -, starting with a letter or digit", s.Name, cut(c, MaxChoiceBytes), MaxChoiceBytes)
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		switch {
		case s.Default == "":
			s.Default = s.Choices[0]
		case !s.ValidChoice(s.Default):
			fail("%s: default %q is not one of its choices", s.Name, cut(s.Default, MaxChoiceBytes))
		}
	}
	return errs
}

// cut shortens s to at most n bytes for a message.
func cut(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
