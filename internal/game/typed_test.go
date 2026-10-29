package game

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// What players type stays with them (typed.go): how words are cut, the
// bounds of what a world remembers, and every way a player types (input,
// ui.ask answers, room command arguments) against every way a hook
// shares (state.world set and update, keys and nested tables).

// typedRoom shares what the player typed in many ways. The words the
// tests type must not be in it, or in roomsFS: those are the pack's.
const typedRoom = `
commands = { note = { help = "leave a note" }, mark = { help = "mark a spot", gm = true } }
local function result(ok, err) say(tostring(ok) .. " " .. tostring(err)) end
function on_input(p, text)
  local cmd, rest = text, ""
  local sp = string.find(text, " ", 1, true)
  if sp then cmd, rest = string.sub(text, 1, sp - 1), string.sub(text, sp + 1) end
  if cmd == "ask" then
    result(state.world.set("home", ui.ask("Where do you live?")))
  elseif cmd == "update" then
    result(state.world.update("list", function(v) v = v or {} v[#v + 1] = rest return v end))
  elseif cmd == "nested" then
    result(state.world.set("deep", {a = {b = {c = rest}}}))
  elseif cmd == "key" then
    result(state.world.set(rest, true))
  elseif cmd == "tkey" then
    result(state.world.set("map", {[rest] = 1}))
  elseif cmd == "share" then
    result(state.world.set("note", rest))
  elseif cmd == "dialog" then
    ui.dialog("Ready?", {"Yes"}, {from = rest})
  elseif cmd == "askfrom" then
    ui.ask("Name?", {from = rest})
  end
end
function on_command(p, name, args) result(state.world.set("note", args)) end
`

const refusedTyped = "nil state.world.set: it holds words a player typed"

