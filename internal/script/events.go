package script

import (
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
// and reservedParts letters none may hold anywhere: they would read as the
// engine, a game master or the school's staff ("GM Carl", "AdminBob",
// "TheGameMaster"). ReservedName compares them.
var (
	reservedWords = []string{"gm", "gms", "admin", "admins", "mod", "mods", "moderator", "teacher", "staff", "system", "server", "sysop", "talespeak"}
	reservedParts = []string{"gamemaster", "moderator", "administrator", "talespeak"}
)

// ReservedName reports whether name reads as the engine's, a game
// master's or staff's. Letter case is ignored; words are split at
// anything but a letter and where a capital follows a small letter, so
// "AdminBob" holds "admin" and "Badminton" does not.
func ReservedName(name string) bool {
	var flat, word strings.Builder
	var words []string
	end := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	prevLower := false
	for _, c := range name {
		if !unicode.IsLetter(c) {
			end()
			prevLower = false
			continue
		}
		if unicode.IsUpper(c) && prevLower {
			end()
		}
		prevLower = unicode.IsLower(c)
		lc := unicode.ToLower(c)
		flat.WriteRune(lc)
		word.WriteRune(lc)
	}
	end()
	for _, w := range words {
		if slices.Contains(reservedWords, w) {
			return true
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
