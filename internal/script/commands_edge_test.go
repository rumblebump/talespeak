package script

import (
	"fmt"
	"strings"
	"testing"
)

// Commands reads the table raw: metatables run no Lua and add nothing.
func TestCommandsIgnoreMetatables(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	for _, tt := range []struct {
		name, src, want string
	}{
		{"__index on commands", `commands = setmetatable({}, {__index = function() error("ran") end})`, ""},
		{"__index on a spec", `commands = { roll = setmetatable({}, {__index = function() error("ran") end}) }`, `roll:"":false`},
		{"gm through __index", `commands = { roll = setmetatable({}, {__index = {gm = true, help = "hidden"}}) }`, `roll:"":false`},
		{"__pairs", `commands = setmetatable({}, {__pairs = function() error("ran") end})`, ""},
		{"array part", `commands = { {help = "x"}, {help = "y"}, roll = {help = "r"} }`, `roll:"r":false`},
		{"gm false", `commands = { roll = {gm = false}, skip = {gm = 0} }`, `roll:"":false skip:"":true`}, // 0 is true in Lua
	} {
		env, err := r.Load(tt.name+".lua", tt.src)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		var got []string
		for _, c := range env.Commands() {
			got = append(got, fmt.Sprintf("%s:%q:%v", c.Name, c.Help, c.GM))
		}
		if s := strings.Join(got, " "); s != tt.want {
			t.Errorf("%s: %s, want %s", tt.name, s, tt.want)
		}
	}
}

// Help lines: one line of at most MaxCommandHelpBytes, no bidi controls,
// not blank, and a string.
func TestCommandHelpLines(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	for _, tt := range []struct {
		help string // a Lua expression
		want string
	}{
		{`string.rep("x", 100)`, strings.Repeat("x", 100)},
		{`string.rep("é", 50)`, strings.Repeat("é", 50)}, // 100 bytes
		{`string.rep("é", 51)`, ""},
		{`"   "`, ""},
		{`5`, ""},
		{`true`, ""},
		{`"left\226\128\174right"`, ""}, // U+202E, right-to-left override
		{`"tab\there"`, ""},
		{`"\255\254"`, ""},
		{`" roll "`, "roll"},
	} {
		env, err := r.Load("help.lua", `commands = { c = { help = `+tt.help+` } }`)
		if err != nil {
			t.Fatalf("%s: %v", tt.help, err)
		}
		c := env.Commands()
		if len(c) != 1 || c[0].Help != tt.want {
			t.Errorf("help = %s: %+v, want %q", tt.help, c, tt.want)
		}
	}
}

// The table is read once, when the top level has run: a hook that changes
// it later changes nothing the engine offers.
func TestCommandsChangeAtRuntime(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env, err := r.Load("live.lua", `commands = { roll = {} }
function on_input(p, text)
  if text == "none" then commands = nil else commands[text] = { help = text } end
end`)
	if err != nil {
		t.Fatal(err)
	}
	h := &recorder{}
	if _, err := r.RunHook(h, env, Hook{Name: "on_input", Args: []string{"zap"}}); err != nil {
		t.Fatal(err)
	}
	if c := env.Commands(); len(c) != 1 || c[0].Name != "roll" {
		t.Errorf("after adding zap: %+v", c)
	}
	if _, err := r.RunHook(h, env, Hook{Name: "on_input", Args: []string{"none"}}); err != nil {
		t.Fatal(err)
	}
	if c := env.Commands(); len(c) != 1 || c[0].Name != "roll" {
		t.Errorf("after commands = nil: %+v", c)
	}
}

// FuzzValidCommand: a valid name is short, lower case ASCII and safe to
// put after a slash in a notice.
func FuzzValidCommand(f *testing.F) {
	for _, s := range []string{"help", "a", "-a", "a-b_c", "A", "é", "", "a b", strings.Repeat("a", 21), "a\x00", "9lives"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if !ValidCommand(name) {
			return
		}
		if len(name) == 0 || len(name) > MaxCommandBytes || strings.ToLower(name) != name {
			t.Errorf("ValidCommand(%q) is true", name)
		}
		for _, r := range name {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				t.Errorf("ValidCommand(%q): holds %q", name, r)
			}
		}
		if name[0] == '-' || name[0] == '_' {
			t.Errorf("ValidCommand(%q): starts with %q", name, name[0])
		}
	})
}
