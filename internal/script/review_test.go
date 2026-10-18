package script

import (
	"errors"
	"testing"
)

// Names and labels refuse bidi controls and line separators, which would
// turn the rest of a line around; emoji joiners stay allowed.
func TestNamesRefuseBidiControls(t *testing.T) {
	for _, tt := range []struct {
		lua string
		ok  bool
	}{
		{"say(\"x\", {from = \"Whisp\u202e\"})", false},
		{"say(\"x\", {from = \"Whisp\u2066\"})", false},
		{"say(\"x\", {from = \"Wh\u2028isp\"})", false},
		{"say(\"x\", {from = \"Whisp \U0001F468\u200d\U0001F467\"})", true},
		{"ui.items({{id = \"a\", label = \"lamp\u200f\"}})", false},
		{"ui.items({{id = \"a\", label = \"lamp \U0001F468\u200d\U0001F467\"}})", true},
		{"timer.start(\"t\", 5, {show = \"Guards\u202d\"})", false},
	} {
		_, _, err := runHook(t, roomy, `function on_enter() `+tt.lua+` end`)
		if tt.ok && err != nil || !tt.ok && !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v", tt.lua, err)
		}
	}
}
