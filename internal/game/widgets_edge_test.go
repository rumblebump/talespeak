package game

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of the T-0004 session messages: input, click, answer and
// choose with choices.

// itemWorld is one room with the YAML items and the script, plus an image
// pic.png.
func itemWorld(t *testing.T, items, src string) *World {
	t.Helper()
	room := "id: r\ntitle: R\ndescription: A room.\n"
	if src != "" {
		room += "script: r.lua\n"
	}
	if items != "" {
		room += "items:\n" + items
	}
	fsys := fstest.MapFS{
		"pack.yaml":      {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: r\n")},
		"rooms/r.yaml":   {Data: []byte(room)},
		"assets/pic.png": {Data: []byte("\x89PNG\r\n\x1a\n0000")},
	}
	if src != "" {
		fsys["scripts/r.lua"] = &fstest.MapFile{Data: []byte(src)}
	}
	p, err := pack.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// A room without a script still shows its YAML items, and clicking them
// does nothing.
func TestItemsWithoutScript(t *testing.T) {
	w := itemWorld(t, "  - {id: rock, label: a rock}\n", "")
	s, first := w.NewSession("", ws.Who{})
	defer s.Close()
	if kinds(first) != "room:r" || len(first[0].Items) != 1 || first[0].Items[0] != (ws.Item{ID: "rock", Label: "a rock"}) {
		t.Fatalf("first = %+v", first)
	}
	for _, m := range []ws.Message{{Type: "click", Item: "rock"}, {Type: "input", Text: "look"}} {
		if got := s.Handle(m); len(got) != 1 || got[0].Type != "text" || got[0].Text != "Nothing happens." {
			t.Errorf("%+v = %+v", m, got)
		}
	}
}

// A room without items and ui.items({}) send no "items" key at all
// (omitempty); the client's cleanItems reads a missing list as none, which
// clears the items of the room before. This pins that wire format.
func TestEmptyItemsWireFormat(t *testing.T) {
	w := itemWorld(t, "", `function on_enter() room.describe() ui.items({}) end`)
	_, first := w.NewSession("", ws.Who{})
	expect(t, "empty items", jsonLines(t, first), []string{
		`{"type":"room","id":"r","title":"R","text":"A room."}`,
		`{"type":"items"}`,
	})
}

func TestClickEdgeCases(t *testing.T) {
	w := itemWorld(t, "  - {id: box, label: a box}\n  - {id: key, label: a key}\n", `
function on_click(player, id)
  if id == "box" then
    local n = ui.dialog("Open it?", {"yes", "no"})
    say("box " .. n)
  elseif id == "key" then
    ui.items({{id = "door", label = "a door"}})
  elseif id == "door" then
    error("stuck")
  end
end`)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	for _, item := range []string{"", "BOX", "box ", "door", "../box"} {
		if got := s.Handle(ws.Message{Type: "click", Item: item}); kinds(got) != "error:" || got[0].Message != "no such item" {
			t.Errorf("click %q = %+v", item, got)
		}
	}
	// A click opens a dialog; while it waits, clicks and typing are refused.
	if got := s.Handle(ws.Message{Type: "click", Item: "box"}); kinds(got) != "dialog:d1" {
		t.Fatalf("click box = %+v", got)
	}
	for _, m := range []ws.Message{{Type: "click", Item: "box"}, {Type: "click", Item: "key"}, {Type: "input", Text: "hi"}} {
		if got := s.Handle(m); kinds(got) != "error:" || !strings.Contains(got[0].Message, "open question") {
			t.Errorf("%+v during a dialog = %+v", m, got)
		}
	}
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2}); len(got) != 1 || got[0].Text != "box 2" {
		t.Fatalf("choose = %+v", got)
	}
	// ui.items replaces the YAML items: the old ones are gone.
	if got := s.Handle(ws.Message{Type: "click", Item: "key"}); kinds(got) != "items:" || len(got[0].Items) != 1 {
		t.Fatalf("click key = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "click", Item: "box"}); kinds(got) != "error:" {
		t.Errorf("click on a replaced item = %+v", got)
	}
	// A failing click hook reports without details and keeps the items.
	got := s.Handle(ws.Message{Type: "click", Item: "door"})
	if kinds(got) != "error:" || strings.Contains(got[0].Message, "stuck") || !strings.Contains(got[0].Message, "game master") {
		t.Errorf("click door = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "click", Item: "door"}); kinds(got) != "error:" || got[0].Message == "no such item" {
		t.Errorf("door gone after a failing hook: %+v", got)
	}
}

// Every player has their own clickable items.
func TestItemsArePerSession(t *testing.T) {
	w := itemWorld(t, "  - {id: key, label: a key}\n", `function on_click(player, id)
  if id == "key" then ui.items({{id = "door", label = "a door"}}) else say(id) end
end`)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	if got := a.Handle(ws.Message{Type: "click", Item: "key"}); kinds(got) != "items:" {
		t.Fatalf("a click key = %+v", got)
	}
	if got := b.Handle(ws.Message{Type: "click", Item: "door"}); kinds(got) != "error:" {
		t.Errorf("b clicked a's door: %+v", got)
	}
	if got := b.Handle(ws.Message{Type: "click", Item: "key"}); kinds(got) != "items:" {
		t.Errorf("b lost its key: %+v", got)
	}
}

// on_input can ask a question; the answer goes to it, not to on_input.
func TestInputOpensQuestion(t *testing.T) {
	w := scriptWorld(t, `function on_input(player, text)
  if text == "riddle" then
    local a = ui.ask("What am I?")
    say("answer " .. a)
  else
    say("typed " .. text)
  end
end`, script.DefaultLimits, quiet)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	got := s.Handle(ws.Message{Type: "input", Text: "riddle"})
	if kinds(got) != "ask:d1" || got[0].Strict || got[0].Speak || got[0].Hide {
		t.Fatalf("input = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "input", Text: "cell"}); kinds(got) != "error:" {
		t.Errorf("input while asked = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "answer", ID: "d1", Text: "cell"}); len(got) != 1 || got[0].Text != "answer cell" {
		t.Errorf("answer = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "input", Text: "x"}); len(got) != 1 || got[0].Text != "typed x" {
		t.Errorf("input after = %+v", got)
	}
}

// The answer is cleaned like typed input, and 500 bytes are accepted.
func TestAnswerCleaning(t *testing.T) {
	w := scriptWorld(t, `function on_enter() while true do local a = ui.ask("?") say("[" .. a .. "]") end end`, script.DefaultLimits, quiet)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	tests := []struct{ in, want string }{
		{"  Cell  ", "[Cell]"},
		{"a\r\nb", "[a  b]"},
		{"\x00x\x1b[31m", "[x [31m]"},
		{"tab\there", "[tab here]"},
		{"x\u2029y", "[x y]"},
		{"\u0085next", "[next]"},
		{strings.Repeat("y", MaxInputBytes), "[" + strings.Repeat("y", MaxInputBytes) + "]"},
	}
	for i, tt := range tests {
		id := "d" + itoa(i+1)
		got := s.Handle(ws.Message{Type: "answer", ID: id, Text: tt.in})
		if len(got) != 2 || got[0].Text != tt.want || got[1].Type != "ask" {
			t.Fatalf("answer %q = %+v, want %q", tt.in, got, tt.want)
		}
	}
}

// An answer that cleaning makes longer than script.MaxAnswerBytes is
// refused and the question stays open: the hook must not be lost.
//
// cleanInput turns each invalid UTF-8 byte into U+FFFD (3 bytes), so 500
// bytes in can be 1000 bytes out. Over a WebSocket encoding/json already
// replaced invalid bytes, so this reaches Handle only from Go, but the
// session must not drop a paused hook it can no longer answer or cancel.
func TestAnswerGrownByCleaningKeepsQuestion(t *testing.T) {
	w := scriptWorld(t, `function on_enter() local a = ui.ask("?") say("got " .. #a) end`, script.DefaultLimits, quiet)
	s, _ := w.NewSession("", ws.Who{})
	in := strings.Repeat("\xffa", MaxInputBytes/2)
	if len(cleanInput(in)) <= script.MaxAnswerBytes {
		t.Fatalf("cleaned length %d: the test needs a longer result", len(cleanInput(in)))
	}
	if got := s.Handle(ws.Message{Type: "answer", ID: "d1", Text: in}); kinds(got) != "error:d1" {
		t.Errorf("grown answer = %+v", got)
	}
	if n := w.rt.Paused(); n != 1 {
		t.Errorf("Paused = %d after a refused answer, want 1", n)
	}
	if got := s.Handle(ws.Message{Type: "answer", ID: "d1", Text: "ok"}); len(got) != 1 || got[0].Text != "got 2" {
		t.Errorf("answer after the refused one = %+v", got)
	}
	s.Close()
	if n := w.rt.Paused(); n != 0 {
		t.Errorf("Paused = %d after Close: a hook leaked", n)
	}
}

func TestChooseSelectEdgeCases(t *testing.T) {
	w := scriptWorld(t, `function on_enter()
  while true do
    local n = ui.select("?", {"a", "b", "c"}, {min = 1, max = 2})
    say(table.concat(n, ","))
  end
end`, script.DefaultLimits, quiet)
	s, first := w.NewSession("", ws.Who{})
	defer s.Close()
	if kinds(first) != "select:d1" {
		t.Fatalf("first = %+v", first)
	}
	huge := make([]int, 100000)
	for i := range huge {
		huge[i] = i + 1
	}
	for _, m := range []ws.Message{
		{Type: "choose", ID: "d1"},                     // none, min 1
		{Type: "choose", ID: "d1", Choices: []int{}},   // none
		{Type: "choose", ID: "d1", Option: 2},          // option is not a select answer
		{Type: "choose", ID: "d1", Choices: huge},      // far too many
		{Type: "choose", ID: "d1", Choices: []int{-1}}, // negative
		{Type: "choose", ID: "d1", Choices: []int{1 << 40}},
		{Type: "answer", ID: "d1", Text: "a"},
	} {
		if got := s.Handle(m); !isError(got) {
			t.Errorf("%+v = %+v", m.Choices[:min(len(m.Choices), 3)], got)
		}
	}
	// Option and choices together: choices count for a select.
	got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 3, Choices: []int{2, 1}})
	if kinds(got) != "text: select:d2" || got[0].Text != "1,2" {
		t.Fatalf("choose = %+v", got)
	}
	// The old id is gone once answered.
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Choices: []int{1}}); kinds(got) != "error:" {
		t.Errorf("stale id = %+v", got)
	}
}

// For ui.dialog only option counts; choices are ignored.
func TestChooseDialogIgnoresChoices(t *testing.T) {
	w := scriptWorld(t, `function on_enter() local n = ui.dialog("?", {"a", "b"}) say("n" .. n) end`, script.DefaultLimits, quiet)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Choices: []int{1}}); kinds(got) != "error:d1" {
		t.Errorf("choices without option = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2, Choices: []int{1}}); len(got) != 1 || got[0].Text != "n2" {
		t.Errorf("option with choices = %+v", got)
	}
}

