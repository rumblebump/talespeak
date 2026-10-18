package script

import (
	"errors"
	"strings"
	"testing"
)

// A line, a dialog, a multiple choice and a typed question can name who
// says them, such as the guide (T-0027).
func TestFromNamesTheSpeaker(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter()
  say("Hello.", {from = "Whisp"})
  say("Listen.", {from = "Whisp", speak = true})
  ui.dialog("Go back?", {"Yes", "No"}, {from = "Whisp"})
  ui.select("Pick", {"a", "b"}, {min = 1, from = "Whisp"})
  ui.ask("Your name?", {from = "Whisp", strict = true})
end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatalf("th = %v, err = %v", th, err)
	}
	if got := strings.Join(h.out, "|"); got != "say:[Whisp] Hello.|say:[Whisp] (spoken) Listen." {
		t.Errorf("out = %q", got)
	}
	if th.Dialog.Voice.From != "Whisp" {
		t.Errorf("dialog from = %q", th.Dialog.Voice.From)
	}
	if th, err = r.Resume(h, th, 1); err != nil || !th.Dialog.Multiple || th.Dialog.Voice.From != "Whisp" || th.Dialog.Min != 1 {
		t.Fatalf("select: %+v, %v", th, err)
	}
	if th, err = r.Resume(h, th, 2); err != nil || !th.Dialog.Ask || !th.Dialog.Strict || th.Dialog.Voice.From != "Whisp" {
		t.Fatalf("ask: %+v, %v", th, err)
	}
	r.Cancel(th)
}

func TestFromErrors(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	for _, tt := range []struct{ body, want string }{
		{`say("x", {from = ""})`, "from must be a name of 1 to 40 bytes on one line"},
		{`say("x", {from = "  "})`, "from must be a name"},
		{`say("x", {from = string.rep("w", 41)})`, "from must be a name"},
		{`say("x", {from = "Whi\nsp"})`, "from must be a name"},
		{`say("x", {from = "\255"})`, "from must be a name"},
		{`say("x", {from = 3})`, "from must be a string"},
		{`ui.dialog("t", {"a"}, {from = true})`, "from must be a string"},
		{`ui.dialog("t", {"a"}, "Whisp")`, "table expected"},
		{`ui.select("t", {"a"}, {from = ""})`, "from must be a name"},
		{`ui.ask("q", {from = {}})`, "from must be a string"},
	} {
		env := load(t, r, "function on_enter() "+tt.body+" end")
		if _, err := r.Run(&recorder{}, env, "on_enter", "p"); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.body, err, tt.want)
		}
	}
	// 40 bytes, in any script, is fine.
	env := load(t, r, `function on_enter() say("x", {from = string.rep("é", 20)}) end`)
	if _, err := r.Run(&recorder{}, env, "on_enter", "p"); err != nil {
		t.Errorf("40 bytes: %v", err)
	}
}
