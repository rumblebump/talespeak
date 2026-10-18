package script

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Edge cases of the T-0004 functions: limits at their bounds, waiting and
// leaving in ui.ask and ui.select, and the output budget.

func TestAskNeedsAPlayer(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	if _, err := r.Load("w.lua", `ui.ask("?")`); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "no player") {
		t.Errorf("ui.ask at the top level: err = %v", err)
	}
}

func TestAskVoiceOptions(t *testing.T) {
	tests := []struct {
		call                string
		strict, speak, hide bool
	}{
		{`ui.ask("?")`, false, false, false},
		{`ui.ask("?", nil)`, false, false, false},
		{`ui.ask("?", {})`, false, false, false},
		{`ui.ask("?", {strict = false})`, false, false, false},
		{`ui.ask("?", {speak = true})`, false, true, false},
		{`ui.ask("?", {speak = true, show = true})`, false, true, false},
		{`ui.ask("?", {strict = true, speak = true, show = false})`, true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.call, func(t *testing.T) {
			_, th, err := runHook(t, DefaultLimits, `function on_enter() `+tt.call+` end`)
			if err != nil || th == nil {
				t.Fatal(th, err)
			}
			d := th.Dialog
			if !d.Ask || d.Multiple || d.Strict != tt.strict || d.Voice.Speak != tt.speak || d.Voice.Hide != tt.hide {
				t.Errorf("dialog = %+v", d)
			}
		})
	}
}

