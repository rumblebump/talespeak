package game

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of timers that last (T-0012): saves that fail, a player
// replaced by a second connection, stored rows that cannot be restored,
// the cap on question deadlines, a room timer that waits (due) and is
// replaced, cancelled or reset, and /reset forgetting deadlines.

// lastEdgeA reads state.world "mode" in on_enter: "restart" starts the
// room timer turn again, "cancel" cancels it; otherwise it says what
// timer.left gives for it. A line starting with q asks a question titled
// with the line.
const lastEdgeA = `
function on_enter(p)
  local m = state.world.get("mode")
  if m == "restart" then timer.start("turn", 60, {room = true, show = "Again"}) say("in a restart")
  elseif m == "cancel" then say("in a cancel " .. tostring(timer.cancel("turn", {room = true})))
  else say("in a left " .. tostring(timer.left("turn", {room = true}))) end
end
function on_input(p, text)
  if text == "room" then timer.start("turn", 60, {room = true, show = "Turn"})
  elseif text == "start" then timer.start("guards", 60, {show = "Guards"})
  elseif text == "cancel" then timer.cancel("guards")
  elseif text == "left" then
    say(tostring(timer.left("guards")) .. " " .. tostring(timer.left("turn", {room = true})))
  elseif text == "go" then room.go("b")
  elseif string.sub(text, 1, 1) == "q" then
    local n = ui.dialog(text, {"A", "B"}, {timeout = 30})
    say(text .. " " .. tostring(n))
  else say("a hears") end
end
function on_timer(p, name, kind) say("up " .. name .. " " .. tostring(kind)) end`

const lastEdgeB = `
function on_enter(p) say("in b") end
function on_input(p, text)
  if text == "back" then room.go("a")
  elseif string.sub(text, 1, 5) == "mode " then state.world.set("mode", string.sub(text, 6)) say("mode set")
  else say("b hears") end
end
function on_timer(p, name, kind) say("b up " .. name .. " " .. tostring(kind)) end`

// lastEdgeWorld is a world of lastEdgeA and lastEdgeB with st on c.
func lastEdgeWorld(t *testing.T, st store.Worlds, world store.World, c *fakeClock) *World {
	t.Helper()
	p, err := pack.Load(roomsFS(lastEdgeA, lastEdgeB))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithStore(st, world.ID), WithClock(c.Now, c.After))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// charID is the id of who's character in world.
