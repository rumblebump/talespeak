package script

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"
)

// What a script sends to the player: say, room.describe, room.go, the
// questions of ui.dialog, ui.select and ui.ask, clickable items and
// pictures.

// output counts say and room.describe calls and returns the host, or raises
// an error when called outside a hook or too often.
func (r *Runtime) output(L *lua.LState) Host {
	if r.cur == nil || r.cur.host == nil {
		L.RaiseError("no player here: call this from a hook such as on_enter")
	}
	r.cur.outputs++
	if r.cur.outputs > MaxOutputs {
		L.RaiseError("more than %d outputs in one call", MaxOutputs)
	}
	return r.cur.host
}

// say(text [, {speak, show, from}]) sends a line of text to the player.
func (r *Runtime) say(L *lua.LState) int {
	text := L.CheckString(1)
	if len(text) > MaxTextBytes {
		L.ArgError(1, "text longer than 4000 bytes")
	}
	how := voiceArg(L, 2)
	r.output(L).Say(text, how)
	return 0
}

// voiceArg reads the optional {speak = bool, show = bool} table at n:
// speak reads the text aloud even when read aloud is off, show = false
// shows it only when it cannot be spoken (a listening exercise).
func voiceArg(L *lua.LState, n int) Voice {
	if L.GetTop() < n || L.Get(n) == lua.LNil {
		return Voice{}
	}
	t := L.CheckTable(n)
	flag := func(name string, def bool) bool {
		switch v := t.RawGetString(name).(type) {
		case *lua.LNilType:
			return def
		case lua.LBool:
			return bool(v)
		default:
			L.ArgError(n, name+" must be true or false")
			return false
		}
	}
	v := Voice{Speak: flag("speak", false), Hide: !flag("show", true), From: fromArg(L, t, n)}
	if v.Hide && !v.Speak {
		L.ArgError(n, "show = false needs speak = true: the player must see or hear it")
	}
	return v
}

// MaxFromBytes bounds a speaker's name.
const MaxFromBytes = 40

// fromArg reads the optional from field of the table at n: who says a
// line or asks a question, for example "Whisp" for the guide. It is shown
// as written, so it must be one short line of text, and no name that
// reads as the game master's or the server's (ReservedName).
func fromArg(L *lua.LState, t *lua.LTable, n int) string {
	switch v := t.RawGetString("from").(type) {
	case *lua.LNilType:
		return ""
	case lua.LString:
		s := string(v)
		if strings.TrimSpace(s) == "" || len(s) > MaxFromBytes || !utf8.ValidString(s) || strings.IndexFunc(s, notInLine) >= 0 {
			L.ArgError(n, "from must be a name of 1 to 40 bytes on one line")
		}
		if ReservedName(s) {
			L.ArgError(n, "from cannot name the game master, an admin or the server: only the engine speaks for them")
		}
		return s
	default:
		L.ArgError(n, "from must be a string")
		return ""
	}
}

// room.describe() sends the current room's title and description.
func (r *Runtime) describe(L *lua.LState) int {
	r.output(L).DescribeRoom()
	return 0
}

// room.go(id) moves the player to room id of the pack once the hook ends.
// The new room's on_enter runs then; the hook cannot wait in a dialog after
// room.go, so a move is never half done. The last room.go of a hook wins.
func (r *Runtime) goRoom(L *lua.LState) int {
	id := L.CheckString(1)
	host := r.output(L)
	if len(id) > 64 {
		L.ArgError(1, "room ids are at most 64 bytes")
	}
	if !host.Go(id) {
		L.ArgError(1, "no room "+strconv.Quote(id)+" in this pack")
	}
	r.cur.thread.moved = true
	return 0
}

