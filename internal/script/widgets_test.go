package script

import (
	"errors"
	"strings"
	"testing"
)

// Tests for the T-0004 additions: ui.select, ui.items, ui.image and hook
// arguments.

func TestSelect(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter()
  local nums, labels = ui.select("Pack", {"rope", "lamp", "bread"}, {min = 0, max = 2})
  say(#nums .. ":" .. table.concat(nums, ",") .. ":" .. table.concat(labels, ","))
end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(th, err)
	}
	d := th.Dialog
	if !d.Multiple || d.Min != 0 || d.Max != 2 || len(d.Options) != 3 {
		t.Fatalf("dialog = %+v", d)
	}
	for _, bad := range [][]int{{1, 2, 3}, {1, 1}, {0}, {4}, {-1, 2}} {
		if _, err := r.Resume(h, th, bad...); err == nil {
			t.Errorf("Resume(%v) = nil error", bad)
		}
	}
	if th, err = r.Resume(h, th, 3, 1); err != nil || th != nil {
		t.Fatal(th, err)
	}
	if got := strings.Join(h.out, "|"); got != "say:2:1,3:rope,bread" {
		t.Errorf("out = %q", got)
	}
}

func TestSelectNone(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter() local n = ui.select("?", {"a"}, {min = 0}) say("picked " .. #n) end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(th, err)
	}
	if _, err := r.Resume(h, th); err != nil {
		t.Fatal(err)
	}
	if h.out[0] != "say:picked 0" {
		t.Errorf("out = %q", h.out)
	}
}

func TestSelectDefaults(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter() ui.select("?", {"a", "b", "c"}) end`)
	th, err := r.Run(&recorder{}, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(th, err)
	}
	if d := th.Dialog; d.Min != 1 || d.Max != 3 {
		t.Errorf("dialog = %+v, want min 1 and max 3", d)
	}
}

func TestSelectBadLimits(t *testing.T) {
	for _, limits := range []string{`{min = 3, max = 2}`, `{max = 0}`, `{max = 4}`, `{min = -1}`, `{min = 1.5}`, `{min = "1"}`, `{min = 1e300}`, `{min = 0/0}`, `"x"`} {
		if _, err := hook(t, `ui.select("?", {"a", "b", "c"}, `+limits+`)`); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", limits, err)
		}
	}
}

func TestDialogCheck(t *testing.T) {
	single := Dialog{Options: []string{"a", "b"}}
	if got, err := single.Check([]int{2}); err != nil || len(got) != 1 || got[0] != 2 {
		t.Errorf("single Check(2) = %v, %v", got, err)
	}
	for _, bad := range [][]int{nil, {}, {0}, {3}, {1, 2}} {
		if _, err := single.Check(bad); err == nil {
			t.Errorf("single Check(%v) = nil error", bad)
		}
	}
	multi := Dialog{Options: []string{"a", "b", "c"}, Multiple: true, Min: 1, Max: 3}
	if got, err := multi.Check([]int{3, 1}); err != nil || len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("multi Check(3, 1) = %v, %v", got, err)
	}
}

func TestItems(t *testing.T) {
	out, err := hook(t, `ui.items({{id = "straw", label = "heap of straw"}, {id = "door-2", label = "door"}}) ui.items({})`)
	if err != nil || strings.Join(out, "|") != "items:straw=heap of straw;door-2=door;|items:" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	for _, body := range []string{
		`ui.items({{id = "Straw", label = "x"}})`,
		`ui.items({{id = "-a", label = "x"}})`,
		`ui.items({{id = "", label = "x"}})`,
		`ui.items({{id = "a", label = ""}})`,
		`ui.items({{id = "a"}})`,
		`ui.items({{id = 1, label = "x"}})`,
		`ui.items({{id = "a", label = "x"}, {id = "a", label = "y"}})`,
		`ui.items({"a"})`,
		`ui.items({{id = string.rep("a", 65), label = "x"}})`,
		`ui.items({{id = "a", label = string.rep("x", 101)}})`,
		`local t = {} for i = 1, 21 do t[i] = {id = "i" .. i, label = "x"} end ui.items(t)`,
	} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

func TestImage(t *testing.T) {
	out, err := hook(t, `ui.image("map.png", "Scratches on the wall") ui.image("map.png")`)
	if err != nil || strings.Join(out, "|") != "image:map.png|Scratches on the wall|image:map.png|" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	for _, body := range []string{`ui.image("dragon.png")`, `ui.image("map.png", string.rep("x", 201))`, `ui.image()`, `ui.image({})`} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

func TestWidgetsNeedAPlayer(t *testing.T) {
	for _, src := range []string{`ui.select("?", {"a"})`, `ui.items({})`, `ui.image("map.png")`} {
		r := New(DefaultLimits)
		if _, err := r.Load("w.lua", src); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "no player") {
			t.Errorf("%s: err = %v", src, err)
		}
		r.Close()
	}
}

func TestHookArguments(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_input(player, text) say(player.name .. ":" .. text) end`)
	h := &recorder{}
	if _, err := r.Run(h, env, "on_input", "Ada", "open door"); err != nil {
		t.Fatal(err)
	}
	if h.out[0] != "say:Ada:open door" {
		t.Errorf("out = %q", h.out)
	}
}

