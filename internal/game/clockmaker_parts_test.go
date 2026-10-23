package game

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The clockmaker's case module (lib/case.lua) installs its parts
// (lib/case_*.lua) with require(name)(case). These tests check that every
// function of every part reaches every room, at world start and in each
// room's own instance, and that the parts reach the main module and each
// other through case.x.

var (
	caseDefRE     = regexp.MustCompile(`(?m)^(?:function case\.([A-Za-z_]\w*)|case\.([A-Za-z_]\w*)\s*=)`)
	caseUseRE     = regexp.MustCompile(`\bcase\.([A-Za-z_]\w*)`)
	caseInstallRE = regexp.MustCompile(`(?s)for _, name in ipairs\(\{([^}]*)\}\) do\s*require\(name\)\(case\)`)
	luaCommentRE  = regexp.MustCompile(`--[^\n]*`)
	luaStringRE   = regexp.MustCompile(`"[^"\n]*"`)
)

// caseNames is every case.x that lib/case*.lua defines, sorted.
func caseNames(t *testing.T, p *pack.Pack) []string {
	t.Helper()
	var names []string
	for name, src := range p.Modules {
		if name != "case" && !strings.HasPrefix(name, "case_") {
			continue
		}
		for _, m := range caseDefRE.FindAllStringSubmatch(src, -1) {
			names = append(names, m[1]+m[2])
		}
	}
	slices.Sort(names)
	return names
}

// TestClockmakerCaseInstallsEveryPart: case.lua's install list is exactly
// the lib/case_*.lua files, no case.x is defined twice (a part would
// overwrite another's), and every case.x a room or a module uses is
// defined somewhere.
func TestClockmakerCaseInstallsEveryPart(t *testing.T) {
	p := clockmakerPack(t)
	m := caseInstallRE.FindStringSubmatch(p.Modules["case"])
	if m == nil {
		t.Fatal("lib/case.lua: no install loop require(name)(case)")
	}
	var installed []string
	for _, s := range strings.Split(m[1], ",") {
		if s = strings.TrimSpace(s); s != "" {
			name, err := strconv.Unquote(s)
			if err != nil {
				t.Fatalf("install list entry %s: %v", s, err)
			}
			installed = append(installed, name)
		}
	}
	var parts []string
	for name := range p.Modules {
		if strings.HasPrefix(name, "case_") {
			parts = append(parts, name)
		}
	}
	slices.Sort(parts)
	if got := slices.Sorted(slices.Values(installed)); !slices.Equal(got, parts) {
		t.Errorf("case.lua installs %v, lib has the parts %v", installed, parts)
	}

	names := caseNames(t, p)
	defined := map[string]bool{}
	for _, n := range names {
		if defined[n] {
			t.Errorf("case.%s is defined twice", n)
		}
		defined[n] = true
	}
	for _, want := range []string{"arrive", "FULL", "give", "status", "crate", "ready", "input"} {
		if !defined[want] {
			t.Errorf("case.%s is not defined (the name list is wrong)", want)
		}
	}

	srcs := map[string]string{}
	for name, src := range p.Modules {
		srcs["lib/"+name+".lua"] = src
	}
	for _, r := range p.Rooms {
		if r.Source != "" {
			srcs["scripts/"+r.Script] = r.Source
		}
	}
	for file, src := range srcs {
		code := luaStringRE.ReplaceAllString(luaCommentRE.ReplaceAllString(src, ""), `""`)
		for _, m := range caseUseRE.FindAllStringSubmatch(code, -1) {
			if !defined[m[1]] {
				t.Errorf("%s uses case.%s, which no lib/case*.lua defines", file, m[1])
			}
		}
	}

	// A part cannot see case.lua's locals (LEVEL_OF, FULL, count, ...):
	// a bare use there is a global, nil when the hook runs.
	for _, m := range regexp.MustCompile(`(?m)^local (?:function )?([A-Za-z_]\w*)`).FindAllStringSubmatch(p.Modules["case"], -1) {
		local := m[1]
		if local == "case" {
			continue
		}
		bare := regexp.MustCompile(`(^|[^.\w])` + local + `\b`)
		for _, part := range parts {
			code := luaStringRE.ReplaceAllString(luaCommentRE.ReplaceAllString(p.Modules[part], ""), `""`)
			if bare.MatchString(code) {
				t.Errorf("lib/%s.lua uses %s, a local of case.lua it cannot see: use case.%s", part, local, local)
			}
		}
	}
}

