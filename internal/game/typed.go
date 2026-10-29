package game

import (
	"strings"
	"unicode"

	"github.com/rumblebump/talespeak/internal/script"
)

// What players type stays with them. Players never chat: a typed line, an
// answer or a room command's arguments only run the player's own hooks.
// But a hook could pass them on, by accident, to the other players: in
// state.world, a room event, a room timer's label or world.finish's text.
// The world remembers the words each player typed lately and refuses
// shared text that holds one (script.Shared), so a child's name, address
// or phone number typed into an answer never reaches the others.
//
// Words the pack itself holds (its room texts, labels, Lua and modules)
// and the names of the world's players are not remembered: they are no
// secret, and a pack writes them all the time. A game master in GM mode
// may share what they type (/say does). A pack that takes typed text apart
// letter by letter on purpose gets past this; packs are installed by
// admins, and the rule is there for mistakes.

// Bounds of the typed words a world remembers.
const (
	minTypedRunes   = 3    // shorter words are not remembered: "a", "is", "12"
	maxTypedBytes   = 64   // longer words are remembered by their first 64 bytes
	maxTypedWords   = 200  // per player, the latest kept
	maxTypedPlayers = 1000 // players remembered, the latest to type kept
	maxNameWords    = 5000 // words of players' names kept
)

// typedWords are the words one player typed lately, the oldest first.
type typedWords struct {
	order []string
	has   map[string]struct{}
}

// words splits s into its lowercased words: runs of letters and digits of
// at least minTypedRunes, cut to maxTypedBytes.
func words(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) < minTypedRunes {
			continue
		}
		out = append(out, cutWord(w))
	}
	return out
}

// cutWord cuts w to maxTypedBytes on a letter's boundary.
func cutWord(w string) string {
	if len(w) <= maxTypedBytes {
		return w
	}
	return strings.ToValidUTF8(w[:maxTypedBytes], "")
}

// packWords returns every word of the pack's own texts and code, and of
// its ruleset's modules: words a pack shares are no player's secret.
func (w *World) packWords() map[string]struct{} {
	vocab := map[string]struct{}{}
	add := func(s string) {
		for _, word := range words(s) {
			vocab[word] = struct{}{}
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
			w.typedOrder = append(w.typedOrder, s.id)
		}
		if _, ok := t.has[word]; ok {
			continue
		}
		if len(t.order) >= maxTypedWords {
			old := t.order[0]
			t.order = t.order[1:]
			delete(t.has, old)
			w.uncount(old)
		}
		t.order = append(t.order, word)
		t.has[word] = struct{}{}
		w.typedCount[word]++
	}
}

// forgetTyped forgets what player id typed. The caller holds w.mu.
func (w *World) forgetTyped(id string) {
	t := w.typedBy[id]
	if t == nil {
		return
	}
	for _, word := range t.order {
		w.uncount(word)
	}
	delete(w.typedBy, id)
	for i, o := range w.typedOrder {
		if o == id {
			w.typedOrder = append(w.typedOrder[:i], w.typedOrder[i+1:]...)
			break
		}
	}
}

func (w *World) uncount(word string) {
	if w.typedCount[word]--; w.typedCount[word] <= 0 {
		delete(w.typedCount, word)
	}
}

// joinedName notes the words of a player's name, which shared text may
// hold. The caller holds w.mu.
func (w *World) joinedName(name string) {
	for _, word := range words(name) {
		if len(w.nameWords) < maxNameWords {
			w.nameWords[word] = struct{}{}
		}
	}
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