func TestValidItemID(t *testing.T) {
	for id, want := range map[string]bool{"a": true, "a-b_9": true, "9": true, "": false, "_a": false, "A": false, "a b": false, "ä": false, strings.Repeat("a", 64): true, strings.Repeat("a", 65): false} {
		if got := ValidItemID(id); got != want {
			t.Errorf("ValidItemID(%q) = %v", id, got)
		}
	}
}

func TestAsk(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter()
  local a = ui.ask("Spell the word you hear", {strict = true, speak = true, show = false})
  say(tostring(words.same(a, "Castle")) .. ":" .. a)
end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(th, err)
	}
	d := th.Dialog
	if !d.Ask || !d.Strict || !d.Voice.Speak || !d.Voice.Hide || d.Title != "Spell the word you hear" || len(d.Options) != 0 {
		t.Fatalf("dialog = %+v", d)
	}
	if _, err := r.Resume(h, th, 1); err == nil {
		t.Error("Resume with a choice on ui.ask = nil error")
	}
	for _, bad := range []string{"", strings.Repeat("x", MaxAnswerBytes+1)} {
		if _, err := r.Answer(h, th, bad); err == nil {
			t.Errorf("Answer(%d bytes) = nil error", len(bad))
		}
	}
	if th, err = r.Answer(h, th, "  the CASTLE"); err != nil || th != nil {
		t.Fatal(th, err)
	}
	// Only case and spaces are ignored: "the" makes it wrong.
	if h.out[0] != "say:false:  the CASTLE" {
		t.Errorf("out = %q", h.out)
	}
}

func TestAnswerRefusesChoiceDialogs(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter() ui.dialog("?", {"a"}) end`)
	th, err := r.Run(&recorder{}, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(th, err)
	}
	if _, err := r.Answer(&recorder{}, th, "a"); err == nil {
		t.Error("Answer on ui.dialog = nil error")
	}
	if r.Paused() != 1 {
		t.Error("a refused answer ended the hook")
	}
}

func TestAskArguments(t *testing.T) {
	for _, body := range []string{
		`ui.ask("")`,
		`ui.ask(string.rep("x", 201))`,
		`ui.ask("?", {strict = 1})`,
		`ui.ask("?", {show = false})`,
		`ui.ask("?", "x")`,
	} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

func TestSayVoice(t *testing.T) {
	out, err := hook(t, `say("a") say("b", {speak = true}) say("c", {speak = true, show = false}) say("d", {})`)
	if err != nil || strings.Join(out, "|") != "say:a|say:(spoken) b|say:(spoken only) c|say:d" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	for _, body := range []string{`say("x", {show = false})`, `say("x", {speak = "yes"})`, `say("x", 1)`} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

func TestWords(t *testing.T) {
	out, err := hook(t, `say(words.normalize("  The \t Castle  ")) say(tostring(words.same("CASTLE", " castle "))) say(tostring(words.same("castle", "castel")))`)
	if err != nil || strings.Join(out, "|") != "say:the castle|say:true|say:false" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	if _, err := hook(t, `words.same = nil`); !errors.Is(err, ErrScript) {
		t.Errorf("words is writable: %v", err)
	}
}
