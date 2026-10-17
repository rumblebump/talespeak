package game

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

func shippedDemo(t *testing.T) *World {
	t.Helper()
	p, err := pack.Load(content.Demo())
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

func send(t *testing.T, s ws.Session, raw string) []string {
	t.Helper()
	var m ws.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return jsonLines(t, s.Handle(m))
}

func expect(t *testing.T, step string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s:\n%s\nwant:\n%s", step, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The shipped demo produces the exchange shown in
// docs/websocket-protocol.md, byte for byte (the room text is shortened
// there).
func TestDemoMatchesProtocolDoc(t *testing.T) {
	w := shippedDemo(t)
	s, first := w.NewSession("")
	defer s.Close()
	got := jsonLines(t, first)
	if len(got) != 3 || !strings.HasPrefix(got[0], `{"type":"room","id":"cell","title":"A damp cell","text":"Stone walls, `) ||
		!strings.HasSuffix(got[0], `","items":[{"id":"straw","label":"heap of straw"},{"id":"guard","label":"sleepy guard"},{"id":"scratches","label":"scratches on the wall"},{"id":"lock","label":"lock with letter wheels"}]}`) {
		t.Fatalf("first:\n%s", strings.Join(got, "\n"))
	}
	expect(t, "first texts", got[1:], []string{
		`{"type":"text","text":"A guard leans against the bars, half asleep."}`,
		`{"type":"text","text":"Click something in the cell, or type what you do."}`,
	})
	expect(t, "click", send(t, s, `{"type":"click","item":"guard"}`), []string{
		`{"type":"dialog","id":"d1","title":"What do you say to the guard, traveler?","options":["Ask for water","Ask what time it is","Say nothing"]}`,
	})
	expect(t, "choose", send(t, s, `{"type":"choose","id":"d1","option":1}`), []string{
		`{"type":"text","text":"The guard grunts and slides a cup of water through the bars."}`,
	})
	expect(t, "input", send(t, s, `{"type":"input","text":"search the straw"}`), []string{
		`{"type":"select","id":"d2","title":"Under the straw you find a few things. What do you keep? Pick up to two.","options":["A bent nail","A smooth pebble","A torn piece of cloth"],"min":1,"max":2}`,
	})
	expect(t, "choose several", send(t, s, `{"type":"choose","id":"d2","choices":[3,1]}`), []string{
		`{"type":"text","text":"You keep: A bent nail, A torn piece of cloth."}`,
		`{"type":"text","text":"The nail might open the lock."}`,
		`{"type":"items","items":[{"id":"guard","label":"sleepy guard"},{"id":"scratches","label":"scratches on the wall"},{"id":"lock","label":"lock with letter wheels"},{"id":"straw","label":"flattened straw"}]}`,
	})
	expect(t, "riddle", send(t, s, `{"type":"click","item":"lock"}`), []string{
		`{"type":"text","text":"Four letter wheels. Someone scratched a riddle next to them:"}`,
		`{"type":"ask","id":"d3","title":"I have bars but no drinks, and you are standing in me. What am I?","strict":true}`,
	})
	expect(t, "answer", send(t, s, `{"type":"answer","id":"d3","text":"Cell"}`), []string{
		`{"type":"text","text":"The wheels click into place: C, E, L, L. The lock opens!"}`,
	})
	got = send(t, s, `{"type":"click","item":"scratches"}`)
	if len(got) != 2 || !strings.HasPrefix(got[0], `{"type":"text","text":"You roll `) ||
		(!strings.HasPrefix(got[1], `{"type":"image","text":"A map scratched`) && !strings.HasPrefix(got[1], `{"type":"image","text":"Faint scratches`)) ||
		!strings.Contains(got[1], `"url":"/packs/demo/assets/wall-map-`) {
		t.Fatalf("image:\n%s", strings.Join(got, "\n"))
	}
}

func TestInput(t *testing.T) {
	w := scriptWorld(t, `function on_input(player, text) say("[" .. text .. "]") end`, script.DefaultLimits, quiet)
	s, _ := w.NewSession("")
	defer s.Close()
	for in, want := range map[string]string{
		"open door":                        "[open door]",
		"  spaces  ":                       "[spaces]",
		"two\nlines\r\tand\x00nul":         "[two lines  and nul]",
		"line\u2028sep":                    "[line sep]",
		"bad \xff utf8":                    "[bad \ufffd utf8]",
		strings.Repeat("x", MaxInputBytes): "[" + strings.Repeat("x", MaxInputBytes) + "]",
	} {
		got := s.Handle(ws.Message{Type: "input", Text: in})
		if len(got) != 1 || got[0].Text != want {
			t.Errorf("input %q = %+v, want %q", in, got, want)
		}
	}
	if got := s.Handle(ws.Message{Type: "input", Text: " \n\t "}); len(got) != 0 {
		t.Errorf("blank input = %+v, want nothing", got)
	}
	if got := s.Handle(ws.Message{Type: "input", Text: strings.Repeat("x", MaxInputBytes+1)}); kinds(got) != "error:" {
		t.Errorf("long input = %+v", got)
	}
}

func TestInputWithoutHook(t *testing.T) {
	w := scriptWorld(t, `x = 1`, script.DefaultLimits, quiet)
	s, _ := w.NewSession("")
	defer s.Close()
	if got := s.Handle(ws.Message{Type: "input", Text: "hello"}); len(got) != 1 || got[0].Text != "Nothing happens." {
		t.Errorf("got %+v", got)
	}
}

func TestInputWaitsForOpenDialog(t *testing.T) {
	w := scriptWorld(t, `function on_enter() ui.dialog("?", {"a"}) say("answered") end
function on_input() say("typed") end`, script.DefaultLimits, quiet)
	s, _ := w.NewSession("")
	defer s.Close()
	if got := s.Handle(ws.Message{Type: "input", Text: "hi"}); kinds(got) != "error:" {
		t.Errorf("input during dialog = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}); len(got) != 1 || got[0].Text != "answered" {
		t.Errorf("choose = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "input", Text: "hi"}); len(got) != 1 || got[0].Text != "typed" {
		t.Errorf("input after dialog = %+v", got)
	}
}

func TestClick(t *testing.T) {
	w := scriptWorld(t, `function on_enter() ui.items({{id = "box", label = "a box"}}) end
function on_click(player, id) say("clicked " .. id) ui.items({}) end`, script.DefaultLimits, quiet)
	s, first := w.NewSession("")
	defer s.Close()
	if kinds(first) != "items:" || len(first[0].Items) != 1 || first[0].Items[0] != (ws.Item{ID: "box", Label: "a box"}) {
		t.Fatalf("first = %+v", first)
	}
	if got := s.Handle(ws.Message{Type: "click", Item: "nope"}); kinds(got) != "error:" {
		t.Errorf("unknown item = %+v", got)
	}
	got := s.Handle(ws.Message{Type: "click", Item: "box"})
	if kinds(got) != "text: items:" || got[0].Text != "clicked box" || len(got[1].Items) != 0 {
		t.Fatalf("click = %+v", got)
	}
	// ui.items({}) removed the box.
	if got := s.Handle(ws.Message{Type: "click", Item: "box"}); kinds(got) != "error:" {
		t.Errorf("removed item = %+v", got)
	}
}

func TestRoomItemsFromYAML(t *testing.T) {
	w := shippedDemo(t)
	s, first := w.NewSession("")
	defer s.Close()
	if len(first[0].Items) != 4 {
		t.Fatalf("room items = %+v", first[0].Items)
	}
	if got := s.Handle(ws.Message{Type: "click", Item: "guard"}); kinds(got) != "dialog:d1" {
		t.Errorf("click on a YAML item = %+v", got)
	}
}

func TestSelectAnswers(t *testing.T) {
	w := scriptWorld(t, `function on_enter() local n = ui.select("?", {"a", "b", "c"}, {min = 0, max = 2}) say("n" .. #n) end`, script.DefaultLimits, quiet)
	s, first := w.NewSession("")
	defer s.Close()
	if kinds(first) != "select:d1" || first[0].Min != 0 || first[0].Max != 2 {
		t.Fatalf("first = %+v", first)
	}
	for _, bad := range []ws.Message{
		{Type: "choose", ID: "d1", Choices: []int{1, 2, 3}},
		{Type: "choose", ID: "d1", Choices: []int{2, 2}},
		{Type: "choose", ID: "d1", Choices: []int{4}},
		{Type: "choose", ID: "d1", Choices: []int{0}},
		{Type: "choose", ID: "d2", Choices: []int{1}},
	} {
		if got := s.Handle(bad); !isError(got) {
			t.Errorf("%+v = %+v", bad, got)
		}
	}
	// An option alone does not answer a select; with min 0 it is the empty answer.
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}); len(got) != 1 || got[0].Text != "n0" {
		t.Errorf("empty answer = %+v", got)
	}
}

func TestImageMessage(t *testing.T) {
	w := shippedDemo(t)
	s, _ := w.NewSession("")
	defer s.Close()
	sess := s.(*session)
	if sess.Image("missing.png", "") {
		t.Error("Image(missing) = true")
	}
	if !sess.Image("wall-map-clear.png", "cap") {
		t.Fatal("Image(wall-map-clear.png) = false")
	}
	m := sess.flush()
	if len(m) != 1 || m[0].Type != "image" || m[0].URL != "/packs/demo/assets/wall-map-clear.png" || m[0].Text != "cap" {
		t.Errorf("image message = %+v", m)
	}
}

func TestAskAnswers(t *testing.T) {
	w := scriptWorld(t, `function on_enter()
  local a = ui.ask("Spell it", {strict = true, speak = true, show = false})
  say("got [" .. a .. "]", {speak = true})
end`, script.DefaultLimits, quiet)
	s, first := w.NewSession("")
	defer s.Close()
	if kinds(first) != "ask:d1" || !first[0].Strict || !first[0].Speak || !first[0].Hide || len(first[0].Options) != 0 {
		t.Fatalf("first = %+v", first)
	}
	for _, bad := range []ws.Message{
		{Type: "choose", ID: "d1", Option: 1},
		{Type: "answer", ID: "d2", Text: "x"},
		{Type: "answer", ID: "d1", Text: " \n "},
		{Type: "answer", ID: "d1", Text: strings.Repeat("x", MaxInputBytes+1)},
		{Type: "input", Text: "x"},
	} {
		if got := s.Handle(bad); !isError(got) {
			t.Errorf("%+v = %+v", bad, got)
		}
	}
	got := s.Handle(ws.Message{Type: "answer", ID: "d1", Text: " cas\ttle\n"})
	if len(got) != 1 || got[0].Text != "got [cas tle]" || !got[0].Speak || got[0].Hide {
		t.Errorf("answer = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "answer", ID: "d1", Text: "again"}); kinds(got) != "error:" {
		t.Errorf("second answer = %+v", got)
	}
}

func TestAnswerOnChoiceDialog(t *testing.T) {
	w := scriptWorld(t, `function on_enter() ui.dialog("?", {"a"}) end`, script.DefaultLimits, quiet)
	s, _ := w.NewSession("")
	defer s.Close()
	if got := s.Handle(ws.Message{Type: "answer", ID: "d1", Text: "a"}); kinds(got) != "error:" {
		t.Errorf("answer on a dialog = %+v", got)
	}
	if got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}); len(got) != 0 {
		t.Errorf("dialog closed by the answer: %+v", got)
	}
}

// Each player runs their own instance of a room script: what one player
// typed never reaches another through script globals (T-0004 security
// review), and a closed session's globals are released.
func TestScriptStateIsPerPlayer(t *testing.T) {
	w := scriptWorld(t, `last = "nothing"
function on_input(player, text) say("before: " .. last) last = text end`, script.DefaultLimits, quiet)
	a, _ := w.NewSession("")
	defer a.Close()
	b, _ := w.NewSession("")
	defer b.Close()
	a.Handle(ws.Message{Type: "input", Text: "secret of a"})
	if got := b.Handle(ws.Message{Type: "input", Text: "hello"}); len(got) != 1 || got[0].Text != "before: nothing" {
		t.Errorf("b sees %+v", got)
	}
	if got := a.Handle(ws.Message{Type: "input", Text: "again"}); len(got) != 1 || got[0].Text != "before: secret of a" {
		t.Errorf("a's own state = %+v", got)
	}
}

func TestClosedSessionsReleaseTheirState(t *testing.T) {
	limits := script.Limits{Time: time.Second, CallMemory: 64 << 20, StateMemory: 8 << 20}
	w := scriptWorld(t, `keep = {} function on_enter() keep[1] = string.rep("x", 1e6) .. "!" say("ok") end`, limits, quiet)
	for i := range 50 {
		s, first := w.NewSession("")
		s.Close()
		if kinds(first) != "text:" {
			t.Fatalf("session %d = %+v: closed sessions' state still counts", i, first)
		}
	}
}

// One client address shares a Lua time budget across its sessions (T-0004
// security review): flooding typed lines cannot keep the world busy.
func TestClientLuaBudget(t *testing.T) {
	w, err := NewWorld(packWith(`function on_enter() ui.dialog("?", {"a"}) end
function on_input() say("ran") end`), script.DefaultLimits, quiet, WithClientBudget(100*time.Millisecond, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	now := time.Unix(1000, 0)
	w.now = func() time.Time { now = now.Add(600 * time.Millisecond); return now } // every call "takes" 600 ms
	a, first := w.NewSession("10.0.0.1")
	defer a.Close()
	if kinds(first) != "dialog:d1" {
		t.Fatalf("first = %+v", first)
	}
	// 1 s burst: the first session's two calls used 1.2 s (start and on_enter).
	b, first := w.NewSession("10.0.0.1")
	defer b.Close()
	if kinds(first) != "error:" || !strings.Contains(first[0].Message, "Too much at once") {
		t.Fatalf("second session of the same address = %+v", first)
	}
	// Answering is refused too, and the question stays open.
	if got := a.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}); kinds(got) != "error:d1" || !strings.Contains(got[0].Message, "Too much") {
		t.Fatalf("choose while busy = %+v", got)
	}
	// Another address is not affected.
	c, first := w.NewSession("10.0.0.2")
	defer c.Close()
	if kinds(first) != "dialog:d1" {
		t.Errorf("other address = %+v", first)
	}
	// After waiting, the budget has refilled: 30 s at 100 ms/s covers the debt.
	now = now.Add(30 * time.Second)
	if got := a.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}); len(got) != 0 {
		t.Errorf("choose after waiting = %+v", got)
	}
}

