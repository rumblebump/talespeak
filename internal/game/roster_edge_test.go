package game

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of T-0031's party for T-0036: player.gm in on_leave, room
// timers and after a reconnect; world.players for a joiner, while several
// leave, while the world ends, after a replaced session, and under
// concurrent joins and leaves.

// rosterEdge runs in rooms a and b. on_enter reports player.gm and
// world.players; on_leave writes the leaver's gm and world.players to
// state.world under "left_<id>".
const rosterEdge = `
local function roster()
  local parts = {}
  for _, q in ipairs(world.players()) do
    parts[#parts + 1] = q.id .. "@" .. q.room .. (q.gm and "*" or "")
  end
  return table.concat(parts, ",")
end
function on_enter(p) say("enter " .. tostring(p.gm) .. " " .. roster()) end
function on_input(p, text)
  if text == "roster" then say(roster())
  elseif text == "rtimer" then timer.start("r", 5, {room = true})
  elseif string.sub(text, 1, 5) == "left " then
    say(tostring(state.world.get("left_" .. string.sub(text, 6))))
  end
end
function on_timer(p, name, kind) say("timer " .. name .. " " .. tostring(kind) .. " " .. tostring(p.gm)) end
function on_leave(p) state.world.set("left_" .. p.id, tostring(p.gm) .. " " .. roster()) end`

// on_enter of a joining player lists them, last.
func TestWorldPlayersListsJoinerInOnEnter(t *testing.T) {
	w, _ := clockWorld(t, rosterEdge, rosterEdge)
	s1, first := w.NewSession("", ws.Who{GM: true})
	defer s1.Close()
	if got := texts(first); got != "enter true p1@a*" {
		t.Errorf("first on_enter = %q", got)
	}
	s2, first := w.NewSession("", ws.Who{})
	defer s2.Close()
	if got := texts(first); got != "enter false p1@a*,p2@a" {
		t.Errorf("second on_enter = %q, want the joiner listed last", got)
	}
}

// on_leave sees the leaver's gm, and each leaver is gone from
// world.players for the next one.
func TestWorldPlayersInOnLeaveOneAfterAnother(t *testing.T) {
	w, _ := clockWorld(t, rosterEdge, rosterEdge)
	s1, _ := w.NewSession("", ws.Who{})
	s2, _ := w.NewSession("", ws.Who{GM: true})
	s3, _ := w.NewSession("", ws.Who{})
	s4, _ := w.NewSession("", ws.Who{})
	defer s4.Close()
	s2.Close()
	s1.Close()
	s3.Close()
	for _, tt := range []struct{ id, want string }{
		{"p2", "true p1@a,p3@a,p4@a"},
		{"p1", "false p3@a,p4@a"},
		{"p3", "false p4@a"},
	} {
		if got := texts(input(s4, "left "+tt.id)); got != tt.want {
			t.Errorf("on_leave of %s saw %q, want %q", tt.id, got, tt.want)
		}
	}
}

// A room timer's on_timer runs for whoever started it, with their gm.
func TestPlayerGMInRoomTimer(t *testing.T) {
	for _, gm := range []bool{true, false} {
		w, c := clockWorld(t, rosterEdge, rosterEdge)
		s, _ := w.NewSession("", ws.Who{GM: gm})
		other, _ := w.NewSession("", ws.Who{GM: !gm})
		input(s, "rtimer")
		c.Advance(5 * time.Second)
		want := "timer r room " + strconv.FormatBool(gm)
		if got := texts(pending(t, s)); got != want {
			t.Errorf("GM %v: on_timer = %q, want %q", gm, got, want)
		}
		pending(t, other)
		s.Close()
		other.Close()
	}
}

// End runs on_leave for everyone: each sees the players not gone yet,
// never itself, so the lists shrink one by one whatever the order.
func TestWorldPlayersWhileWorldEnds(t *testing.T) {
	w, _ := clockWorld(t, rosterEdge, rosterEdge)
	var ss []ws.Session
	for i := range 3 {
		s, _ := w.NewSession("", ws.Who{GM: i == 0})
		ss = append(ss, s)
	}
	w.End("over")
	w.mu.Lock()
	var sizes []int
	for i := 1; i <= 3; i++ {
		id := "p" + strconv.Itoa(i)
		v, ok := w.world.Encoded("left_" + id)
		got := string(v)
		if !ok {
			t.Errorf("on_leave of %s did not run", id)
			continue
		}
		if strings.Contains(got, id+"@") {
			t.Errorf("on_leave of %s lists itself: %q", id, got)
		}
		if wantGM := i == 1; !strings.Contains(got, strconv.FormatBool(wantGM)+" ") {
			t.Errorf("on_leave of %s: %q, want gm %v", id, got, wantGM)
		}
		sizes = append(sizes, strings.Count(got, "@"))
	}
	w.mu.Unlock()
	slices.Sort(sizes)
	if !slices.Equal(sizes, []int{0, 1, 2}) {
		t.Errorf("world.players sizes in on_leave = %v, want 0, 1 and 2", sizes)
	}
	for _, s := range ss {
		if ms := input(s, "roster"); !isError(ms) {
			t.Errorf("roster after End = %v", ms)
		}
		s.Close()
	}
}