// ui.dialog(title, options [, {from = name, timeout = seconds}]) shows a
// choice and waits until the player picks one. It returns the option's
// number (1-based) and its label, or nil once the timeout passed.
func (r *Runtime) dialog(L *lua.LState) int {
	d := r.dialogArgs(L, "ui.dialog")
	if L.GetTop() >= 3 && L.Get(3) != lua.LNil {
		opts := L.CheckTable(3)
		d.Voice.From = fromArg(L, opts, 3)
		d.Timeout = timeoutArg(L, opts, 3)
	}
	a := r.pause(L, &d)
	if a.expired {
		L.Push(lua.LNil)
		L.Push(lua.LNil)
		return 2
	}
	choices := a.choices
	L.Push(lua.LNumber(choices[0]))
	L.Push(lua.LString(d.Options[choices[0]-1]))
	return 2
}

// ui.select(title, options [, {min = 1, max = #options, from = name,
// timeout = seconds}]) lets the player pick several options and waits for
// them. It returns two lists: the numbers picked, in ascending order, and
// their labels; nil once the timeout passed.
func (r *Runtime) choose(L *lua.LState) int {
	d := r.dialogArgs(L, "ui.select")
	n := len(d.Options)
	d.Multiple, d.Min, d.Max = true, 1, n
	if L.GetTop() >= 3 && L.Get(3) != lua.LNil {
		limits := L.CheckTable(3)
		d.Min = countField(L, limits, "min", d.Min)
		d.Max = countField(L, limits, "max", d.Max)
		d.Voice.From = fromArg(L, limits, 3)
		d.Timeout = timeoutArg(L, limits, 3)
	}
	if d.Min < 0 || d.Max < 1 || d.Min > d.Max || d.Max > n {
		L.ArgError(3, "need 0 <= min <= max, 1 <= max <= number of options")
	}
	a := r.pause(L, &d)
	if a.expired {
		L.Push(lua.LNil)
		L.Push(lua.LNil)
		return 2
	}
	choices := a.choices
	nums, labels := L.CreateTable(len(choices), 0), L.CreateTable(len(choices), 0)
	for i, c := range choices {
		nums.RawSetInt(i+1, lua.LNumber(c))
		labels.RawSetInt(i+1, lua.LString(d.Options[c-1]))
	}
	L.Push(nums)
	L.Push(labels)
	return 2
}

// countField reads limits[name] as a whole number from -9 to 9, or def
// when it is nil.
func countField(L *lua.LState, limits *lua.LTable, name string, def int) int {
	switch v := limits.RawGetString(name).(type) {
	case *lua.LNilType:
		return def
	case lua.LNumber:
		f := float64(v)
		if f != math.Trunc(f) || f < -MaxDialogOptions || f > MaxDialogOptions {
			L.ArgError(3, name+" must be a whole number of options")
		}
		return int(f)
	default:
		L.ArgError(3, name+" must be a number")
		return 0
	}
}

// dialogArgs checks the title and options of ui.dialog and ui.select, and
// that there is a player to ask.
func (r *Runtime) dialogArgs(L *lua.LState, name string) Dialog {
	title := L.CheckString(1)
	opts := L.CheckTable(2)
	if len(title) > MaxTitleBytes {
		L.ArgError(1, "title longer than 200 bytes")
	}
	n := opts.Len()
	if n < 1 || n > MaxDialogOptions {
		L.ArgError(2, "a dialog needs 1 to 9 options")
	}
	d := Dialog{Title: title, Options: make([]string, n)}
	for i := range n {
		s, ok := opts.RawGetInt(i + 1).(lua.LString)
		if !ok || s == "" || len(s) > MaxOptionBytes {
			L.ArgError(2, "options must be strings of 1 to 200 bytes")
		}
		d.Options[i] = string(s)
	}
	if r.cur == nil || r.cur.host == nil {
		L.RaiseError("no player here: call %s from a hook such as on_enter", name)
	}
	return d
}

