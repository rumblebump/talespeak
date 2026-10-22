package game

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of state.player in sessions (T-0023): accounting across
// players and addresses, state surviving moves, evicted and reset
// instances, the address and world limits, closing, and concurrent
// players.

// stateScript keeps what on_input is told: "set NAME" keeps a 1000-byte
// string under NAME, "del NAME" removes it, "get NAME" says it, "go ROOM"
// moves, anything else is kept under "me".
const stateScript = `
function on_enter(p) say("in " .. p.room) end
function on_input(p, text)
  local cmd, arg = string.sub(text, 1, 4), string.sub(text, 5)
  if cmd == "set " then say(tostring(state.player.set(arg, string.rep("x", 1000))))
  elseif cmd == "del " then say(tostring(state.player.set(arg, nil)))
  elseif cmd == "get " then say(tostring(state.player.get(arg)))
  elseif cmd == "big " then
    local ok, why = state.player.set(arg, string.rep("y", 16000))
    say(tostring(ok) .. " " .. tostring(why))
  elseif cmd == "go  " then room.go(arg)
  else say(tostring(state.player.set("me", text))) end
end`

// totals returns the world's state.player total and per-client map, and
// the Size of each session's state, under the world's lock.
func totals(w *World, ss ...ws.Session) (total int64, clients map[string]int64, sizes []int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	clients = map[string]int64{}
	for k, v := range w.clientState {
		clients[k] = v
	}
	for _, s := range ss {
		sizes = append(sizes, s.(*session).state.Size())
	}
	return w.stateTotal, clients, sizes
}

func say(t *testing.T, s ws.Session, text string) string {
	t.Helper()
	return texts(s.Handle(ws.Message{Type: "input", Text: text}))
}

// TestStatePlayerAccounting: the world total is the sum of every player's
// Size, each address's share the sum of its players', after writes,
// overwrites, removals and Close, which is idempotent.
func TestStatePlayerAccounting(t *testing.T) {
	w := scriptWorld(t, stateScript, script.DefaultLimits, quiet, WithClientBudget(time.Second, time.Hour))
	a1, _ := w.NewSession("192.0.2.1/32", ws.Who{})
	a2, _ := w.NewSession("192.0.2.1/32", ws.Who{})
	b, _ := w.NewSession("198.51.100.1/32", ws.Who{})
	local, _ := w.NewSession("", ws.Who{})
	all := []ws.Session{a1, a2, b, local}
	defer func() {
		for _, s := range all {
			s.Close()
		}
	}()
	check := func(step string) {
		t.Helper()
		total, clients, sz := totals(w, all...)
		if total != sz[0]+sz[1]+sz[2]+sz[3] || clients["192.0.2.1/32"] != sz[0]+sz[1] || clients["198.51.100.1/32"] != sz[2] {
			t.Errorf("%s: total = %d, clients = %v, sizes = %v", step, total, clients, sz)
		}
		if _, ok := clients[""]; ok {
			t.Errorf("%s: a share for the empty address: %v", step, clients)
		}
		for k, v := range clients {
			if v <= 0 {
				t.Errorf("%s: share %s = %d, want removed at 0", step, k, v)
			}
		}
	}
	for i, s := range all {
		for _, cmd := range []string{"set a", "set b", "short", "longer value " + strings.Repeat("z", i*100)} {
			if got := say(t, s, cmd); got != "true" {
				t.Fatalf("player %d %s = %q", i, cmd, got)
			}
		}
	}
	check("written")
	say(t, a1, "del a")
	say(t, b, "x") // overwrites "me" with a shorter value
	say(t, local, "del missing")
	check("changed")
	a1.Close()
	a1.Close()
	check("one left")
	if _, clients, _ := totals(w); clients["192.0.2.1/32"] == 0 {
		t.Errorf("the other player of the address lost its share: %v", clients)
	}
	for _, s := range all {
		s.Close()
	}
	if total, clients, _ := totals(w); total != 0 || len(clients) != 0 {
		t.Errorf("after everyone left: total = %d, clients = %v", total, clients)
	}
}