func TestClientBudgetsStayBounded(t *testing.T) {
	w := scriptWorld(t, `function on_enter() end`, script.DefaultLimits, quiet)
	for i := range maxBudgets + 50 {
		s, _ := w.NewSession("c" + strconv.Itoa(i))
		s.Close()
	}
	if n := len(w.budgets); n > maxBudgets {
		t.Errorf("%d budgets kept, cap %d", n, maxBudgets)
	}
}

// A client cannot push its own used-up budget out of a full map to get a
// fresh one (T-0004 security re-review): budgets in debt are kept, and a new
// client waits until one refills.
func TestUsedBudgetsAreNotEvicted(t *testing.T) {
	w := scriptWorld(t, `function on_enter() end`, script.DefaultLimits, quiet)
	now := time.Unix(1e9, 0)
	w.now = func() time.Time { return now }
	for i := range maxBudgets {
		w.budgets["c"+strconv.Itoa(i)] = &budget{left: -1, last: now}
	}
	s, first := w.NewSession("new")
	s.Close()
	if !isError(first) || !strings.Contains(first[0].Message, "Too much") {
		t.Errorf("new client while every budget is used up = %+v", first)
	}
	if w.budgets["c0"] == nil || len(w.budgets) != maxBudgets {
		t.Errorf("used-up budgets evicted: %d left", len(w.budgets))
	}
	now = now.Add(time.Minute) // every budget has refilled
	s, first = w.NewSession("new")
	s.Close()
	if isError(first) {
		t.Errorf("new client after the others refilled = %+v", first)
	}
}