// A closed (unloaded) world runs no on_leave and lists nobody: a player
// who tries to join a closed world without a store is not listed.
func TestWorldPlayersClosedWorld(t *testing.T) {
	w, _ := clockWorld(t, rosterEdge, rosterEdge)
	s, _ := w.NewSession("", ws.Who{})
	w.Close()
	late, _ := w.NewSession("", ws.Who{})
	w.mu.Lock()
	online := len(w.online())
	_, ran := w.world.Encoded("left_p1")
	w.mu.Unlock()
	if ran {
		t.Error("on_leave ran in a closed world")
	}
	if online != 1 {
		t.Errorf("online after close = %d, want the one who was there", online)
	}
	s.Close()
	if late != nil {
		late.Close()
	}
	w.mu.Lock()
	online = len(w.online())
	w.mu.Unlock()
	if online != 0 {
		t.Errorf("online after everyone closed = %d", online)
	}
}

// Connecting again replaces the older session: the newer Who's GM wins,
// the old one's on_leave sees the old role and neither session, and the
// player moves to the end of the join order.
func TestPlayerGMAfterReplacedSession(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	w := storeWorldWith(t, st, world, rosterEdge, rosterEdge)
	miraGM := who[0]
	miraGM.GM = true
	old, _ := w.NewSession("", miraGM)
	zed, _ := w.NewSession("", who[1])
	defer zed.Close()
	mid := strings.SplitN(texts(input(old, "roster")), "@", 2)[0]
	zid := strings.SplitN(texts(input(zed, "roster")), ",", 2)[1]
	zid = strings.SplitN(zid, "@", 2)[0]
	if got := texts(input(zed, "roster")); got != mid+"@a*,"+zid+"@a" {
		t.Fatalf("roster = %q", got)
	}

	cur, first := w.NewSession("", who[0]) // no longer a GM
	if got, want := texts(first), "enter false "+zid+"@a,"+mid+"@a"; !strings.HasSuffix(got, want) {
		t.Errorf("new session on_enter = %q, want suffix %q", got, want)
	}
	if got, want := texts(input(zed, "left "+mid)), "true "+zid+"@a"; got != want {
		t.Errorf("old session's on_leave = %q, want %q", got, want)
	}
	if got, want := texts(input(zed, "roster")), zid+"@a,"+mid+"@a"; got != want {
		t.Errorf("roster after the reconnect = %q, want %q", got, want)
	}
	old.Close()
	if got, want := texts(input(zed, "roster")), zid+"@a,"+mid+"@a"; got != want {
		t.Errorf("roster after the old connection ended = %q, want %q", got, want)
	}

	again, first := w.NewSession("", miraGM) // a GM again
	defer again.Close()
	if got, want := texts(first), "enter true "+zid+"@a,"+mid+"@a*"; !strings.HasSuffix(got, want) {
		t.Errorf("GM again: on_enter = %q, want suffix %q", got, want)
	}
	cur.Close()
	if got, want := texts(input(zed, "roster")), zid+"@a,"+mid+"@a*"; got != want {
		t.Errorf("roster = %q, want %q", got, want)
	}
}

// Concurrent joins, leaves and world.players calls: in a world without a
// store ids follow the join order, so every list has rising ids.
func TestWorldPlayersConcurrent(t *testing.T) {
	w, _ := clockWorld(t, rosterEdge, rosterEdge)
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := range 8 {
		wg.Go(func() {
			for i := range 20 {
				s, _ := w.NewSession("", ws.Who{GM: (g+i)%3 == 0})
				got := texts(input(s, "roster"))
				if msg := checkRising(got); msg != "" {
					select {
					case errs <- msg:
					default:
					}
				}
				if i%2 == 0 {
					s.Close()
				} else {
					defer s.Close()
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// checkRising reports what is wrong with a roster "p1@a,p3@a*,...": ids
// must rise.
func checkRising(roster string) string {
	last := 0
	for e := range strings.SplitSeq(roster, ",") {
		id, _, ok := strings.Cut(e, "@")
		n, err := strconv.Atoi(strings.TrimPrefix(id, "p"))
		if !ok || err != nil || n <= last {
			return fmt.Sprintf("roster %q is not in join order", roster)
		}
		last = n
	}
	return ""
}