// stateChain is a pack with scripted rooms r1 to rn, which count visits in
// state.player and go to any room typed.
func stateChain(t *testing.T, n int, limits script.Limits) *World {
	t.Helper()
	fsys := fstest.MapFS{
		"pack.yaml": {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: r1\n")},
	}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("r%d", i)
		fsys["rooms/"+id+".yaml"] = &fstest.MapFile{Data: []byte("id: " + id + "\ntitle: R\ndescription: A room.\nscript: " + id + ".lua\n")}
		fsys["scripts/"+id+".lua"] = &fstest.MapFile{Data: []byte(`
mine = 0
function on_enter(p)
  mine = mine + 1
  local seen = state.player.get("seen") or {}
  seen[#seen + 1] = p.room
  assert(state.player.set("seen", seen))
  say(p.room .. " " .. mine .. " " .. #seen)
end
function on_input(p, text)
  if text == "grow" then keep = {} for i = 1, 3 do keep[i] = string.rep("z", 1e6) .. i end return end
  room.go(text)
end`)}
	}
	p, err := pack.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, limits, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// TestStatePlayerSurvivesEviction: an evicted room starts over, its globals
// gone, but state.player still has every visit.
func TestStatePlayerSurvivesEviction(t *testing.T) {
	w := stateChain(t, 6, script.DefaultLimits)
	s, first := w.NewSession("192.0.2.1/32", ws.Who{})
	defer s.Close()
	if texts(first) != "r1 1 1" {
		t.Fatalf("first = %q", texts(first))
	}
	visits := 1
	for _, st := range []struct{ to, want string }{
		{"r2", "r2 1"}, {"r3", "r3 1"}, {"r4", "r4 1"}, {"r5", "r5 1"}, // r1 evicted
		{"r6", "r6 1"}, // r2 evicted
		{"r1", "r1 1"}, // r1 started over
		{"r6", "r6 2"}, // kept
	} {
		visits++
		if got, want := goTo(t, s, st.to), fmt.Sprintf("%s %d", st.want, visits); got != want {
			t.Errorf("go %s = %q, want %q", st.to, got, want)
		}
	}
	if n := len(s.(*session).envs); n > MaxRoomInstances {
		t.Errorf("%d instances kept", n)
	}
	total, clients, sz := totals(w, s)
	if total != sz[0] || clients["192.0.2.1/32"] != sz[0] {
		t.Errorf("total = %d, clients = %v, Size = %d", total, clients, sz[0])
	}
}

// TestStatePlayerSurvivesInstanceReset: a room whose instance keeps too
// much is reset; state.player is not.
func TestStatePlayerSurvivesInstanceReset(t *testing.T) {
	limits := script.Limits{Time: time.Second, CallMemory: 64 << 20, PlayerMemory: 2 << 20, StateMemory: 64 << 20}
	w := stateChain(t, 2, limits)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	got := s.Handle(ws.Message{Type: "input", Text: "grow"})
	if len(got) == 0 || got[len(got)-1].Type != "error" || !strings.Contains(got[len(got)-1].Message, "limits") {
		t.Fatalf("grow = %q", texts(got))
	}
	if s.(*session).envs["r1"] != nil {
		t.Fatalf("r1 not reset")
	}
	if got := goTo(t, s, "r2"); got != "r2 1 2" {
		t.Errorf("r2 after r1's reset = %q, want the state of both visits", got)
	}
	if got := goTo(t, s, "r1"); got != "r1 1 3" {
		t.Errorf("r1 again = %q", got)
	}
}

// TestStatePlayerAddressShare: what an address's players keep in
// state.player counts toward its share, so another player of the address
// cannot start a room script; removing the state makes room again. Other
// addresses are not affected.
func TestStatePlayerAddressShare(t *testing.T) {
	// A small PlayerMemory measures instances often, so what clientKept
	// counts for allocation garbage stays well below the state removed.
	limits := script.DefaultLimits
	limits.PlayerMemory = 256 << 10
	w := scriptWorld(t, stateScript, limits, quiet, WithClientBudget(time.Second, time.Hour))
	const addr = "192.0.2.1/32"
	a, _ := w.NewSession(addr, ws.Who{})
	defer a.Close()
	for i := range 10 {
		if got := say(t, a, fmt.Sprintf("big k%d", i)); got != "true nil" {
			t.Fatalf("big %d = %q", i, got)
		}
	}
	w.mu.Lock()
	state := w.clientState[addr]
	w.clientMemory = w.clientKept(addr) // full, with the state
	w.mu.Unlock()
	if state < 10*16000 {
		t.Fatalf("share = %d", state)
	}
	b, first := w.NewSession(addr, ws.Who{})
	defer b.Close()
	if kinds(first) != "error:" || !strings.Contains(first[0].Message, "your network") {
		t.Errorf("second player of the full address = %+v", first)
	}
	if got := say(t, a, "big more"); !strings.Contains(got, "too many players from this network") {
		t.Errorf("a write past the share = %q", got)
	}
	o, first := w.NewSession("198.51.100.1/32", ws.Who{})
	defer o.Close()
	if kinds(first) != "text:" {
		t.Errorf("another address = %+v", first)
	}
	for i := range 10 {
		say(t, a, fmt.Sprintf("del k%d", i))
	}
	c, first := w.NewSession(addr, ws.Who{})
	defer c.Close()
	if kinds(first) != "text:" {
		t.Errorf("after the state was removed = %+v", first)
	}
}

// TestStatePlayerCountsTowardWorldMemory: state.player and the room script
// instances share the world's StateMemory. Together they keep at most the
// limit plus the one Instance that may start just below it (the Runtime
// refuses new Instances only once the world is past the limit), not twice
// the limit as two separate pools would.
func TestStatePlayerCountsTowardWorldMemory(t *testing.T) {
	limits := script.DefaultLimits
	limits.Time = 10 * time.Second
	limits.StateMemory = 1 << 20
	w := scriptWorld(t, `pad = string.rep("p", 300000)
function on_enter() say("in") end
function on_input()
  for i = 1, 16 do
    local ok, why = state.player.set("k" .. i, string.rep("x", 15000))
    if not ok then say(why) return end
  end
  say("kept")
end`, limits, quiet, WithClientBudget(time.Second, time.Hour))
	var open []ws.Session
	defer func() {
		for _, s := range open {
			s.Close()
		}
	}()
	refused := false
	for i := range 6 {
		s, first := w.NewSession("", ws.Who{})
		open = append(open, s)
		if kinds(first) != "text:" {
			refused = true
			if !strings.Contains(first[0].Message, "full") {
				t.Errorf("player %d = %+v", i, first)
			}
			continue
		}
		if got := say(t, s, "fill"); got != "kept" {
			refused = true
			if !strings.Contains(got, "full") {
				t.Errorf("player %d fill = %q", i, got)
			}
		}
	}
	w.mu.Lock()
	kept := w.stateTotal
	for _, s := range open {
		for _, env := range s.(*session).envs {
			kept += w.rt.Kept(env)
		}
	}
	w.mu.Unlock()
	const pad = 300000
	if !refused || kept > limits.StateMemory+pad {
		t.Errorf("refused = %v, the world keeps %d bytes with StateMemory %d", refused, kept, limits.StateMemory)
	}
	for _, s := range open {
		s.Close()
	}
	open = nil
	s, first := w.NewSession("", ws.Who{})
	open = append(open, s)
	if kinds(first) != "text:" || say(t, s, "fill") != "kept" {
		t.Errorf("after everyone left: %+v", first)
	}
}

// TestStatePlayerClose: Close frees the state of a player waiting in a
// dialog, and of a player of a world already closed.
func TestStatePlayerClose(t *testing.T) {
	w := scriptWorld(t, `function on_enter()
  assert(state.player.set("k", string.rep("x", 1000)))
  ui.dialog("?", {"x"})
  pcall(state.player.set, "after", 1)
end
function on_input(p, text) say(tostring(state.player.set(text, 1))) end`, script.DefaultLimits, quiet)
	s, first := w.NewSession("192.0.2.1/32", ws.Who{})
	if kinds(first) != "dialog:d1" {
		t.Fatalf("first = %+v", first)
	}
	if total, _, _ := totals(w); total == 0 {
		t.Fatal("nothing kept")
	}
	s.Close()
	if total, clients, _ := totals(w); total != 0 || len(clients) != 0 {
		t.Errorf("after Close in a dialog: total = %d, clients = %v", total, clients)
	}

	s, _ = w.NewSession("192.0.2.1/32", ws.Who{})
	s.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})
	w.Close()
	if got := s.Handle(ws.Message{Type: "input", Text: "x"}); kinds(got) != "error:" {
		t.Errorf("input after the world closed = %+v", got)
	}
	s.Close()
	if total, clients, _ := totals(w); total != 0 || len(clients) != 0 {
		t.Errorf("after the world closed: total = %d, clients = %v", total, clients)
	}
}