func charID(t *testing.T, st *sqlite.Store, world store.World, who ws.Who) int64 {
	t.Helper()
	c, err := st.Character(context.Background(), world.ID, who.Account, who.Name)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// storedTimers is what the database has of who's timers (by kind:name,
// questions by kind only) and of the world's room timers (by room:name).
func storedTimers(t *testing.T, st *sqlite.Store, world store.World, who ws.Who) (player []string, rooms []string) {
	t.Helper()
	ctx := context.Background()
	list, err := st.PlayerTimers(ctx, charID(t, st, world, who))
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.Kind == store.TimerQuestion {
			player = append(player, x.Kind)
		} else {
			player = append(player, x.Kind+":"+x.Name)
		}
	}
	rl, err := st.RoomTimers(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range rl {
		rooms = append(rooms, x.Room+":"+x.Name)
	}
	return player, rooms
}

// A save that fails keeps the timer changes it did not write: the next
// save, of the same player or another, writes them, also a cancel.
func TestLastingFailedSaveKeepsTimers(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	fs := &flakyStore{Store: st}
	c := newClock()
	w := lastEdgeWorld(t, fs, world, c)
	mira, _ := w.NewSession("", who[0])
	defer mira.Close()

	fs.fail(1)
	input(mira, "start")
	if p, r := storedTimers(t, st, world, who[0]); len(p) != 0 || len(r) != 0 {
		t.Fatalf("after a failed save: player %v, rooms %v; want nothing", p, r)
	}
	input(mira, "room")
	if p, r := storedTimers(t, st, world, who[0]); strings.Join(p, ",") != "timer:guards" || strings.Join(r, ",") != "a:turn" {
		t.Fatalf("after the next save: player %v, rooms %v; want both", p, r)
	}

	// A cancel whose save fails is written by another player's save.
	zed, _ := w.NewSession("", who[1])
	defer zed.Close()
	fs.fail(1)
	input(mira, "cancel")
	if p, _ := storedTimers(t, st, world, who[0]); len(p) != 1 {
		t.Fatalf("a cancel whose save failed was written: %v", p)
	}
	input(zed, "x")
	if p, _ := storedTimers(t, st, world, who[0]); len(p) != 0 {
		t.Errorf("another player's save did not write the cancel: %v", p)
	}
}

// A player who leaves while saves fail and comes back gets the timers
// they started: joining writes them first, and refuses the join while it
// cannot, rather than read timers older than what the world has.
func TestLastingFailedSaveWrittenOnReturn(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	fs := &flakyStore{Store: st}
	c := newClock()
	w := lastEdgeWorld(t, fs, world, c)
	mira, _ := w.NewSession("", who[0])
	fs.fail(3) // start, leave, and the first try to come back
	input(mira, "start")
	mira.Close()
	c.Advance(10 * time.Second)

	s, first := w.NewSession("", who[0])
	if s != nil || len(first) != 1 || first[0].Type != "error" {
		t.Fatalf("join while the timers cannot be written: %v %+v, want it refused", s, first)
	}
	mira, first = w.NewSession("", who[0])
	if mira == nil {
		t.Fatalf("join after the store is back: %+v", first)
	}
	defer mira.Close()
	if m, ok := timerMsg(first, "guards"); !ok || m.Ms != 50000 {
		t.Errorf("coming back: %+v, want the guards' countdown with 50 s left", first)
	}
	if p, _ := storedTimers(t, st, world, who[0]); strings.Join(p, ",") != "timer:guards" {
		t.Errorf("stored: %v", p)
	}
}

// A player who connects again from another device replaces their session
// and keeps their timers, the room's and the deadline of the question the
// old session had open.
func TestLastingReplacedKeepsTimers(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := newClock()
	w := lastEdgeWorld(t, st, world, c)
	old, _ := w.NewSession("", who[0])
	input(old, "start")
	input(old, "room")
	if got := input(old, "qfight"); len(got) != 1 || got[0].Ms != 30000 {
		t.Fatalf("ask: %+v", got)
	}
	c.Advance(10 * time.Second)

	mira, first := w.NewSession("", who[0])
	defer mira.Close()
	if q, ok := old.(ws.Pusher).Pending(); ok || len(q) != 1 || q[0].Message != replacedMessage {
		t.Errorf("the old session: %+v %v, want it replaced", q, ok)
	}
	if m, ok := timerMsg(first, "guards"); !ok || m.Ms != 50000 {
		t.Errorf("the new session: %+v, want the guards with 50 s left", first)
	}
	if m, ok := timerMsg(first, "room:turn"); !ok || m.Ms != 50000 {
		t.Errorf("the new session: %+v, want the room's turn with 50 s left", first)
	}
	if got := input(mira, "qfight"); len(got) != 1 || got[0].Ms != 20000 {
		t.Fatalf("the same question again: %+v, want 20 s left", got)
	}
	c.Advance(20 * time.Second)
	if got := texts(pending(t, mira)); got != "close | qfight nil" {
		t.Errorf("at the old deadline: %q", got)
	}
	c.Advance(30 * time.Second)
	if got := texts(pending(t, mira)); got != "timer | up guards nil | timer | up turn room" && got != "timer | up turn room | timer | up guards nil" {
		t.Errorf("at the timers' deadline: %q", got)
	}
	if n := c.running(); n != 0 {
		t.Errorf("%d clock timers running: the old session's came back too", n)
	}
}

// Stored rows that could never have been started are dropped as they
// load and deleted with the next save: a bad name, a bad label, an
// unknown room, a bad question name, and those past the caps. A deadline
// further away than a timer can run is brought forward to a day.
func TestLastingInvalidStoredTimersDropped(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	ctx := context.Background()
	c := newClock()
	now := c.Now()
	far := now.Add(30 * 24 * time.Hour)
	soon := now.Add(time.Minute)
	ch := charID(t, st, world, who[0])
	q := func(i int) string { return fmt.Sprintf("%032x", i) }
	list := []store.Timer{
		{Kind: store.TimerRoom, Room: "a", Name: "turn", Deadline: far, Show: "Turn"},
		{Kind: store.TimerRoom, Room: "a", Name: "Bad Name", Deadline: soon},
		{Kind: store.TimerRoom, Room: "a", Name: "label", Deadline: soon, Show: "two\nlines"},
		{Kind: store.TimerRoom, Room: "a", Name: "long", Deadline: soon, Show: strings.Repeat("x", script.MaxLabelBytes+1)},
		{Kind: store.TimerRoom, Room: "nowhere", Name: "ghost", Deadline: soon},
		{Kind: store.TimerPlayer, Character: ch, Name: "guards", Deadline: far, Show: "Guards"},
		{Kind: store.TimerPlayer, Character: ch, Name: "Bad Name", Deadline: soon},
		{Kind: store.TimerPlayer, Character: ch, Name: "label", Deadline: soon, Show: "\xff"},
		{Kind: store.TimerQuestion, Character: ch, Room: "a", Name: "not-a-hash", Deadline: soon},
		{Kind: store.TimerQuestion, Character: ch, Room: "a", Name: q(1)[:31], Deadline: soon},
		{Kind: store.TimerQuestion, Character: ch, Room: "nowhere", Name: q(99), Deadline: soon},
	}
	for i := range 10 { // with turn, one room timer too many
		list = append(list, store.Timer{Kind: store.TimerRoom, Room: "a", Name: fmt.Sprintf("x%02d", i), Deadline: soon})
	}
	for i := range 10 { // with guards, one player timer too many
		list = append(list, store.Timer{Kind: store.TimerPlayer, Character: ch, Name: fmt.Sprintf("p%02d", i), Deadline: soon})
	}
	for i := range 11 { // one question too many
		list = append(list, store.Timer{Kind: store.TimerQuestion, Character: ch, Room: "a", Name: q(i + 1), Deadline: soon})
	}
	if err := st.Save(ctx, store.Save{World: world.ID, Timers: list}); err != nil {
		t.Fatal(err)
	}

	w := lastEdgeWorld(t, st, world, c)
	if n := runningRoomTimers(w); n != MaxRoomTimers {
		t.Errorf("%d room timers restored, want %d", n, MaxRoomTimers)
	}
	mira, first := w.NewSession("", who[0])
	defer mira.Close()
	if m, ok := timerMsg(first, "guards"); !ok || m.Ms != script.MaxTimerSeconds*1000 {
		t.Errorf("a far deadline: %+v, want a day", first)
	}
	if m, ok := timerMsg(first, "room:turn"); !ok || m.Ms != script.MaxTimerSeconds*1000 {
		t.Errorf("a far room deadline: %+v, want a day", first)
	}
	if _, ok := timerMsg(first, "label"); ok {
		t.Errorf("a timer with a bad label was shown: %+v", first)
	}
	w.mu.Lock()
	timers, questions := len(mira.(*session).timers), len(mira.(*session).questions)
	w.mu.Unlock()
	if timers != script.MaxTimers || questions != maxQuestions {
		t.Errorf("restored %d timers and %d question deadlines, want %d and %d", timers, questions, script.MaxTimers, maxQuestions)
	}

	// The join saved: what was dropped is gone from the database.
	p, r := storedTimers(t, st, world, who[0])
	if len(r) != MaxRoomTimers || strings.Contains(strings.Join(r, ","), "x09") || strings.Contains(strings.Join(r, ","), "nowhere") {
		t.Errorf("stored room timers: %v", r)
	}
	nq, np := 0, 0
	for _, x := range p {
		if x == store.TimerQuestion {
			nq++
		} else {
			np++
		}
	}
	if nq != maxQuestions || np != script.MaxTimers || strings.Contains(strings.Join(p, ","), "p09") ||
		strings.Contains(strings.Join(p, ","), "Bad Name") || strings.Contains(strings.Join(p, ","), "label") {
		t.Errorf("stored player timers: %v", p)
	}
	stored, _ := st.PlayerTimers(ctx, ch)
	for _, x := range stored {
		if x.Kind == store.TimerQuestion && (x.Room != "a" || !validQuestion(x.Name) || x.Name == q(11)) {
			t.Errorf("a question deadline that should be gone: %+v", x)
		}
	}
}

// A player keeps at most maxQuestions question deadlines. While all of
// them still run, a new question's deadline is not kept, so cutting off
// other questions never frees one that runs; once one has passed, it
// makes room. Rows past the cap are not written either.
func TestLastingQuestionCap(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := newClock()
	w := lastEdgeWorld(t, st, world, c)
	ask := func(s ws.Session, q string, wantMs int64) []ws.Message {
		t.Helper()
		got := input(s, q)
		if len(got) != 1 || got[0].Type != "dialog" || got[0].Ms != wantMs {
			t.Fatalf("%s at %s: %+v, want %d ms", q, c.Now().Format("15:04:05"), got, wantMs)
		}
		return got
	}
	cut := func(q string, wantMs int64) { // ask q in a new session and cut it off
		t.Helper()
		mira, _ := w.NewSession("", who[0])
		ask(mira, q, wantMs)
		mira.Close()
	}
	for i := range maxQuestions + 1 { // q0 to q9 at 0 to 9 s, then q10 at 10 s
		cut(fmt.Sprintf("q%d", i), 30000)
		c.Advance(time.Second)
	}
	if p, _ := storedTimers(t, st, world, who[0]); len(p) != maxQuestions {
		t.Fatalf("stored %d question deadlines, want %d: %v", len(p), maxQuestions, p)
	}
	cut("q10", 30000) // at 11 s: still not kept, the others run
	c.Advance(time.Second)

	cut("q1", 19000) // kept: asked at 1 s, now 12 s; cut off again

	// Every deadline passes; q10 is kept now, in the place of q0, whose
	// deadline passed first. q0 asked again is new, not timed out.
	c.Advance(time.Minute)
	cut("q10", 30000)
	c.Advance(10 * time.Second)
	cut("q10", 20000)
	cut("q0", 30000)
	if p, _ := storedTimers(t, st, world, who[0]); len(p) > maxQuestions {
		t.Errorf("stored %d question deadlines: %v", len(p), p)
	}
}

// dueTurn starts room a's timer turn, moves the player out and lets it
// run out in the empty room, so it waits (due).
func dueTurn(t *testing.T, w *World, c *fakeClock, s ws.Session) {
	t.Helper()
	input(s, "room")
	if got := texts(input(s, "go")); !strings.Contains(got, "in b") {
		t.Fatalf("go: %q", got)
	}
	c.Advance(time.Minute)
	if got := texts(pending(t, s)); got != "" {
		t.Fatalf("a timer that ran out in an empty room was run elsewhere: %q", got)
	}
	w.mu.Lock()
	tm := w.roomTimers["a"]["turn"]
	w.mu.Unlock()
	if tm == nil || !tm.due {
		t.Fatalf("turn is %+v, want it due", tm)
	}
}

// A room timer that waits for a player runs for the first one in, after
// their on_enter, where timer.left gives 0; its countdown is gone from
// the database too.
func TestLastingDueRoomTimerFires(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := newClock()
	w := lastEdgeWorld(t, st, world, c)
	mira, _ := w.NewSession("", who[0])
	defer mira.Close()
	dueTurn(t, w, c, mira)
	input(mira, "x") // a save
	rl, err := st.RoomTimers(context.Background(), world.ID)
	if err != nil || len(rl) != 1 || rl[0].Show != "" {
		t.Errorf("stored room timers of a due timer: %+v, %v; want it secret", rl, err)
	}
	got := input(mira, "back")
	if _, ok := timerMsg(got, "room:turn"); ok || !strings.Contains(texts(got), "in a left 0") {
		t.Errorf("coming in: %q", texts(got))
	}
	c.Advance(0)
	if got := texts(pending(t, mira)); got != "up turn room" {
		t.Errorf("after on_enter: %q", got)
	}
	if _, r := storedTimers(t, st, world, who[0]); len(r) != 0 {
		t.Errorf("a room timer that fired is still stored: %v", r)
	}
}

// A room timer that waits is replaced when the first player in starts it
// again in on_enter, and gone when they cancel it: it does not run.
func TestLastingDueRoomTimerReplacedOrCancelled(t *testing.T) {
	for _, mode := range []string{"restart", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
			c := newClock()
			w := lastEdgeWorld(t, st, world, c)
			mira, _ := w.NewSession("", who[0])
			defer mira.Close()
			dueTurn(t, w, c, mira)
			input(mira, "mode "+mode)
			got := input(mira, "back")
			c.Advance(0)
			if got := texts(pending(t, mira)); got != "" {
				t.Errorf("the old timer ran: %q", got)
			}
			_, r := storedTimers(t, st, world, who[0])
			switch mode {
			case "restart":
				if m, ok := timerMsg(got, "room:turn"); !ok || m.Title != "Again" || m.Ms != 60000 {
					t.Errorf("coming in: %+v, want the new countdown", got)
				}
				if len(r) != 1 {
					t.Errorf("stored: %v", r)
				}
				c.Advance(time.Minute)
				if got := texts(pending(t, mira)); got != "timer | up turn room" {
					t.Errorf("at the new deadline: %q", got)
				}
			case "cancel":
				if !strings.Contains(texts(got), "in a cancel true") {
					t.Errorf("coming in: %q", texts(got))
				}
				if len(r) != 0 || runningRoomTimers(w) != 0 || c.running() != 0 {
					t.Errorf("a cancelled due timer stays: stored %v", r)
				}
			}
		})
	}
}