// Closing a session or the world while a select or ask waits ends the
// hook; the world refuses answers once closed.
func TestLeaveDuringSelectAndAsk(t *testing.T) {
	for name, call := range map[string]string{"select": `ui.select("?", {"a"})`, "ask": `ui.ask("?")`} {
		t.Run(name, func(t *testing.T) {
			w := scriptWorld(t, `function on_enter() `+call+` say("not reached") end`, script.DefaultLimits, quiet)
			for range 3 {
				s, _ := w.NewSession("", ws.Who{})
				s.Close()
			}
			if n := w.rt.Paused(); n != 0 {
				t.Errorf("Paused = %d after Close", n)
			}
			s, _ := w.NewSession("", ws.Who{})
			w.Close()
			for _, m := range []ws.Message{{Type: "choose", ID: "d1", Choices: []int{1}}, {Type: "answer", ID: "d1", Text: "a"}} {
				got := s.Handle(m)
				if name == "select" && m.Type == "choose" || name == "ask" && m.Type == "answer" {
					if kinds(got) != "error:" || !strings.Contains(got[0].Message, "closed") {
						t.Errorf("%s after the world closed = %+v", m.Type, got)
					}
				}
			}
			s.Close()
		})
	}
}

// ui.image of a name the pack lacks fails the hook without details.
func TestImageUnknownName(t *testing.T) {
	w := itemWorld(t, "", `function on_enter() ui.image("pic.png", "ok") ui.image("../pack.yaml") end`)
	_, first := w.NewSession("", ws.Who{})
	if kinds(first) != "image: error:" || first[0].URL != "/packs/t/1.0.0/assets/pic.png" || first[0].Text != "ok" || strings.Contains(first[1].Message, "pack.yaml") {
		t.Errorf("first = %+v", first)
	}
}