func TestWords(t *testing.T) {
	long := strings.Repeat("zq", 50)
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a 12 p3", nil},
		{"a is 12", []string{"is"}},
		{"Anna's phone: 0151-2345678!", []string{"anna", "phone", "0151", "2345678", "#015123", "#151234", "#512345", "#123456", "#234567", "#345678"}},
		{"06 12 34 56", []string{"#061234", "#612345", "#123456"}}, // a number typed in pairs
		{"KLEIN stadt", []string{"klein", "stadt"}},
		{"Größe über", []string{"größe", "über"}},
		{long, []string{long[:maxTypedBytes]}},
		{"abc​def", []string{"abcdef"}},        // a zero-width space does not split
		{"Jo\u0308rg", []string{"jo\u0308rg"}}, // nor a combining mark
	} {
		if got := words(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("words(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A long word is cut to maxTypedBytes on a letter's boundary, never in
// the middle of one.
func TestCutWordKeepsLetters(t *testing.T) {
	for _, letter := range []string{"é", "日", "𝔸"} {
		w := strings.Repeat(letter, 40)
		got := cutWord(w)
		if len(got) > maxTypedBytes || !utf8.ValidString(got) || !strings.HasPrefix(w, got) || len(got) <= maxTypedBytes-utf8.UTFMax {
			t.Errorf("cutWord(%d × %q) = %d bytes %q", 40, letter, len(got), got)
		}
	}
	if got := cutWord("short"); got != "short" {
		t.Errorf("cutWord(short) = %q", got)
	}
}

// bareWorld is a World with only what typed.go uses, for its bounds.
func bareWorld() *World {
	return &World{typedBy: map[string]*typedWords{}, typedCount: map[string]int{}, nameWords: map[string]struct{}{}, names: map[string]struct{}{}, vocab: map[string]struct{}{"door": {}}}
}

// A player's latest maxTypedWords words are kept; a word two players
// typed stays refused while one of them is remembered.
func TestTypedWordsPerPlayerBound(t *testing.T) {
	w := bareWorld()
	a, b := &session{w: w, id: "a"}, &session{w: w, id: "b"}
	word := func(i int) string { return "word" + strconv.Itoa(i) }
	for i := 0; i <= maxTypedWords; i++ {
		a.typed(word(i))
		a.typed(word(i)) // twice: counted once
	}
	if n := len(w.typedBy["a"].order); n != maxTypedWords || len(w.typedBy["a"].has) != maxTypedWords {
		t.Fatalf("a keeps %d words", n)
	}
	if err := w.shareable(word(0)); err != nil {
		t.Errorf("the oldest word is still refused: %v", err)
	}
	if err := w.shareable("at " + word(maxTypedWords)); err != script.ErrTyped {
		t.Errorf("the latest word: %v", err)
	}
	if len(w.typedCount) != maxTypedWords {
		t.Errorf("typedCount has %d words", len(w.typedCount))
	}
	b.typed(word(maxTypedWords))
	if w.typedCount[word(maxTypedWords)] != 2 {
		t.Errorf("count of a word two players typed = %d", w.typedCount[word(maxTypedWords)])
	}
	w.forgetTyped("a")
	if err := w.shareable(word(maxTypedWords)); err != script.ErrTyped {
		t.Errorf("b's word after a is forgotten: %v", err)
	}
	if err := w.shareable(word(maxTypedWords - 1)); err != nil {
		t.Errorf("a's word after a is forgotten: %v", err)
	}
	w.forgetTyped("nobody") // no such player: nothing happens
	if len(w.typedBy) != 1 || !slices.Equal(w.typedOrder, []string{"b"}) {
		t.Errorf("typedBy %v, order %v", w.typedBy, w.typedOrder)
	}
}

// At most maxTypedPlayers players are remembered: the first to type is
// forgotten for a new one.
func TestTypedWordsPlayerBound(t *testing.T) {
	w := bareWorld()
	for i := 0; i <= maxTypedPlayers; i++ {
		s := &session{w: w, id: "p" + strconv.Itoa(i)}
		s.typed("secret" + strconv.Itoa(i))
	}
	if len(w.typedBy) != maxTypedPlayers || len(w.typedOrder) != maxTypedPlayers || len(w.typedCount) != maxTypedPlayers {
		t.Fatalf("remembered %d players, order %d, words %d", len(w.typedBy), len(w.typedOrder), len(w.typedCount))
	}
	if w.typedBy["p0"] != nil || w.shareable("secret0") != nil {
		t.Error("the first player is still remembered")
	}
	if w.shareable("secret1") == nil || w.shareable("secret"+strconv.Itoa(maxTypedPlayers)) == nil {
		t.Error("a later player was forgotten")
	}
}

// Typing only short words, the pack's words or nothing remembers no
// player at all; a game master in GM mode is never remembered.
func TestTypedWordsNotRemembered(t *testing.T) {
	w := bareWorld()
	s := &session{w: w, id: "a"}
	for _, text := range []string{"", "o", "a b 12 p3", "a DOOR!", "door door"} {
		s.typed(text)
	}
	if len(w.typedBy) != 0 || len(w.typedOrder) != 0 {
		t.Errorf("remembered %v", w.typedBy)
	}
	gm := &session{w: w, id: "g", gm: true}
	gm.typed("Treasure under Hollowoak")
	if len(w.typedBy) != 0 || w.shareable("hollowoak") != nil {
		t.Errorf("a game master's words were remembered: %v", w.typedBy)
	}
}

// The words of players' names are bounded, and shared text may hold them
// even when a player typed them.
func TestNameWords(t *testing.T) {
	w := bareWorld()
	for i := 0; i < maxNameWords+10; i++ {
		w.joinedName("Name" + strconv.Itoa(i))
	}
	if len(w.nameWords) != maxNameWords {
		t.Errorf("%d name words kept", len(w.nameWords))
	}
	(&session{w: w, id: "a"}).typed("name7 lives in Quellbach")
	if err := w.shareable("Name7 waves."); err != nil {
		t.Errorf("a player's name: %v", err)
	}
	if err := w.shareable("from QUELLBACH"); err != script.ErrTyped {
		t.Errorf("a typed word in other letter case: %v", err)
	}
}

// What the player types anywhere is refused in state.world, in a value,
// nested in tables, as a key and as a table's key; a long word is
// compared by its first maxTypedBytes bytes.
func TestTypedWordsEveryWay(t *testing.T) {
	w, _ := clockWorld(t, typedRoom, typedRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	long := strings.Repeat("vy", 50)
	for _, tc := range []struct{ line, want string }{
		{"nested Moosgrund", refusedTyped},
		{"key moosgrund", refusedTyped},
		{"tkey MOOSGRUND", refusedTyped},
		{"update Moosgrund", "nil state.world.update: it holds words a player typed"},
		{"update box", "true nil"}, // the pack's word
		{"share " + long, refusedTyped},
	} {
		if got := texts(input(a, tc.line)); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%.40s = %q, want %q", tc.line, got, tc.want)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for text, refused := range map[string]bool{
		long[:maxTypedBytes] + "different": true, // the same first 64 bytes
		long[:maxTypedBytes-1]:             false,
		"the box":                          false,
	} {
		if err := w.shareable(text); (err != nil) != refused {
			t.Errorf("shareable(%.70s) = %v", text, err)
		}
	}
}

// An answer to ui.ask is remembered before the hook gets it.
func TestTypedAnswer(t *testing.T) {
	w, _ := clockWorld(t, typedRoom, typedRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	id := question(t, input(a, "ask"))
	got := a.Handle(ws.Message{Type: "answer", ID: id, Text: "Birkenhain 12"})
	if !strings.HasPrefix(texts(got), refusedTyped) {
		t.Errorf("answer = %q", texts(got))
	}
}

// A room command's arguments are remembered; a game master's are not,
// and in GM-off mode the game master types as a player.
func TestTypedCommandArgs(t *testing.T) {
	w, _ := clockWorld(t, typedRoom, typedRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if got := texts(input(a, "/note meet at Erlenhof")); !strings.HasPrefix(got, refusedTyped) {
		t.Errorf("/note = %q", got)
	}
	if got := texts(input(gm, "/mark Eichenkamp")); got != "true nil" {
		t.Errorf("gm /mark = %q", got)
	}
	if got := texts(input(gm, "/note Eichenkamp")); got != "true nil" {
		t.Errorf("gm /note = %q", got)
	}
	input(gm, "/gm off")
	if got := texts(input(gm, "share Lindenau")); !strings.HasPrefix(got, refusedTyped) {
		t.Errorf("GM off: %q", got)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.shareable("Eichenkamp"); err != nil {
		t.Errorf("the GM's word, typed in GM mode: %v", err)
	}
}

// A question's speaker that names a player of the world is dropped, in
// any letter case or punctuation; the player's own name too.
func TestDialogVoiceNames(t *testing.T) {
	w, _ := clockWorld(t, typedRoom, typedRoom)
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	b, _ := w.NewSession("", ws.Who{})
	defer b.Close()
	for _, tc := range []struct{ cmd, from, want string }{
		{"dialog", "Whisp", "Whisp"},
		{"dialog", "Traveler-2!", ""},
		{"dialog", "traveler", ""},
		{"askfrom", "TRAVELER 2", ""},
		{"askfrom", "Holmes", "Holmes"},
	} {
		got := input(a, tc.cmd+" "+tc.from)
		i := slices.IndexFunc(got, func(m ws.Message) bool { return m.Type == "dialog" || m.Type == "ask" })
		if i < 0 || got[i].From != tc.want {
			t.Errorf("%s from %q: %+v", tc.cmd, tc.from, got)
			continue
		}
		if got[i].Type == "dialog" {
			a.Handle(ws.Message{Type: "choose", ID: got[i].ID, Option: 1})
		} else {
			a.Handle(ws.Message{Type: "answer", ID: got[i].ID, Text: "yes"})
		}
	}
}

func TestNameKey(t *testing.T) {
	for in, want := range map[string]string{
		"Anna": "anna", "anna!": "anna", " Traveler  2 ": "traveler 2", "Traveler-2": "traveler 2", "": "", "!!": "", "Zoë": "zoë",
	} {
		if got := nameKey(in); got != want {
			t.Errorf("nameKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// FuzzTyped: what a player typed is refused when shared as typed, and
// every remembered word is lowercased, at most maxTypedBytes, and a word
// of its own.
func FuzzTyped(f *testing.F) {
	for _, s := range []string{"Anna 0151 2345678", "İstanbul", "Größe", strings.Repeat("日", 30), "a b c", "\xff\xfeabc", "ǅemal"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		w := bareWorld()
		s := &session{w: w, id: "a"}
		got := words(text)
		for _, word := range got {
			if len(word) > maxTypedBytes || word == "" || (utf8.ValidString(text) && !utf8.ValidString(word)) {
				t.Fatalf("words(%q) has %q", text, word)
			}
		}
		s.typed(text)
		remembered := false
		for _, word := range got {
			if _, ok := w.vocab[word]; !ok {
				remembered = true
			}
		}
		if err := w.shareable(text); remembered && err == nil {
			t.Fatalf("typed %q, then shared it: %v", text, w.typedBy)
		}
	})
}

// world.finish refuses a text with words a player typed: the world goes
// on, and the game master's own words in GM mode end it.
func TestTypedFinish(t *testing.T) {
	ended := make(chan string, 1)
	w := gameWorld(t, script.DefaultLimits, WithFinish(func(text string) { ended <- text }))
	a, _ := w.NewSession("", ws.Who{})
	defer a.Close()
	if got := say1(t, a, "finish Moosgrund is saved."); !strings.HasPrefix(got, "nil world.finish: it holds words a player typed") {
		t.Errorf("finish = %q", got)
	}
	w.mu.Lock()
	finishing := w.finishing
	w.mu.Unlock()
	if finishing {
		t.Error("the world ends")
	}
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	if got := say1(t, gm, "finish The dragon sleeps at Hollowoak."); got != "true nil" {
		t.Fatalf("gm finish = %q", got)
	}
	select {
	case text := <-ended:
		if text != "The dragon sleeps at Hollowoak." {
			t.Errorf("ended with %q", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the world did not end")
	}
}