// /reset room forgets the room's timers, also one that waits in the
// empty room, here and in the database.
func TestLastingResetForgetsDueTimer(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	setRole(t, st, world, who[1], store.MemberGM)
	c := newClock()
	w := lastEdgeWorld(t, st, world, c)
	mira, _ := w.NewSession("", who[0])
	defer mira.Close()
	dueTurn(t, w, c, mira)
	gm, _ := w.NewSession("", who[1]) // joins in a: the due timer runs for him
	c.Advance(0)
	if got := texts(pending(t, gm)); got != "up turn room" {
		t.Fatalf("the game master coming in: %q", got)
	}
	input(gm, "room")
	input(gm, "go")
	c.Advance(time.Minute)
	if got := texts(input(gm, "/reset room a")); !strings.Contains(got, "notice") {
		t.Fatalf("reset: %q", got)
	}
	if _, r := storedTimers(t, st, world, who[0]); len(r) != 0 {
		t.Errorf("stored after /reset: %v", r)
	}
	got := input(mira, "back")
	c.Advance(0)
	if !strings.Contains(texts(got), "in a left nil") || texts(pending(t, mira)) != "" {
		t.Errorf("coming in after /reset: %q", texts(got))
	}
}

// /reset room closes the question a player had open there and forgets
// its deadline, and also the deadline of the same question cut off
// before: the room starts over, so asking it again gives the whole time.
func TestLastingResetForgetsQuestionDeadline(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	setRole(t, st, world, who[1], store.MemberGM)
	c := newClock()
	w := lastEdgeWorld(t, st, world, c)
	gm, _ := w.NewSession("", who[1])
	defer gm.Close()

	t.Run("open", func(t *testing.T) {
		mira, _ := w.NewSession("", who[0])
		defer mira.Close()
		input(mira, "qopen")
		c.Advance(10 * time.Second)
		input(gm, "/reset room a")
		pending(t, mira)
		if got := input(mira, "qopen"); len(got) != 1 || got[0].Ms != 30000 {
			t.Errorf("asked again after /reset: %+v, want a new 30 s", got)
		}
	})
	t.Run("cut off", func(t *testing.T) {
		mira, _ := w.NewSession("", who[0])
		input(mira, "qcut")
		c.Advance(10 * time.Second)
		mira.Close()
		mira, _ = w.NewSession("", who[0]) // the deadline waits for qcut
		defer mira.Close()
		input(gm, "/reset room a")
		pending(t, mira)
		if got := input(mira, "qcut"); len(got) != 1 || got[0].Ms != 30000 {
			t.Errorf("asked again after /reset: %+v, want a new 30 s", got)
		}
	})
}

