package game

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

func scriptWorld(t *testing.T, src string, limits script.Limits, log *slog.Logger, opts ...Option) *World {
	t.Helper()
	w, err := NewWorld(packWith(src), limits, log, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

func mustDemo() *pack.Pack {
	p, err := pack.Load(os.DirFS("testdata/dialog"))
	if err != nil {
		panic(err)
	}
	return p
}

func jsonLines(t *testing.T, ms []ws.Message) []string {
	t.Helper()
	var out []string
	for _, m := range ms {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}

func TestSessionsAreIndependent(t *testing.T) {
	w := demoWorld(t)
	a, _ := w.NewSession("")
	defer a.Close()
	b, _ := w.NewSession("")
	defer b.Close()
	// Both start with their own d1.
	if got := kinds(a.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "text: dialog:d2" {
		t.Fatalf("a = %s", got)
	}
	if got := kinds(b.Handle(ws.Message{Type: "choose", ID: "d2", Option: 1})); got != "error:" {
		t.Errorf("b answered a's dialog: %s", got)
	}
	if got := kinds(b.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2})); got != "text:" {
		t.Errorf("b = %s", got)
	}
	if got := kinds(a.Handle(ws.Message{Type: "choose", ID: "d2", Option: 2})); got != "text:" {
		t.Errorf("a after b finished = %s", got)
	}
}

// Sessions on one world run on their own goroutines; -race checks the
// world's locking.
func TestConcurrentSessions(t *testing.T) {
	w := demoWorld(t)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, first := w.NewSession("")
			defer s.Close()
			if kinds(first) != "room:cell text: dialog:d1" {
				errs <- fmt.Errorf("session %d first = %s", i, kinds(first))
				return
			}
			for n := 1; n <= 10; n++ {
				id := fmt.Sprintf("d%d", n)
				if got := kinds(s.Handle(ws.Message{Type: "choose", ID: id, Option: 1 + 2*(n%2)})); got != fmt.Sprintf("text: dialog:d%d", n+1) {
					errs <- fmt.Errorf("session %d choice %d = %s", i, n, got)
					return
				}
			}
			if got := kinds(s.Handle(ws.Message{Type: "choose", ID: "d11", Option: 2})); got != "text:" {
				errs <- fmt.Errorf("session %d end = %s", i, got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestChooseEdgeCases(t *testing.T) {
	w := demoWorld(t)
	s, _ := w.NewSession("")
	defer s.Close()
	for _, m := range []ws.Message{
		{Type: "choose"},
		{Type: "choose", ID: "", Option: 1},
		{Type: "choose", ID: "D1", Option: 1},
		{Type: "choose", ID: "d1 ", Option: 1},
		{Type: "choose", ID: "d0", Option: 1},
		{Type: "choose", ID: "d2", Option: 1},
		{Type: "Choose", ID: "d1", Option: 1},
		{Type: "text", Text: "hello"},
		{Type: "welcome"},
		{Type: ""},
	} {
		got := s.Handle(m)
		if kinds(got) != "error:" || got[0].Message == "" {
			t.Errorf("%+v: %+v", m, got)
		}
	}
	// The dialog is still open after all of that.
	if got := kinds(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2})); got != "text:" {
		t.Errorf("valid choice = %s", got)
	}
}

func TestChooseInRoomWithoutDialog(t *testing.T) {
	w := scriptWorld(t, `function on_enter() say("hi") end`, script.DefaultLimits, quiet)
	s, first := w.NewSession("")
	defer s.Close()
	if kinds(first) != "text:" {
		t.Fatalf("first = %+v", first)
	}
	if got := kinds(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "error:" {
		t.Errorf("choose = %s", got)
	}
}

func TestClosedSessionHasNoDialog(t *testing.T) {
	w := demoWorld(t)
	s, _ := w.NewSession("")
	s.Close()
	if got := kinds(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "error:" {
		t.Errorf("choose after Close = %s", got)
	}
}

// A hook that fails after a dialog reports the failure and leaves no dialog
// open.
func TestScriptErrorAfterDialog(t *testing.T) {
	w := scriptWorld(t, `function on_enter() ui.dialog("?", {"a"}) say("x") error("late") end`, script.DefaultLimits, quiet)
	s, first := w.NewSession("")
	defer s.Close()
	if kinds(first) != "dialog:d1" {
		t.Fatalf("first = %s", kinds(first))
	}
	got := s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})
	if kinds(got) != "text: error:" || !strings.Contains(got[1].Message, "failed") || strings.Contains(got[1].Message, "late") {
		t.Errorf("choose = %+v", got)
	}
	if got := kinds(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "error:" {
		t.Errorf("second choose = %s", got)
	}
}

// docs/lua-sandbox.md: the log gets the room and the Lua error with its line.
func TestScriptFailureIsLogged(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil))
	w := scriptWorld(t, "function on_enter()\n  error(\"boom\")\nend", script.DefaultLimits, log)
	s, _ := w.NewSession("")
	defer s.Close()
	mu.Lock()
	got := buf.String()
	mu.Unlock()
	for _, want := range []string{"room=r", "scripts/r.lua:2", "boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("log %q does not contain %q", got, want)
		}
	}
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestPlayerError(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{script.ErrTimeout, "limits"},
		{script.ErrMemory, "limits"},
		{script.ErrStateMemory, "limits"},
		{fmt.Errorf("wrapped: %w", script.ErrMemory), "limits"},
		{script.ErrScript, "failed"},
		{fmt.Errorf("%w: x.lua:1: secret", script.ErrScript), "failed"},
		{errors.New("anything else"), "failed"},
	}
	for _, tt := range tests {
		got := playerError(tt.err)
		if !strings.Contains(got, tt.want) || !strings.Contains(got, "game master") || strings.Contains(got, "secret") {
			t.Errorf("playerError(%v) = %q", tt.err, got)
		}
	}
}

// When the world's scripts together keep too much, new players are told
// the world is full; once players leave it works again (T-0004 security
// re-review: it used to fail every call until a restart).
func TestFullWorldRecovers(t *testing.T) {
	limits := script.Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 2 << 20, StateMemory: 8 << 20}
	w := scriptWorld(t, `keep = {} function on_enter() keep[1] = string.rep("x", 1e6) .. "!" say("ok") end`, limits, quiet)
	var open []ws.Session
	full := false
	for range 50 {
		s, first := w.NewSession("")
		open = append(open, s)
		if kinds(first) == "error:" {
			if !strings.Contains(first[0].Message, "full") {
				t.Fatalf("message = %q", first[0].Message)
			}
			full = true
			break
		}
	}
	if !full {
		t.Fatal("50 sessions keeping 1 MiB each never filled an 8 MiB world")
	}
	for _, s := range open {
		s.Close()
	}
	for i := range 3 {
		s, first := w.NewSession("")
		defer s.Close()
		if kinds(first) != "text:" {
			t.Errorf("session %d after the others left = %+v", i, first)
		}
	}
}

