package script

import (
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// words: comparing what a player typed with the words a script expects.

// normalize lowercases s, trims it and turns every run of spaces into
// one, so "  The  Castle " and "the castle" compare equal.
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// words.normalize(s) returns s lowercased, trimmed, with single spaces.
func wordsNormalize(L *lua.LState) int {
	L.Push(lua.LString(normalize(L.CheckString(1))))
	return 1
}

// words.same(a, b) reports whether a and b are the same words, ignoring
// case and extra spaces: the exact check for spelling practice.
func wordsSame(L *lua.LState) int {
	L.Push(lua.LBool(normalize(L.CheckString(1)) == normalize(L.CheckString(2))))
	return 1
}