// TestStatePlayerConcurrent: many players on few addresses write, read and
// leave at once. Each sees only its own values, and once all left nothing
// is counted.
func TestStatePlayerConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	w := scriptWorld(t, stateScript, script.DefaultLimits, quiet, WithClientBudget(time.Second, time.Hour))
	addrs := []string{"192.0.2.1/32", "192.0.2.2/32", ""}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	stop := make(chan struct{})
	watch := make(chan struct{})
	go func() { // the totals stay consistent while players come and go
		defer close(watch)
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.Gosched()
			total, clients, _ := totals(w)
			var sum int64
			for _, v := range clients {
				if v <= 0 {
					errs <- fmt.Errorf("share %d", v)
					return
				}
				sum += v
			}
			if total < sum {
				errs <- fmt.Errorf("total %d below the shares %d", total, sum)
				return
			}
		}
	}()
	for g := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := range 3 {
				s, _ := w.NewSession(addrs[g%len(addrs)], ws.Who{})
				if got := say(t, s, "get me"); got != "nil" {
					errs <- fmt.Errorf("player %d round %d starts with %q", g, round, got)
				}
				for i := range 10 {
					me := fmt.Sprintf("p%d-%d-%d", g, round, i)
					if got := say(t, s, me); got != "true" {
						errs <- fmt.Errorf("%s: set = %q", me, got)
						break
					}
					say(t, s, fmt.Sprintf("set k%d", i%3))
					if got := say(t, s, "get me"); got != me {
						errs <- fmt.Errorf("%s: get = %q", me, got)
						break
					}
					if i%4 == 0 {
						say(t, s, "del me")
					}
				}
				s.Close()
			}
		}()
	}
	wg.Wait()
	close(stop)
	<-watch
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if total, clients, _ := totals(w); total != 0 || len(clients) != 0 {
		t.Errorf("after everyone left: total = %d, clients = %v", total, clients)
	}
}
