package game

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

func scriptWorld(t *testing.T, src string, limits script.Limits, log *slog.Logger) *World {
	t.Helper()
	w, err := NewWorld(packWith(src), limits, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

func mustDemo() *pack.Pack {
	p, err := pack.Load(content.Demo())
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

// The demo produces the exchange shown in docs/websocket-protocol.md,
// byte for byte (the room text is shortened there).
func TestDemoMatchesProtocolDoc(t *testing.T) {
	w := demoWorld(t)
	s, first := w.NewSession()
	defer s.Close()
	got := jsonLines(t, first)
	want := []string{
		`{"type":"room","id":"cell","title":"A damp cell","text":"Stone walls, `,
		`{"type":"text","text":"A guard leans against the bars, half asleep."}`,
		`{"type":"dialog","id":"d1","title":"What do you do, traveler?","options":["Ask the guard for water","Search the straw","Wait quietly"]}`,
	}
	if len(got) != len(want) || !strings.HasPrefix(got[0], want[0]) || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("first messages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var choose ws.Message
	if err := json.Unmarshal([]byte(`{"type":"choose","id":"d1","option":2}`), &choose); err != nil {
		t.Fatal(err)
	}
	got = jsonLines(t, s.Handle(choose))
	if len(got) != 1 || got[0] != `{"type":"text","text":"Under the straw you find a bent nail. It might open the lock."}` {
		t.Errorf("after choose: %s", strings.Join(got, "\n"))
	}
}

func TestSessionsAreIndependent(t *testing.T) {
	w := demoWorld(t)
	a, _ := w.NewSession()
	defer a.Close()
	b, _ := w.NewSession()
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
			s, first := w.NewSession()
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
	s, _ := w.NewSession()
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
	s, first := w.NewSession()
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
	s, _ := w.NewSession()
	s.Close()
	if got := kinds(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "error:" {
		t.Errorf("choose after Close = %s", got)
	}
}

// A hook that fails after a dialog reports the failure and leaves no dialog
// open.
func TestScriptErrorAfterDialog(t *testing.T) {
	w := scriptWorld(t, `function on_enter() ui.dialog("?", {"a"}) say("x") error("late") end`, script.DefaultLimits, quiet)
	s, first := w.NewSession()
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
	s, _ := w.NewSession()
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

// Once the world's scripts keep too much, every later session fails too
// (until the world restarts).
func TestStateMemoryFailsEveryLaterSession(t *testing.T) {
	limits := script.Limits{Time: time.Second, CallMemory: 64 << 20, StateMemory: 8 << 20}
	w := scriptWorld(t, `keep = {} function on_enter() keep[#keep + 1] = string.rep("x", 1e6) .. #keep say("ok") end`, limits, quiet)
	failed := -1
	for i := range 50 {
		s, first := w.NewSession()
		s.Close()
		if kinds(first) == "error:" {
			if !strings.Contains(first[0].Message, "limits") {
				t.Fatalf("message = %q", first[0].Message)
			}
			failed = i
			break
		}
	}
	if failed < 0 {
		t.Fatal("50 sessions keeping 1 MiB each never hit an 8 MiB state limit")
	}
	for range 3 {
		s, first := w.NewSession()
		s.Close()
		if kinds(first) != "error:" {
			t.Errorf("session after the limit = %+v", first)
		}
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
			s, _ := w.NewSession()
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
	w, err := NewWorld(mustDemo(), script.DefaultLimits, quiet)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(w.Close)
	f.Fuzz(func(t *testing.T, typ, id string, option int, text string) {
		s, _ := w.NewSession()
		defer s.Close()
		got := s.Handle(ws.Message{Type: typ, ID: id, Option: option, Text: text, Options: []string{text}})
		if len(got) == 0 {
			t.Fatal("no answer")
		}
		valid := typ == "choose" && id == "d1" && option >= 1 && option <= 3
		if !valid {
			if kinds(got) != "error:" {
				t.Fatalf("invalid message answered with %+v", got)
			}
			if again := kinds(s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2})); again != "text:" {
				t.Fatalf("dialog closed by an invalid message: %s", again)
			}
		}
	})
}
