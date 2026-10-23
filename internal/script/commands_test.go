package script

import (
	"fmt"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env, err := r.Load("cmds.lua", `
commands = {
  skip = { help = "skip a turn", gm = true },
  roll = { help = "  roll a die  " },
  ["no-help"] = {},
  ["Upper"] = { help = "bad name" },
  ["-dash"] = { help = "bad name" },
  ["toolong-toolong-toolong"] = { help = "longer than 20" },
  bad_help = { help = "two\nlines" },
  long_help = { help = string.rep("x", 101) },
  number = 5,
  [1] = { help = "a number key" },
  gm_text = { gm = "yes" },
}`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range env.Commands() {
		got = append(got, fmt.Sprintf("%s:%q:%v", c.Name, c.Help, c.GM))
	}
	want := `bad_help:"":false gm_text:"":true long_help:"":false no-help:"":false roll:"roll a die":false skip:"skip a turn":true`
	if strings.Join(got, " ") != want {
		t.Errorf("commands =\n%s\nwant\n%s", strings.Join(got, " "), want)
	}

	// None, or not a table.
	for _, src := range []string{`x = 1`, `commands = "skip"`, `commands = {}`} {
		env, err := r.Load("none.lua", src)
		if err != nil {
			t.Fatal(err)
		}
		if c := env.Commands(); len(c) != 0 {
			t.Errorf("%s: %+v", src, c)
		}
	}
	var none *Env
	if c := none.Commands(); c != nil {
		t.Errorf("nil env: %+v", c)
	}
}

// A room offers at most MaxCommands, and the engine looks at no more than
// 4*MaxCommands entries of the table, so a huge one costs nothing.
func TestCommandsCap(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	env, err := r.Load("many.lua", `
commands = {}
for i = 1, 30 do commands["c" .. i] = { help = "n" } end`)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(env.Commands()); n != MaxCommands {
		t.Errorf("listed %d of 30, want %d", n, MaxCommands)
	}
	env, err = r.Load("junk.lua", `
commands = {}
for i = 1, 5000 do commands["c" .. i] = true end`)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(env.Commands()); n != 0 {
		t.Errorf("listed %d of junk", n)
	}
}

func TestValidCommand(t *testing.T) {
	for name, ok := range map[string]bool{"help": true, "a": true, "skip-turn": true, "a_b": true, "9": true,
		"": false, "Help": false, "-a": false, "_a": false, "a b": false, "é": false, strings.Repeat("a", 21): false, strings.Repeat("a", 20): true} {
		if ValidCommand(name) != ok {
			t.Errorf("ValidCommand(%q) = %v", name, !ok)
		}
	}
}