// Many players of a pack with a word list at the top level: each player's
// instance keeps its own copy, so the world limit must allow for a full
// server (T-0004 security re-review: 303 sessions broke the world).
func TestManyPlayersWithTopLevelData(t *testing.T) {
	w := scriptWorld(t, `words = {} for i = 1, 2000 do words[i] = "word" .. i end
function on_enter() say(words[#words]) end`, script.DefaultLimits, quiet)
	var open []ws.Session
	defer func() {
		for _, s := range open {
			s.Close()
		}
	}()
	for i := range 400 {
		s, first := w.NewSession("")
		open = append(open, s)
		if kinds(first) != "text:" {
			t.Fatalf("session %d = %+v", i, first)
		}
	}
}

// One player keeping too much loses only their own state in that room: the
// next visit starts a new instance, and other players are not affected.
func TestPlayerMemoryResetsOnlyThatPlayer(t *testing.T) {
	limits := script.Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 2 << 20, StateMemory: 64 << 20}
	w := scriptWorld(t, `keep = {} function on_enter() say("n" .. #keep) end
function on_input(p, text) keep[#keep + 1] = string.rep("x", 1e6) .. #keep say("kept") end`, limits, quiet)
	a, _ := w.NewSession("")
	defer a.Close()
	b, _ := w.NewSession("")
	defer b.Close()
	if got := b.Handle(ws.Message{Type: "input", Text: "x"}); kinds(got) != "text:" {
		t.Fatalf("b = %+v", got)
	}
	var got []ws.Message
	for range 10 {
		if got = a.Handle(ws.Message{Type: "input", Text: "x"}); kinds(got) != "text:" {
			break
		}
	}
	// The output so far, then the error.
	if !strings.HasSuffix(kinds(got), "error:") || !strings.Contains(got[len(got)-1].Message, "limits") {
		t.Fatalf("a keeping 10 MiB with a 2 MiB player limit = %+v", got)
	}
	if got := a.Handle(ws.Message{Type: "input", Text: "x"}); kinds(got) != "text:" || got[0].Text != "kept" {
		t.Errorf("a after the reset = %+v", got)
	}
	if got := b.Handle(ws.Message{Type: "input", Text: "x"}); kinds(got) != "text:" {
		t.Errorf("b after a's reset = %+v", got)
	}
}