// Without a store nothing is noted for a save, and a player's timers stop
// when they leave.
func TestLastingNoStoreNotesNothing(t *testing.T) {
	w, c := clockWorld(t, lastEdgeA, lastEdgeB)
	mira, _ := w.NewSession("", ws.Who{Name: "Mira"})
	input(mira, "start")
	input(mira, "room")
	if got := input(mira, "qx"); len(got) != 1 || got[0].Ms != 30000 {
		t.Fatalf("ask: %+v", got)
	}
	w.mu.Lock()
	n, nq := len(w.timerSaves), len(mira.(*session).questions)
	w.mu.Unlock()
	if n != 0 || nq != 0 {
		t.Errorf("without a store: %d timer changes noted, %d question deadlines kept", n, nq)
	}
	mira.Close()
	c.Advance(time.Minute)
	mira, first := w.NewSession("", ws.Who{Name: "Mira"})
	defer mira.Close()
	if _, ok := timerMsg(first, "guards"); ok {
		t.Errorf("a player's timer came back without a store: %+v", first)
	}
}

// FuzzRestoreTimers restores arbitrary stored rows into a player and a
// world: nothing panics, every timer kept is valid and within the caps
// and a day, and they all run out cleanly.
func FuzzRestoreTimers(f *testing.F) {
	f.Add("timer", "", "guards", int64(60000), "Guards", false)
	f.Add("question", "a", "0123456789abcdef0123456789abcdef", int64(-5000), "", false)
	f.Add("room", "a", "turn", int64(1<<62), "Turn", true)
	f.Add("room", "nowhere", "Bad Name", int64(0), "\xff\n", false)
	f.Add("question", "b", "0123456789ABCDEF0123456789ABCDEF", int64(10), "", true)
	f.Fuzz(func(t *testing.T, kind, room, name string, ms int64, show string, readOnly bool) {
		w, c := clockWorld(t, lastEdgeA, lastEdgeB)
		s, _ := w.NewSession("", ws.Who{Name: "Mira"})
		defer s.Close()
		now := c.Now()
		var list []store.Timer
		for i := range script.MaxTimers + 2 {
			n := name
			if i > 0 {
				n = fmt.Sprintf("%s%d", name, i)
			}
			list = append(list, store.Timer{Kind: kind, Room: room, Name: n, Deadline: now.Add(time.Duration(ms) * time.Millisecond), Show: show, ReadOnly: readOnly})
		}
		rooms := make([]store.Timer, len(list))
		for i, x := range list {
			x.Kind = store.TimerRoom // as RoomTimers reads them
			rooms[i] = x
		}
		w.mu.Lock()
		w.restoreRoomTimers(rooms)
		ss := s.(*session)
		ss.restoreTimers(list)
		limit := now.Add(script.MaxTimerSeconds * time.Second)
		if len(ss.timers) > script.MaxTimers || len(ss.questions) > maxQuestions {
			t.Errorf("%d timers, %d questions", len(ss.timers), len(ss.questions))
		}
		for n, tm := range ss.timers {
			if !script.ValidItemID(n) || (tm.show != "" && !script.ValidLabel(tm.show)) || tm.deadline.After(limit) {
				t.Errorf("kept timer %q %+v", n, tm)
			}
		}
		for k, at := range ss.questions {
			if !validQuestion(k.name) || w.pack.Rooms[k.room] == nil || at.After(limit) {
				t.Errorf("kept question %+v", k)
			}
		}
		for r, ts := range w.roomTimers {
			if w.pack.Rooms[r] == nil || len(ts) > MaxRoomTimers {
				t.Errorf("room %q: %d timers", r, len(ts))
			}
			for n, tm := range ts {
				if !script.ValidItemID(n) || tm.deadline.After(limit) {
					t.Errorf("kept room timer %q %+v", n, tm)
				}
			}
		}
		w.mu.Unlock()
		c.Advance(script.MaxTimerSeconds * time.Second)
		s.(ws.Pusher).Pending()
	})
}