func TestBudgetKey(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "",
		"192.0.2.1":               "192.0.2.1",
		"2001:db8:1:2::/64":       "2001:db8:1::/48",
		"2001:db8:1:ff00::/56":    "2001:db8:1::/48",
		"2001:db8::/32":           "2001:db8::/32",
		"::ffff:192.0.2.1":        "::ffff:192.0.2.1",
		"not an address":          "not an address",
		"2001:db8:1:2::1/128":     "2001:db8:1::/48",
		"2001:db8:ffff:ffff::/64": "2001:db8:ffff::/48",
	} {
		if got := budgetKey(in); got != want {
			t.Errorf("budgetKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// Two /64s of one /48 share one Lua time budget.
func TestIPv6PrefixSharesBudget(t *testing.T) {
	w, err := NewWorld(packWith(`function on_enter() ui.dialog("?", {"a"}) end`), script.DefaultLimits, quiet, WithClientBudget(100*time.Millisecond, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	now := time.Unix(1e9, 0)
	w.now = func() time.Time { return now }
	a, _ := w.NewSession("2001:db8:1:1::/64")
	defer a.Close()
	w.budgets["2001:db8:1::/48"].left = -1
	b, first := w.NewSession("2001:db8:1:2::/64")
	defer b.Close()
	if !isError(first) || !strings.Contains(first[0].Message, "Too much") {
		t.Errorf("second /64 of the same /48 = %+v", first)
	}
}