// BUG: a hook that calls ui.dialog from a table.sort comparator makes
// script.Runtime.Run panic (see script's TestDialogAcrossGoCallDoesNotPanic).
// session.enter (game.go:95-97) and session.choose (game.go:111-113) unlock
// World.mu without defer, so after net/http recovers the panic the world's
// lock stays held and every later player hangs.
func TestPanickingHookDoesNotLockWorld(t *testing.T) {
	w, err := NewWorld(packWith(`function on_enter() table.sort({3, 1, 2}, function(a, b) ui.dialog("?", {"a"}) return a < b end) end`), script.DefaultLimits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	enter := func() <-chan any {
		done := make(chan any, 1)
		go func() {
			defer func() { done <- recover() }()
			s, _ := w.NewSession("")
			s.Close()
		}()
		return done
	}
	if p := <-enter(); p != nil {
		t.Errorf("NewSession panicked: %v", p)
	}
	select {
	case <-enter():
		w.Close()
	case <-time.After(2 * time.Second):
		// Not closing w: Close would block on the same lock.
		t.Fatal("second session still waiting for the world after 2 s: World.mu was left locked")
	}
}

// FuzzSessionHandle sends arbitrary messages to a session waiting in the
// demo's dialog: it must answer every one, and anything but the right
// choice leaves the dialog open.
func FuzzSessionHandle(f *testing.F) {
	f.Add("choose", "d1", 2, "")
	f.Add("choose", "d1", 0, "")
	f.Add("choose", "d1", -9, "")
	f.Add("ping", "", 0, "")
	f.Add("text", "d1", 1, "<script>")
	f.Add("input", "", 0, " \n ")
	f.Add("click", "", 0, "")
	f.Add("answer", "d1", 0, "x")
	w, err := NewWorld(mustDemo(), script.DefaultLimits, quiet)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(w.Close)
	f.Fuzz(func(t *testing.T, typ, id string, option int, text string) {
		s, _ := w.NewSession("")
		defer s.Close()
		got := s.Handle(ws.Message{Type: typ, ID: id, Option: option, Text: text, Options: []string{text}})
		// A blank typed line is ignored without an answer.
		if typ == "input" && cleanInput(text) == "" {
			if len(got) != 0 {
				t.Fatalf("blank input answered with %+v", got)
			}
			return
		}
		if len(got) == 0 {
			t.Fatal("no answer")
		}
		valid := typ == "choose" && id == "d1" && option >= 1 && option <= 3
		if !valid {
			if !isError(got) {
				t.Fatalf("invalid message answered with %+v", got)
			}
			if again := kinds(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2})); again != "text:" {
				t.Fatalf("dialog closed by an invalid message: %s", again)
			}
		}
	})
}

// The players of one address keep at most their share of a world's script
// state, so a few addresses with many connections cannot fill the world
// and keep everyone else out (T-0004 security re-review). A class of 30
// behind one address fits with a 10000-word list.
func TestClientMemoryShare(t *testing.T) {
	slow := script.DefaultLimits
	slow.Time = 10 * time.Second // the top level takes longer than 100 ms with -race
	w := scriptWorld(t, `words = {} for i = 1, 10000 do words[i] = "word" .. i end
function on_enter() say(words[#words]) end`, slow, quiet, WithClientBudget(time.Second, time.Hour))
	var open []ws.Session
	closeAll := func() {
		for _, s := range open {
			s.Close()
		}
		open = nil
	}
	defer closeAll()
	n := 0
	for ; n < 200; n++ {
		s, first := w.NewSession("192.0.2.1")
		open = append(open, s)
		if kinds(first) == "error:" {
			if !strings.Contains(first[0].Message, "your network") {
				t.Fatalf("message = %q", first[0].Message)
			}
			break
		}
	}
	if n < 30 || n == 200 {
		t.Fatalf("one address got %d sessions with a 10000-word list, want at least 30 and a limit", n)
	}
	s, first := w.NewSession("198.51.100.1")
	open = append(open, s)
	if kinds(first) != "text:" {
		t.Errorf("another address while the first keeps its share = %+v", first)
	}
	closeAll()
	s, first = w.NewSession("192.0.2.1")
	open = append(open, s)
	if kinds(first) != "text:" {
		t.Errorf("first address after its players left = %+v", first)
	}
	if len(w.clientEnvs) != 1 || len(w.clientEnvs["192.0.2.1"]) != 1 {
		t.Errorf("clientEnvs = %d clients, want the one open session", len(w.clientEnvs))
	}
}