// A world that fails to load after its room timers were restored (a room
// script that fails as it starts) leaves no clock timer running: nothing
// would ever stop it.
func TestLastingFailedWorldStopsRestoredTimers(t *testing.T) {
	st, world, _ := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := newClock()
	if err := st.Save(context.Background(), store.Save{World: world.ID, Timers: []store.Timer{
		{Kind: store.TimerRoom, Room: "a", Name: "turn", Deadline: c.Now().Add(time.Hour)},
	}}); err != nil {
		t.Fatal(err)
	}
	p, err := pack.Load(roomsFS(lastEdgeA, `error("broken")`))
	if err != nil {
		t.Fatal(err)
	}
	if w, err := NewWorld(p, script.DefaultLimits, quiet, WithStore(st, world.ID), WithClock(c.Now, c.After)); err == nil {
		w.Close()
		t.Fatal("a world with a broken room script loaded")
	}
	if n := c.running(); n != 0 {
		t.Errorf("%d clock timers still running for a world that never loaded", n)
	}
}

// A question deadline that passed less than a day ago times the question
// out at once when it is asked again; one older than that is forgotten as
// the player joins, also in the database, and the question starts anew.
func TestLastingOldQuestionDeadline(t *testing.T) {
	for _, tc := range []struct {
		away   time.Duration
		wantMs int64
	}{{time.Hour, 1}, {25 * time.Hour, 30000}} {
		t.Run(tc.away.String(), func(t *testing.T) {
			st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
			c := newClock()
			w := lastEdgeWorld(t, st, world, c)
			mira, _ := w.NewSession("", who[0])
			input(mira, "qold")
			mira.Close()
			c.Advance(tc.away)
			mira, _ = w.NewSession("", who[0])
			defer mira.Close()
			if tc.wantMs != 1 {
				if p, _ := storedTimers(t, st, world, who[0]); len(p) != 0 {
					t.Errorf("an old deadline is still stored: %v", p)
				}
			}
			if got := input(mira, "qold"); len(got) != 1 || got[0].Ms != tc.wantMs {
				t.Errorf("asked again after %v: %+v, want %d ms", tc.away, got, tc.wantMs)
			}
		})
	}
}

// While saves fail, the timer changes waiting for one are bounded: past
// maxTimerSaves the oldest change goes.
func TestLastingTimerSavesBounded(t *testing.T) {
	st, world, _ := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := newClock()
	w := lastEdgeWorld(t, st, world, c)
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range maxTimerSaves + 1 {
		w.noteTimer(store.Timer{Kind: store.TimerPlayer, Character: int64(i + 1), Name: "t", Deadline: c.Now()})
	}
	// The latest change of a timer noted again replaces it, at no cost.
	w.noteTimer(store.Timer{Kind: store.TimerPlayer, Character: int64(maxTimerSaves + 1), Name: "t", Delete: true})
	if n := len(w.timerSaves); n != maxTimerSaves {
		t.Fatalf("%d timer changes kept, want %d", n, maxTimerSaves)
	}
	if _, ok := w.timerSaves[timerKey{store.TimerPlayer, 1, "", "t"}]; ok {
		t.Error("the oldest change was kept")
	}
	if v := w.timerSaves[timerKey{store.TimerPlayer, int64(maxTimerSaves + 1), "", "t"}]; !v.t.Delete {
		t.Errorf("the latest change of a timer: %+v", v)
	}
}
