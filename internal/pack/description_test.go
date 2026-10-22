package pack

import (
	"strings"
	"testing"
)

// pack.yaml's description is optional, at most MaxPackDescriptionBytes,
// and linted like every other text a player sees.
func TestPackDescription(t *testing.T) {
	withDesc := func(d string) map[string]*string {
		return map[string]*string{"pack.yaml": s(goodPack + "description: " + d + "\n")}
	}
	p, err := Load(fsWith(nil))
	if err != nil || p.Description != "" {
		t.Fatalf("no description: %q, %v", p.Description, err)
	}
	longest := strings.Repeat("x", MaxPackDescriptionBytes)
	if p, err := Load(fsWith(withDesc(longest))); err != nil || p.Description != longest {
		t.Errorf("longest description: %v", err)
	}
	for name, d := range map[string]string{
		"too long":                 strings.Repeat("x", MaxPackDescriptionBytes+1),
		"too long, two bytes each": strings.Repeat("\u00f6", MaxPackDescriptionBytes/2+1), // bytes, not characters
		"a reference":              "Pictures at https://evil.example/map.png",
	} {
		if _, err := Load(fsWith(withDesc(`"` + d + `"`))); err == nil {
			t.Errorf("%s: loaded", name)
		} else if !strings.Contains(err.Error(), "pack.yaml") {
			t.Errorf("%s: error %v does not name pack.yaml", name, err)
		}
	}
}
