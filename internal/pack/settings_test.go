package pack

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/content"
)

// loadSettings loads goodPack with settingsYAML appended.
func loadSettings(settingsYAML string) (*Pack, error) {
	return Load(fstest.MapFS{
		"pack.yaml":    {Data: []byte(goodPack + settingsYAML)},
		"rooms/r.yaml": {Data: []byte(goodRoom)},
	})
}

func TestSettings(t *testing.T) {
	long := strings.Repeat("a", MaxSettingName+1)
	tests := []struct {
		name, yaml, want string // want "" loads
	}{
		{"none", "", ""},
		{"one", "settings:\n  - {name: death, label: Death, choices: [stay, revive]}\n", ""},
		{"default", "settings:\n  - {name: death, label: Death, choices: [stay, revive], default: revive}\n", ""},
		{"bad default", "settings:\n  - {name: death, label: Death, choices: [stay, revive], default: zombie}\n", `default "zombie" is not one of its choices`},
		{"one choice", "settings:\n  - {name: death, label: Death, choices: [stay]}\n", "2 to 20 choices"},
		{"same choice", "settings:\n  - {name: death, label: Death, choices: [stay, stay]}\n", `choice "stay" must be unique`},
		{"spaced choice", "settings:\n  - {name: death, label: Death, choices: [stay, \"stay dead\"]}\n", "must be unique and 1 to 32"},
		{"upper case name", "settings:\n  - {name: Death, label: Death, choices: [a, b]}\n", `name "Death"`},
		{"long name", "settings:\n  - {name: " + long + ", label: X, choices: [a, b]}\n", `name "` + long[:MaxSettingName] + `..."`},
		{"same name", "settings:\n  - {name: d, label: D, choices: [a, b]}\n  - {name: d, label: E, choices: [a, b]}\n", `name "d" must be unique`},
		{"no label", "settings:\n  - {name: d, choices: [a, b]}\n", "label must be one line"},
		{"two-line label", "settings:\n  - {name: d, label: \"a\\nb\", choices: [a, b]}\n", "label must be one line"},
		{"unknown field", "settings:\n  - {name: d, label: D, choices: [a, b], help: x}\n", "field help not found"},
		{"label with a URL", "settings:\n  - {name: d, label: see https://x.example, choices: [a, b]}\n", "https://x.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := loadSettings(tt.yaml)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range p.Settings {
					if !s.ValidChoice(s.Default) {
						t.Errorf("default %q of %s", s.Default, s.Name)
					}
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSettingsCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("settings:\n")
	for i := range MaxSettings + 1 {
		b.WriteString("  - {name: s" + string(rune('a'+i)) + ", label: S, choices: [a, b]}\n")
	}
	if _, err := loadSettings(b.String()); err == nil || !strings.Contains(err.Error(), "21 settings, at most 20") {
		t.Errorf("err = %v", err)
	}
}

// The first choice is the default when none is given; the demo's death
// setting respawns by default.
func TestSettingDefaults(t *testing.T) {
	p, err := loadSettings("settings:\n  - {name: d, label: D, choices: [b, a]}\n")
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := p.Setting("d"); !ok || s.Default != "b" {
		t.Errorf("Setting(d) = %+v, %v", s, ok)
	}
	if _, ok := p.Setting("x"); ok {
		t.Error("Setting(x) found")
	}
	if c := p.SettingChoices(); len(c) != 1 || strings.Join(c["d"], ",") != "b,a" {
		t.Errorf("SettingChoices = %v", c)
	}
	demo, err := Load(content.Demo())
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := demo.Setting("death"); !ok || s.Default != "respawn" || strings.Join(s.Choices, ",") != "respawn,revive,stay" {
		t.Errorf("demo death = %+v, %v", s, ok)
	}
}

// FuzzSettings feeds outside settings YAML to the loader: it must not
// panic, and settings it accepts follow docs/content-format.md.
func FuzzSettings(f *testing.F) {
	f.Add("settings:\n  - {name: death, label: Death, choices: [stay, revive, respawn], default: revive}\n")
	f.Add("settings:\n  - {name: d, label: D, choices: [a, a]}\n")
	f.Add("settings: [{name: d, label: \"\\u202e\", choices: [\"\", b]}]\n")
	f.Add("settings:\n  - name: d\n    label: &l L\n    choices: [*l, b]\n")
	f.Add("settings: {name: d}\n")
	f.Fuzz(func(t *testing.T, yaml string) {
		p, err := loadSettings(yaml)
		if err != nil {
			return
		}
		if len(p.Settings) > MaxSettings {
			t.Fatalf("%d settings", len(p.Settings))
		}
		seen := map[string]bool{}
		for _, s := range p.Settings {
			if seen[s.Name] || len(s.Name) > MaxSettingName || !settingName.MatchString(s.Name) {
				t.Fatalf("accepted name %q", s.Name)
			}
			seen[s.Name] = true
			if strings.TrimSpace(s.Label) == "" || len(s.Label) > MaxSettingLabel || strings.ContainsAny(s.Label, "\r\n") {
				t.Fatalf("accepted label %q", s.Label)
			}
			if len(s.Choices) < 2 || len(s.Choices) > MaxSettingChoices || !s.ValidChoice(s.Default) {
				t.Fatalf("accepted choices %q, default %q", s.Choices, s.Default)
			}
			for _, c := range s.Choices {
				if len(c) > MaxChoiceBytes || !choiceWord.MatchString(c) {
					t.Fatalf("accepted choice %q", c)
				}
			}
		}
	})
}