// say's voice reaches the message as speak and hide.
func TestSayVoiceMessages(t *testing.T) {
	w := scriptWorld(t, `function on_enter() say("a") say("b", {speak = true}) say("c", {speak = true, show = false}) end`, script.DefaultLimits, quiet)
	_, first := w.NewSession("", ws.Who{})
	expect(t, "voices", jsonLines(t, first), []string{
		`{"type":"text","text":"a"}`,
		`{"type":"text","text":"b","speak":true}`,
		`{"type":"text","text":"c","speak":true,"hide":true}`,
	})
}

func TestCleanInput(t *testing.T) {
	tests := map[string]string{
		"":                    "",
		"   ":                 "",
		"\u00a0x\u00a0":       "x", // no-break space is trimmed
		"a\u200bb":            "a\u200bb",
		"\ufeffbom":           "\ufeffbom",
		"x\u2028\u2029y":      "x  y",
		"\x7f":                "",
		"bad\xc3":             "bad\ufffd",
		"\xe2\x80":            "\ufffd",
		"emoji \U0001F600 ok": "emoji \U0001F600 ok",
	}
	for in, want := range tests {
		if got := cleanInput(in); got != want {
			t.Errorf("cleanInput(%q) = %q, want %q", in, got, want)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// FuzzWidgets sends arbitrary messages to a session of a room with items,
// a select, a question and on_input. Every message gets an answer, except
// a blank typed line, and none panics or leaves a hook paused that the
// session cannot end.
func FuzzWidgets(f *testing.F) {
	f.Add("click", "", "box", 0, []byte{}, "")
	f.Add("click", "", "sel", 0, []byte{}, "")
	f.Add("choose", "d1", "", 0, []byte{1, 2}, "")
	f.Add("answer", "d1", "", 0, []byte{}, "cell")
	f.Add("answer", "d1", "", 0, []byte{}, strings.Repeat("\xffa", 250))
	f.Add("input", "", "", 0, []byte{}, " \n ")
	f.Add("input", "", "", 0, []byte{}, "ask")
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml":    {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: r\n")},
		"rooms/r.yaml": {Data: []byte("id: r\ntitle: R\ndescription: A room.\nscript: r.lua\nitems:\n  - {id: box, label: box}\n  - {id: sel, label: sel}\n")},
		"scripts/r.lua": {Data: []byte(`
function on_click(player, id)
  if id == "box" then say("box " .. ui.dialog("?", {"a", "b"}))
  else local n = ui.select("?", {"a", "b", "c"}, {min = 0, max = 2}) say(#n) end
end
function on_input(player, text)
  if text == "ask" then say(ui.ask("?")) else say(text) end
end`)},
	})
	if err != nil {
		f.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(w.Close)
	opens := []ws.Message{{}, {Type: "click", Item: "box"}, {Type: "click", Item: "sel"}, {Type: "input", Text: "ask"}}
	f.Fuzz(func(t *testing.T, typ, id, item string, option int, choices []byte, text string) {
		for _, open := range opens {
			s, _ := w.NewSession("", ws.Who{})
			if open.Type != "" {
				s.Handle(open)
			}
			cs := make([]int, len(choices))
			for i, c := range choices {
				cs[i] = int(int8(c))
			}
			got := s.Handle(ws.Message{Type: typ, ID: id, Item: item, Option: option, Choices: cs, Text: text})
			if len(got) == 0 && !(typ == "input" && cleanInput(text) == "") {
				t.Fatalf("no answer to %q after %+v", typ, open)
			}
			for _, m := range got {
				if m.Type == "error" && m.Message == "" {
					t.Fatal("error without a message")
				}
			}
			s.Close()
		}
		if n := w.rt.Paused(); n != 0 {
			t.Fatalf("%d hooks paused after every session closed", n)
		}
	})
}