func TestAskTitleBounds(t *testing.T) {
	_, th, err := runHook(t, DefaultLimits, `function on_enter() ui.ask(string.rep("q", 200)) end`)
	if err != nil || th == nil || len(th.Dialog.Title) != MaxTitleBytes {
		t.Fatalf("200-byte question: th = %v, err = %v", th, err)
	}
	// A number is a string to Lua's CheckString.
	if _, th, err := runHook(t, DefaultLimits, `function on_enter() ui.ask(42) end`); err != nil || th == nil || th.Dialog.Title != "42" {
		t.Errorf("number question: th = %v, err = %v", th, err)
	}
	for _, body := range []string{`ui.ask()`, `ui.ask(nil)`, `ui.ask({})`, `ui.ask("?", {speak = 1})`, `ui.ask("?", {show = "no"})`} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

func TestAnswerBounds(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter() local a = ui.ask("?") say(#a .. ":" .. a:sub(1, 3)) end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(th, err)
	}
	// Refused answers leave the hook waiting.
	for _, bad := range []string{"", strings.Repeat("é", MaxAnswerBytes/2+1)} {
		if _, err := r.Answer(h, th, bad); !errors.Is(err, ErrAnswer) {
			t.Errorf("Answer(%d bytes) = %v, want ErrAnswer", len(bad), err)
		}
		if r.Paused() != 1 {
			t.Fatalf("refused answer of %d bytes ended the hook", len(bad))
		}
	}
	if th, err = r.Answer(h, th, strings.Repeat("abc", MaxAnswerBytes/3)+"ab"); err != nil || th != nil {
		t.Fatal(th, err)
	}
	if strings.Join(h.out, "|") != "say:500:abc" {
		t.Errorf("out = %q", h.out)
	}
	// A hook that is not waiting is a script error, not a refused answer.
	if _, err := r.Answer(h, th, "late"); !errors.Is(err, ErrScript) || errors.Is(err, ErrAnswer) {
		t.Errorf("Answer after the hook ended = %v", err)
	}
}

// Leaving while a hook waits in ui.ask or ui.select ends it like ui.dialog.
func TestCancelDuringAskAndSelect(t *testing.T) {
	for name, call := range map[string]string{
		"ask":    `ui.ask("?")`,
		"select": `ui.select("?", {"a", "b"})`,
	} {
		t.Run(name, func(t *testing.T) {
			r := New(DefaultLimits)
			defer r.Close()
			env := load(t, r, `function on_enter()
  local ok, err = pcall(function() return `+call+` end)
  say("after: " .. tostring(err))
  `+call+` -- refused: the player left
  say("not reached")
end`)
			h := &recorder{}
			th, err := r.Run(h, env, "on_enter", "p")
			if err != nil || th == nil {
				t.Fatal(th, err)
			}
			r.Cancel(th)
			if r.Paused() != 0 {
				t.Errorf("Paused = %d after Cancel", r.Paused())
			}
			if len(h.out) != 0 {
				t.Errorf("a canceled hook's output reached the player: %q", h.out)
			}
			if _, err := r.Answer(h, th, "x"); err == nil || errors.Is(err, ErrAnswer) {
				t.Errorf("Answer after Cancel = %v, want a not-waiting error", err)
			}
			if _, err := r.Resume(h, th, 1); err == nil {
				t.Error("Resume after Cancel = nil error")
			}
		})
	}
}

func TestSelectLimitsAtBounds(t *testing.T) {
	nine := `{"1", "2", "3", "4", "5", "6", "7", "8", "9"}`
	tests := []struct {
		limits   string
		min, max int
	}{
		{`nil`, 1, 9},
		{`{}`, 1, 9},
		{`{min = 9}`, 9, 9},
		{`{min = 0, max = 1}`, 0, 1},
		{`{max = 9}`, 1, 9},
		{`{min = 2.0, max = 3}`, 2, 3},
	}
	for _, tt := range tests {
		t.Run(tt.limits, func(t *testing.T) {
			_, th, err := runHook(t, DefaultLimits, `function on_enter() ui.select("?", `+nine+`, `+tt.limits+`) end`)
			if err != nil || th == nil {
				t.Fatal(th, err)
			}
			if d := th.Dialog; !d.Multiple || d.Ask || d.Min != tt.min || d.Max != tt.max || len(d.Options) != 9 {
				t.Errorf("dialog = %+v, want min %d max %d", d, tt.min, tt.max)
			}
		})
	}
	for _, body := range []string{
		`ui.select("?", {})`,
		`ui.select("?", ` + nine[:len(nine)-1] + `, "10"})`,
		`ui.select("?", {""})`,
		`ui.select("?", {1})`,
		`ui.select("?", {string.rep("x", 201)})`,
		`ui.select(string.rep("x", 201), {"a"})`,
		`ui.select("?", "a")`,
		`ui.select("?", {"a"}, {max = -1})`,
		`ui.select("?", {"a"}, {max = 1/0})`,
		`ui.select("?", {"a"}, {min = true})`,
	} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

// ui.select with min = max = 9 takes exactly every option, in any order,
// and returns them sorted.
func TestSelectAllNine(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_enter()
  local n, l = ui.select("?", {"a", "b", "c", "d", "e", "f", "g", "h", "i"}, {min = 9})
  say(table.concat(n, ",") .. "=" .. table.concat(l))
end`)
	h := &recorder{}
	th, err := r.Run(h, env, "on_enter", "p")
	if err != nil || th == nil {
		t.Fatal(th, err)
	}
	if _, err := r.Resume(h, th, 1, 2, 3, 4, 5, 6, 7, 8); !errors.Is(err, ErrAnswer) || r.Paused() != 1 {
		t.Fatalf("eight of nine with min 9: err = %v, paused = %d", err, r.Paused())
	}
	if th, err = r.Resume(h, th, 9, 8, 7, 6, 5, 4, 3, 2, 1); err != nil || th != nil {
		t.Fatal(th, err)
	}
	if h.out[0] != "say:1,2,3,4,5,6,7,8,9=abcdefghi" {
		t.Errorf("out = %q", h.out)
	}
}

func TestDialogCheckTable(t *testing.T) {
	multi := Dialog{Options: []string{"a", "b", "c"}, Multiple: true, Min: 0, Max: 2}
	ask := Dialog{Title: "?", Ask: true}
	tests := []struct {
		name    string
		d       Dialog
		choices []int
		want    string // "" for an error
	}{
		{"select none with min 0", multi, nil, "[]"},
		{"select empty slice with min 0", multi, []int{}, "[]"},
		{"select sorted", multi, []int{3, 2}, "[2 3]"},
		{"select too many", multi, []int{1, 2, 3}, ""},
		{"select repeated", multi, []int{2, 2}, ""},
		{"select zero", multi, []int{0, 1}, ""},
		{"select negative", multi, []int{-1}, ""},
		{"select past the end", multi, []int{4}, ""},
		{"select huge", multi, []int{1 << 62}, ""},
		{"ask refuses choices", ask, []int{1}, ""},
		{"ask refuses nothing", ask, nil, ""},
		{"dialog huge", Dialog{Options: []string{"a"}}, []int{-1 << 62}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.d.Check(tt.choices)
			if tt.want == "" {
				if err == nil {
					t.Errorf("Check(%v) = %v, want an error", tt.choices, got)
				}
				return
			}
			if err != nil || got == nil || fmt.Sprint(got) != tt.want {
				t.Errorf("Check(%v) = %v, %v; want %s", tt.choices, got, err, tt.want)
			}
		})
	}
}

// Check must not change the caller's slice: the session passes the
// player's message straight in.
// A refused answer of the wrong kind wraps ErrAnswer and keeps the hook.
func TestWrongKindOfAnswer(t *testing.T) {
	for name, src := range map[string]string{
		"choice for ask":  `function on_enter() ui.ask("?") end`,
		"text for dialog": `function on_enter() ui.dialog("?", {"a"}) end`,
		"text for select": `function on_enter() ui.select("?", {"a"}) end`,
	} {
		t.Run(name, func(t *testing.T) {
			r := New(DefaultLimits)
			defer r.Close()
			th, err := r.Run(&recorder{}, load(t, r, src), "on_enter", "p")
			if err != nil || th == nil {
				t.Fatal(th, err)
			}
			if th.Dialog.Ask {
				_, err = r.Resume(&recorder{}, th, 1)
			} else {
				_, err = r.Answer(&recorder{}, th, "a")
			}
			if !errors.Is(err, ErrAnswer) || r.Paused() != 1 {
				t.Errorf("err = %v, paused = %d; want ErrAnswer and the hook kept", err, r.Paused())
			}
		})
	}
}

func TestDialogCheckKeepsInput(t *testing.T) {
	d := Dialog{Options: []string{"a", "b", "c"}, Multiple: true, Min: 1, Max: 3}
	in := []int{3, 1, 2}
	if _, err := d.Check(in); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(in) != "[3 1 2]" {
		t.Errorf("input changed to %v", in)
	}
}

func TestItemsAtBounds(t *testing.T) {
	// 20 items with 64-byte ids and 100-byte labels.
	out, err := hook(t, `local t = {} for i = 1, 20 do local n = (i < 10 and "0" or "") .. i t[i] = {id = string.rep("a", 62) .. n, label = string.rep("x", 98) .. n} end ui.items(t)`)
	if err != nil || len(out) != 1 || strings.Count(out[0], ";") != 20 {
		t.Fatalf("20 items at the limits: out = %q, err = %v", out, err)
	}
	// A table with only string keys has no items: it clears them.
	if out, err := hook(t, `ui.items({straw = {id = "straw", label = "x"}})`); err != nil || strings.Join(out, "|") != "items:" {
		t.Errorf("hash table: out = %q, err = %v", out, err)
	}
	for _, body := range []string{
		`ui.items()`,
		`ui.items("straw")`,
		`ui.items({{id = "a", label = 5}})`,
		`ui.items({{id = "a", label = "x"}, "b"})`,
		`ui.items({{id = "a b", label = "x"}})`,
		`ui.items({{id = "../a", label = "x"}})`,
	} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

// ui.items and ui.image count toward the outputs of one call, as say does.
func TestItemsAndImageCountAsOutputs(t *testing.T) {
	for name, call := range map[string]string{
		"items": `ui.items({})`,
		"image": `ui.image("map.png")`,
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := hook(t, `for i = 1, 100 do `+call+` end`); err != nil || len(out) != MaxOutputs {
				t.Errorf("100 calls: %d outputs, err = %v", len(out), err)
			}
			if _, err := hook(t, `for i = 1, 101 do `+call+` end`); !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "outputs") {
				t.Errorf("101 calls: err = %v, want too many outputs", err)
			}
		})
	}
}

// The error for an unknown image quotes at most 64 bytes of the name, so a
// script cannot fill the log through it.
func TestImageErrorCutsName(t *testing.T) {
	_, err := hook(t, `ui.image(string.rep("n", 10000) .. ".png")`)
	if !errors.Is(err, ErrScript) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("n", 65)) || !strings.Contains(err.Error(), "...") {
		t.Errorf("error does not cut the name: %.200s", err)
	}
	// A caption of exactly 200 bytes is fine.
	if out, err := hook(t, `ui.image("map.png", string.rep("c", 200))`); err != nil || len(out) != 1 {
		t.Errorf("200-byte caption: out = %q, err = %v", out, err)
	}
}

func TestSayVoiceEdgeCases(t *testing.T) {
	out, err := hook(t, `say("a", nil) say("b", {show = true}) say("c", {speak = false, show = true}) say("d", {speak = true, show = true})`)
	if err != nil || strings.Join(out, "|") != "say:a|say:b|say:c|say:(spoken) d" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	for _, body := range []string{`say("x", {speak = false, show = false})`, `say("x", {show = 0})`, `say("x", "speak")`} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

func TestWordsEdgeCases(t *testing.T) {
	tests := []struct{ expr, want string }{
		{`words.normalize("")`, ""},
		{`words.normalize(" \t\n ")`, ""},
		{`words.normalize("ÄPFEL Über")`, "äpfel über"},
		// U+00A0 no-break space and U+3000 ideographic space are spaces too.
		{`words.normalize("a\194\160\227\128\128b")`, "a b"},
		{`words.normalize(12)`, "12"},
		{`words.same("", "   ")`, "true"},
		{`words.same("a b", "ab")`, "false"},
		{`words.same(1, "1")`, "true"},
		{`words.same("Straße", "STRASSE")`, "false"},
	}
	for _, tt := range tests {
		if got := say1(t, tt.expr); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.expr, got, tt.want)
		}
	}
	for _, body := range []string{`words.normalize()`, `words.normalize({})`, `words.same("a")`, `words.same(nil, "a")`, `words.x = 1`} {
		if _, err := hook(t, body); !errors.Is(err, ErrScript) {
			t.Errorf("%s: err = %v, want a script error", body, err)
		}
	}
}

func TestRunPassesArgumentsInOrder(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env := load(t, r, `function on_x(player, a, b, c) say(player.name .. "|" .. tostring(a) .. "|" .. tostring(b) .. "|" .. tostring(c)) end`)
	h := &recorder{}
	if _, err := r.Run(h, env, "on_x", "Ada", "one", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(h, env, "on_x", "Bo"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.out, ","); got != "say:Ada|one||nil,say:Bo|nil|nil|nil" {
		t.Errorf("out = %q", got)
	}
}
