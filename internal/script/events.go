package script

import (
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"
)

// What happened in a room, told to the players in it (room.event), and
// the rules for text that reaches other players: what players type never
// does (Shared), and a speaker's name never claims to be the engine or a
// game master (fromArg, ReservedName).

// Events is what a Host shares when its world keeps the room's events: a
// Host without it plays alone, and room.event raises an error.
type Events interface {
	// Event tells the players in the room text, as what just happened
	// there, and keeps it among the room's last events, which a player
	// who connects again reads. With all, the player whose hook runs reads
	// it too. An error refuses it (Shared refuses the text).
	Event(text string, all bool) error
}

// Shared is what a world checks text with before it reaches other
// players: state.world, room events, room timer labels, world.finish.
// It returns an error when text holds words a player typed, so a pack
// cannot pass what one player types on to the others by accident.
type Shared func(text string) error

// MaxEvents caps the room.event calls of one hook call, so a loop cannot
// fill the other players' queues.
const MaxEvents = 5

// MaxEventBytes bounds the text of one room event.
const MaxEventBytes = 500

// room.event(text [, {all = true}]) tells the other players in the room
// what just happened, such as "Anna opens the door.", and keeps it among
// the room's last events: a player who connects again reads them. With
// all = true the player whose hook runs reads it too. It returns true, or
// nil and the reason when the world refuses the text (it holds words a
// player typed).
func (r *Runtime) roomEvent(L *lua.LState) int {
	text := L.CheckString(1)
	if strings.TrimSpace(text) == "" || len(text) > MaxEventBytes || !utf8.ValidString(text) {
		L.ArgError(1, "an event is 1 to 500 bytes of text")
	}
	all := false
	if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
		switch v := L.CheckTable(2).RawGetString("all").(type) {
		case *lua.LNilType:
		case lua.LBool:
			all = bool(v)
		default:
			L.ArgError(2, "all must be true or false")
		}
	}
	ev, ok := r.timerHost(L).(Events)
	if !ok {
		L.RaiseError("this world keeps no room events")
	}
	if r.cur.thread != nil && r.cur.thread.readOnlyWorld {
		// on_change runs for every player: events from it would multiply.
		L.Push(lua.LNil)
		L.Push(lua.LString("room.event: on_change cannot tell room events; tell them where the change is made"))
		return 2
	}
	if r.cur.events++; r.cur.events > MaxEvents {
		L.RaiseError("more than %d room events in one call", MaxEvents)
	}
	if err := ev.Event(text, all); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("room.event: " + err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

// ErrTyped refuses shared text that holds words a player typed.
var ErrTyped = errors.New("it holds words a player typed, and what players type never reaches other players")

// reservedWords are words no pack voice and no new account name may hold,
// reservedStarts what no word may start with, and reservedParts letters
// none may hold anywhere: they would read as the engine, a game master or
// the school's staff ("GM Carl", "GMCarl", "ADMINBOB", "TheGameMaster",
// "Teachers", "Spielleiter"). ReservedName compares them.
var (
	reservedWords  = []string{"gm", "gms", "dm", "mod", "mods", "staff", "system", "server", "sysop", "official"}
	reservedStarts = []string{"admin", "teacher", "moderator", "sysop", "lehrer"}
	reservedParts  = []string{"gamemaster", "dungeonmaster", "spielleiter", "moderator", "administrator", "sysadmin", "talespeak"}
)

// ReservedName reports whether name reads as the engine's, a game
// master's or staff's. Letter case, accents and fullwidth forms are
// ignored. Words are split at anything but a letter, and also where a
// capital follows a small letter or a capital starts a small word after
// capitals ("AdminBob", "GMCarl"), so "Badminton" holds no "admin". A name
// mixing Latin letters with Greek or Cyrillic ones ("Аdmin" with a
// Cyrillic A) is reserved too: it can only be there to look like another.
func ReservedName(name string) bool {
	rs := []rune(name)
	latin, other := false, false
	for i, c := range rs {
		c = plainLetter(c)
		rs[i] = c
		switch {
		case unicode.Is(unicode.Latin, c):
			latin = true
		case unicode.Is(unicode.Greek, c), unicode.Is(unicode.Cyrillic, c):
			other = true
		}
	}
	if latin && other {
		return true
	}
	// Words split at non-letters (plain), also where a word in capitals
	// or with one starts (camel: "GM|Carl", "Admin|Bob"), and at every
	// change between capitals and small letters (runs: "SERVER|bot").
	var plain, camel, runs []string
	var flat, word, part, run strings.Builder
	flush := func(b *strings.Builder, list *[]string) {
		if b.Len() > 0 {
			*list = append(*list, b.String())
			b.Reset()
		}
	}
	for i, c := range rs {
		if !unicode.IsLetter(c) {
			flush(&word, &plain)
			flush(&part, &camel)
			flush(&run, &runs)
			continue
		}
		if i > 0 && unicode.IsLetter(rs[i-1]) && unicode.IsUpper(c) != unicode.IsUpper(rs[i-1]) {
			flush(&run, &runs)
		}
		if i > 0 && unicode.IsUpper(c) && unicode.IsLower(rs[i-1]) ||
			i > 0 && i+1 < len(rs) && unicode.IsUpper(rs[i-1]) && unicode.IsUpper(c) && unicode.IsLower(rs[i+1]) {
			flush(&part, &camel)
		}
		lc := unicode.ToLower(c)
		flat.WriteRune(lc)
		word.WriteRune(lc)
		part.WriteRune(lc)
		run.WriteRune(lc)
	}
	flush(&word, &plain)
	flush(&part, &camel)
	flush(&run, &runs)
	for _, w := range slices.Concat(plain, camel, runs) {
		if slices.Contains(reservedWords, w) {
			return true
		}
	}
	for _, w := range plain {
		for _, p := range reservedStarts {
			if strings.HasPrefix(w, p) {
				return true
			}
		}
	}
	f := flat.String()
	if slices.Contains(reservedWords, f) { // "G.M."
		return true
	}
	for _, p := range reservedParts {
		if strings.Contains(f, p) {
			return true
		}
	}
	return false
}

// plainLetter maps a fullwidth form to its ASCII letter and an accented
// Latin letter to its plain one, so "ＧＭ" and "Admín" read as "GM" and
// "Admin".
func plainLetter(c rune) rune {
	if c >= 0xFF01 && c <= 0xFF5E {
		return c - 0xFEE0
	}
	const from, to = "ÀÁÂÃÄÅàáâãäåÈÉÊËèéêëÌÍÎÏìíîïÒÓÔÕÖòóôõöÙÚÛÜùúûüÑñÇçÝýÿ", "AAAAAAaaaaaaEEEEeeeeIIIIiiiiOOOOOoooooUUUUuuuuNnCcYyy"
	if i := strings.IndexRune(from, c); i >= 0 {
		return rune(to[utf8.RuneCountInString(from[:i])])
	}
	return c
}

// EachString calls f with every string s keeps, keys and the strings in
// tables included: the engine reads what a player's state holds.
func (s *State) EachString(f func(string)) {
	for k, b := range s.vals {
		f(k)
		eachString(b, f)
	}
}

// eachString calls f with the strings of the encoding b; it stops at
// anything it cannot read.
func eachString(b []byte, f func(string)) {
	for len(b) > 0 {
		tag := b[0]
		b = b[1:]
		switch tag {
		case tagEnd, tagFalse, tagTrue, tagTable:
		case tagInt:
			_, k := binary.Varint(b)
			if k <= 0 {
				return
			}
			b = b[k:]
		case tagFloat:
			if len(b) < 8 {
				return
			}
			b = b[8:]
		case tagString:
			n, k := binary.Uvarint(b)
			if k <= 0 || n > uint64(len(b)-k) {
				return
			}
			f(string(b[k : k+int(n)]))
			b = b[k+int(n):]
		default:
			return
		}
	}
}