// probeLua is added at the end of every room script, where the room's
// local case is in scope. At the top level (world start, and each new
// instance) it fails when case lacks a name; on_enter says what case
// lacks and calls across the parts before the room's own on_enter runs.
func probeLua(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return `
local probe_names = { ` + strings.Join(quoted, ", ") + ` }
local function probe_missing()
  local out = {}
  for _, n in ipairs(probe_names) do
    if case[n] == nil then
      out[#out + 1] = n
    end
  end
  return table.concat(out, " ")
end
if probe_missing() ~= "" then
  error("case lacks at the top level: " .. probe_missing())
end
local probe_enter = on_enter
function on_enter(player)
  say("probe " .. player.room .. ": [" .. probe_missing() .. "]")
  -- bags -> bags and main; secrets -> secrets and main; input -> main
  say("probe calls " .. player.room .. ": " .. tostring(case.used_up("map_2")) .. " " ..
      tostring(case.carries(player.id, "lens")) .. " " .. tostring(case.holder("lens")) .. " " ..
      case.crate() .. " " .. case.status(player) .. " " .. case.FULL)
  return probe_enter(player)
end
`
}

// probedWorld is the clockmaker with probeLua in every room, and with
// edit applied to its modules first.
func probedWorld(t *testing.T, edit func(mods map[string]string)) (*World, error) {
	t.Helper()
	p := clockmakerPack(t)
	names := caseNames(t, p)
	if edit != nil {
		edit(p.Modules)
	}
	probe := probeLua(names)
	for _, r := range p.Rooms {
		if r.Source != "" {
			r.Source += probe
		}
	}
	c := newClock()
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithClock(c.Now, c.After))
	if err == nil {
		t.Cleanup(w.Close)
	}
	return w, err
}

// Every room, in the instance of a player who enters it, sees every
// function of the case and its parts, and the parts' functions reach the
// main module (case.get, case.secret, case.LEVELS) and their own part.
func TestClockmakerCasePartsInEveryRoom(t *testing.T) {
	w, err := probedWorld(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, first := w.NewSession("", ws.Who{Name: "Gm", GM: true})
	if s == nil {
		t.Fatalf("refused: %+v", first)
	}
	t.Cleanup(func() { s.Close() })
	callsRE := regexp.MustCompile(`probe calls (\w+): true false nil (K8|H3|S4) Level 1 of 5: 221B Baker Street\. Made for 1\. Irregulars: [^.]*\. ` + regexp.QuoteMeta(fullWorld))
	check := func(room, text string) {
		t.Helper()
		want(t, room, text, "probe "+room+": []")
		ok := false
		for _, m := range callsRE.FindAllStringSubmatch(text, -1) {
			ok = ok || m[1] == room
		}
		if !ok {
			t.Errorf("%s: no probe calls line matching %s in %q", room, callsRE, text)
		}
	}
	check("study", caseText(first))
	rooms := slices.Sorted(func(yield func(string) bool) {
		for id := range w.pack.Rooms {
			if !yield(id) {
				return
			}
		}
	})
	if len(rooms) != 9 {
		t.Errorf("rooms %v, want the 9 of the case", rooms)
	}
	for _, room := range rooms {
		// /go room runs the probe in a new instance of that room; the
		// case then sends the player back to the party's level.
		got := s.Handle(ws.Message{Type: "input", Text: "/go " + room})
		check(room, caseText(got))
	}
}

// The probe fails when a part is missing from case.lua's install list,
// when a part's install function is broken, and when case.FULL is gone:
// so the two tests above would catch a split gone wrong.
func TestClockmakerCaseProbeCatchesBrokenParts(t *testing.T) {
	cases := []struct {
		name string
		edit func(mods map[string]string)
		want string
	}{
		{"part not installed", func(m map[string]string) {
			m["case"] = strings.Replace(m["case"], `"case_bags", `, "", 1)
		}, "case lacks at the top level: bag carries give holder show_bag used_up"},
		{"install function returns nothing", func(m map[string]string) {
			m["case_spots"] = strings.Replace(m["case_spots"], "return function(m)", "local _ = function(m)", 1)
		}, "attempt to call"},
		{"FULL gone", func(m map[string]string) {
			m["case"] = strings.Replace(m["case"], "case.FULL = FULL", "", 1)
		}, "case lacks at the top level: FULL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := probedWorld(t, func(m map[string]string) {
				before := m["case"] + m["case_spots"]
				tc.edit(m)
				if m["case"]+m["case_spots"] == before {
					t.Fatal("the edit changed nothing: the module source moved on")
				}
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewWorld: %v, want an error with %q", err, tc.want)
			}
		})
	}
}

// The pack's lib folder on disk holds exactly what the pack loaded: no
// case part is left out of pack.Load (the lib name rules).
func TestClockmakerCasePartsOnDisk(t *testing.T) {
	p := clockmakerPack(t)
	files, err := fs.Glob(os.DirFS(clockmakerDir), "lib/*.lua")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".lua")
		if _, ok := p.Modules[name]; !ok {
			t.Errorf("%s is not a module of the loaded pack", f)
		}
	}
	if len(files) != len(p.Modules) || len(files) < 5 {
		t.Errorf("lib has %d files, the pack %d modules", len(files), len(p.Modules))
	}
}
