package game

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rumblebump/talespeak/internal/script"
)

// What players type stays with them. Players never chat: a typed line, an
// answer or a room command's arguments only run the player's own hooks.
// But a hook could pass them on, by accident, to the other players: in
// state.world, a room event, a room timer's label or world.finish's text.
// The world remembers the words each player online typed lately, and the
// strings their state.player and state.profile hold (where a pack keeps
// an answer), and refuses shared text that holds one (script.Shared), so
// a child's name, address or phone number typed into an answer never
// reaches the others.
//
// Words the pack itself holds (its room texts, labels, Lua and modules)
// and the names of the world's players are not remembered: they are no
// secret, and a pack writes them all the time. A game master in GM mode
// may share what they type (/say does). A pack that takes typed text apart
// letter by letter on purpose gets past this; packs are installed by
// admins, and the rule is there for mistakes.

// Bounds of the typed words a world remembers.
const (
	minTypedLetters = 2   // shorter words are not remembered: "a", "I"
	minTypedDigits  = 3   // nor shorter words with a digit: "12" and "p3", which rolls, counts and player ids use
	digitRun        = 6   // a line's digits, joined, are also remembered in runs of this many: "06 12 34 56 78"
	maxTypedBytes   = 64  // longer words are remembered by their first 64 bytes
	maxTypedWords   = 200 // per player, the latest kept
	maxTypedPlayers = 500 // players remembered, the latest to type kept
	maxNameWords    = 5000
)

// typedWords are the words one player typed lately, the oldest first.
type typedWords struct {
	order []string
	has   map[string]struct{}
}

// wordRune reports whether r belongs to a word: letters, digits and the
// marks written on them (a combining umlaut, Hindi vowel signs).
func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) }

// fold lowercases s and drops invisible format characters (a zero-width
// space or a soft hyphen inside a word), so they cannot split a word.
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// words splits s into its folded words, cut to maxTypedBytes, and adds
// "#" and every digitRun digits in a row of s's digits joined.
func words(s string) []string {
	s = fold(s)
	var out []string
	var digits strings.Builder
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !wordRune(r) }) {
		hasDigit, n := false, 0
		for _, r := range w {
			n++
			if unicode.IsDigit(r) {
				digits.WriteRune(r)
				hasDigit = true
			}
		}
		if !hasDigit && n >= minTypedLetters || hasDigit && n >= minTypedDigits {
			out = append(out, cutWord(w))
		}
	}
	d := []rune(digits.String())
	for i := 0; i+digitRun <= len(d); i++ {
		out = append(out, "#"+string(d[i:i+digitRun]))
	}
	return out
}

// cutWord cuts w to maxTypedBytes on a letter's boundary.
func cutWord(w string) string {
	if len(w) <= maxTypedBytes {
		return w
	}
	cut := maxTypedBytes
	for cut > 0 && !utf8.RuneStart(w[cut]) {
		cut--
	}
	return w[:cut]
}

// packWords returns every word of the pack's own texts and code, and of
// its ruleset's modules: words a pack shares are no player's secret.
func (w *World) packWords() map[string]struct{} {
	vocab := map[string]struct{}{}
	add := func(s string) {
		for _, word := range words(s) {
			if word[0] != '#' {
				vocab[word] = struct{}{}
			}
		}
	}
	p := w.pack
	add(p.Title)
	add(p.Description)
	for _, r := range p.Rooms {
		add(r.Title)
		add(r.Description)
		add(r.Source)
		for _, it := range r.Items {
			add(it.Label)
		}
	}
	for _, src := range w.modules() {
		add(src)
	}
	for _, st := range p.Settings {
		add(st.Label)
		for _, c := range st.Choices {
			add(c)
		}
	}
	return vocab
}

// typed remembers the words the player typed in text, before a hook of
// theirs gets it. The caller holds w.mu.
func (s *session) typed(text string) {
	if s.gm {
		return
	}
	w := s.w
	t := w.typedBy[s.id]
	for _, word := range words(text) {
		if _, ok := w.vocab[word]; ok {
			continue
		}
		if t == nil {
			if len(w.typedBy) >= maxTypedPlayers {
				w.forgetTyped(w.typedOrder[0])
			}
			t = &typedWords{has: map[string]struct{}{}}
			w.typedBy[s.id] = t
		}
		if _, ok := t.has[word]; ok { // typed again: the latest now
			t.order = append(slices.DeleteFunc(t.order, func(o string) bool { return o == word }), word)
			continue
		}
		if len(t.order) >= maxTypedWords {
			old := t.order[0]
			t.order = slices.Delete(t.order, 0, 1)
			delete(t.has, old)
			w.uncount(old)
		}
		word = strings.Clone(word) // not the whole line it came from
		t.order = append(t.order, word)
		t.has[word] = struct{}{}
		w.typedCount[word]++
	}
	if t != nil { // the player who typed last is kept longest
		w.typedOrder = append(slices.DeleteFunc(w.typedOrder, func(o string) bool { return o == s.id }), s.id)
	}
}

// typedState remembers the strings st keeps as typed by the player: a
// pack that stored an answer in state.player before a restart, or in
// state.profile in another world, cannot share it now either. The caller
// holds w.mu.
func (s *session) typedState(st *script.State) {
	st.EachString(s.typed)
}

// forgetTyped forgets what player id typed: once they left, nothing they
// typed is in a script any more but in their state, which typedState reads
// again when they come back. The caller holds w.mu.
func (w *World) forgetTyped(id string) {
	t := w.typedBy[id]
	if t == nil {
		return
	}
	for _, word := range t.order {
		w.uncount(word)
	}
	delete(w.typedBy, id)
	w.typedOrder = slices.DeleteFunc(w.typedOrder, func(o string) bool { return o == id })
}

func (w *World) uncount(word string) {
	if w.typedCount[word]--; w.typedCount[word] <= 0 {
		delete(w.typedCount, word)
	}
}

// joinedName notes a player's name, whose words shared text may hold and
// which no pack voice may take (voice). The caller holds w.mu.
func (w *World) joinedName(name string) {
	if len(w.nameWords) >= maxNameWords {
		return
	}
	for _, word := range words(name) {
		w.nameWords[word] = struct{}{}
	}
	w.names[nameKey(name)] = struct{}{}
}

// shareable implements script.Shared: it refuses text that holds a word a
// player typed lately, other than a player's name. The caller holds w.mu.
func (w *World) shareable(text string) error {
	if len(w.typedCount) == 0 {
		return nil
	}
	for _, word := range words(text) {
		if _, typed := w.typedCount[word]; !typed {
			continue
		}
		if _, name := w.nameWords[word]; name {
			continue
		}
		return script.ErrTyped
	}
	return nil
}