// ui.ask(question [, {strict = true, speak = true, show = false, from =
// name, timeout = seconds}]) waits for the player to type an answer and
// returns it, or nil once the timeout passed: one line, trimmed, with
// control characters turned into spaces. strict asks the client for no
// typing help (no autocorrect, autocomplete or suggestions), for spelling
// practice and riddles. speak and show work as in say. The script decides
// whether the answer is right, for example with words.same.
func (r *Runtime) ask(L *lua.LState) int {
	title := L.CheckString(1)
	if title == "" || len(title) > MaxTitleBytes {
		L.ArgError(1, "question must be 1 to 200 bytes")
	}
	d := Dialog{Title: title, Ask: true}
	if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
		opts := L.CheckTable(2)
		switch v := opts.RawGetString("strict").(type) {
		case *lua.LNilType:
		case lua.LBool:
			d.Strict = bool(v)
		default:
			L.ArgError(2, "strict must be true or false")
		}
		d.Voice = voiceArg(L, 2)
		d.Timeout = timeoutArg(L, opts, 2)
	}
	if r.cur == nil || r.cur.host == nil {
		L.RaiseError("no player here: call ui.ask from a hook such as on_enter")
	}
	a := r.pause(L, &d)
	if a.expired {
		L.Push(lua.LNil)
		return 1
	}
	L.Push(lua.LString(a.text))
	return 1
}

// ui.items({{id = "straw", label = "heap of straw"}, ...}) replaces the
// things in the room the player can click; on_click(player, id) runs on a
// click. An empty list clears them.
func (r *Runtime) items(L *lua.LState) int {
	list := L.CheckTable(1)
	n := list.Len()
	if n > MaxItems {
		L.ArgError(1, "more than 20 items")
	}
	items := make([]Item, n)
	seen := map[string]bool{}
	for i := range n {
		t, ok := list.RawGetInt(i + 1).(*lua.LTable)
		if !ok {
			L.ArgError(1, "items must be tables with an id and a label")
		}
		id, ok1 := t.RawGetString("id").(lua.LString)
		label, ok2 := t.RawGetString("label").(lua.LString)
		if !ok1 || !ok2 || !ValidItemID(string(id)) || !ValidLabel(string(label)) || seen[string(id)] {
			L.ArgError(1, "each item needs a unique id (a-z, 0-9, _ and -, at most 64) and a label: one line of 1 to 100 bytes")
		}
		seen[string(id)] = true
		items[i] = Item{ID: string(id), Label: string(label)}
	}
	r.output(L).Items(items)
	return 0
}

// ValidLabel reports whether s can be a label, of an item (in room YAML
// too) or a timer: one line of UTF-8 text, 1 to MaxLabelBytes long, not
// only spaces.
func ValidLabel(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= MaxLabelBytes && utf8.ValidString(s) && strings.IndexFunc(s, notInLine) < 0
}

// notInLine reports the characters a name or label may not hold: control
// characters, line and paragraph separators, and the bidi controls, which
// would turn the rest of the line around (emoji joiners stay allowed).
func notInLine(r rune) bool {
	switch {
	case unicode.IsControl(r), r == '\u2028', r == '\u2029':
		return true
	case r == '\u061c', r == '\u200e', r == '\u200f', r >= '\u202a' && r <= '\u202e', r >= '\u2066' && r <= '\u2069':
		return true
	}
	return false
}

// ValidItemID reports whether id can name an item: 1 to 64 of a-z, 0-9, _
// and -, starting with a letter or digit, like room ids.
func ValidItemID(id string) bool {
	if id == "" || len(id) > MaxItemIDBytes {
		return false
	}
	for i, c := range []byte(id) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '_' || c == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}

// ui.image(name [, caption]) shows one of the pack's images, from its
// assets/ folder, as a popup with an optional caption.
func (r *Runtime) image(L *lua.LState) int {
	name := L.CheckString(1)
	caption := L.OptString(2, "")
	if len(caption) > MaxCaptionBytes {
		L.ArgError(2, "caption longer than 200 bytes")
	}
	if !r.output(L).Image(name, caption) {
		L.ArgError(1, "the pack has no image named "+strconv.Quote(truncate(name, 64)))
	}
	return 0
}

// truncate cuts s to at most n bytes, for error messages.
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
