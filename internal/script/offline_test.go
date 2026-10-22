package script

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// reachable lists every name a script can read from the shared globals,
// following the read-only library proxies to the tables behind them.
func reachable(r *Runtime) []string {
	var names []string
	var walk func(prefix string, t *lua.LTable)
	walk = func(prefix string, t *lua.LTable) {
		if r.locked[t] {
			if meta, ok := t.Metatable.(*lua.LTable); ok {
				if inner, ok := meta.RawGetString("__index").(*lua.LTable); ok {
					t = inner
				}
			}
		}
		t.ForEach(func(k, v lua.LValue) {
			name := prefix + k.String()
			names = append(names, name)
			if sub, ok := v.(*lua.LTable); ok {
				walk(name+".", sub)
			}
		})
	}
	walk("", r.lib)
	slices.Sort(names)
	return names
}

// No network: the whole surface a script reaches is this list, so a new
// library (http, socket, io, os) or a new function cannot appear without
// this test and docs/lua-sandbox.md changing with it.
func TestSandboxSurfaceIsExactly(t *testing.T) {
	r := New(DefaultLimits)
	defer r.Close()
	want := []string{
		"_VERSION", concatName, "assert", "error", "getmetatable", "ipairs",
		"math", "math.abs", "math.acos", "math.asin", "math.atan", "math.atan2", "math.ceil",
		"math.cos", "math.cosh", "math.deg", "math.exp", "math.floor", "math.fmod", "math.frexp",
		"math.huge", "math.ldexp", "math.log", "math.log10", "math.max", "math.min", "math.mod",
		"math.modf", "math.pi", "math.pow", "math.rad", "math.random", "math.randomseed",
		"math.sin", "math.sinh", "math.sqrt", "math.tan", "math.tanh",
		"next", "pairs", "pcall", "rawequal", "rawget", "rawset", "require",
		"room", "room.describe", "room.go", "room.players", "say", "select", "setmetatable", "state", "state.player", "state.player.get", "state.player.set",
		"state.world", "state.world.get", "state.world.set", "state.world.update",
		"string", "string.byte", "string.char", "string.find", "string.format", "string.gfind",
		"string.gmatch", "string.gsub", "string.len", "string.lower", "string.match", "string.rep",
		"string.reverse", "string.sub", "string.upper",
		"table", "table.concat", "table.getn", "table.insert", "table.maxn", "table.remove", "table.sort",
		"timer", "timer.cancel", "timer.left", "timer.start",
		"tonumber", "tostring", "type",
		"ui", "ui.ask", "ui.dialog", "ui.image", "ui.items", "ui.select",
		"unpack", "words", "words.normalize", "words.same", "world", "world.players", "xpcall",
	}
	slices.Sort(want)
	got := reachable(r)
	for _, name := range got {
		if !slices.Contains(want, name) {
			t.Errorf("scripts can reach %s, which is not on the list", name)
		}
	}
	for _, name := range want {
		if !slices.Contains(got, name) {
			t.Errorf("%s is listed but not reachable", name)
		}
	}
	for _, name := range got {
		root, _, _ := strings.Cut(name, ".")
		switch root {
		case "io", "os", "net", "http", "socket", "package", "debug", "load", "loadstring", "dofile", "loadfile", "channel":
			t.Errorf("scripts can reach %s", name)
		}
	}
}

// The script package cannot open a connection: nothing it links imports
// net. (gopher-lua's own os and io libraries are linked but never opened;
// TestSandboxSurfaceIsExactly keeps them out of reach.)
func TestScriptPackageDoesNotLinkNet(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: runs the go tool")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}
	cmd := exec.Command(goTool, "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := strings.Fields(string(out))
	if !slices.Contains(deps, "github.com/yuin/gopher-lua") {
		t.Fatalf("go list -deps looks wrong: %d packages, no gopher-lua", len(deps))
	}
	for _, dep := range deps {
		if dep == "net" || strings.HasPrefix(dep, "net/") || dep == "crypto/tls" {
			t.Errorf("internal/script links %s", dep)
		}
	}
}
