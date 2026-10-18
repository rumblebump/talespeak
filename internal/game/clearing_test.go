package game

import (
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The demo's forest clearing, where players meet: the turn library
// (lib/round.lua) and the item library (lib/items.lua) at work.

// clearingWorld is the shipped demo on a fake clock, with the players
// starting in the clearing.
func clearingWorld(t *testing.T) (*World, *fakeClock) {
	t.Helper()
	p, err := pack.Load(content.Demo())
	if err != nil {
		t.Fatal(err)
	}
	p.Start = "clearing"
	c := newClock()
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithClock(c.Now, c.After))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w, c
}

func click(s ws.Session, item string) []ws.Message {
	return s.Handle(ws.Message{Type: "click", Item: item})
}

// heard is what pushed messages s got say, once on_change was delivered.
func heard(t *testing.T, c *fakeClock, s ws.Session) (string, []ws.Message) {
	t.Helper()
	c.Advance(DeliverGap)
	got := pending(t, s)
	return texts(got), got
}

func lastOf(ms []ws.Message, typ string) *ws.Message {
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Type == typ {
			return &ms[i]
		}
	}
	return nil
}

func hasItem(ms []ws.Message, id string) bool {
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Type == "items" {
			for _, it := range ms[i].Items {
				if it.ID == id {
					return true
				}
			}
			return false
		}
	}
	return false
}

func TestClearingDiceInTurns(t *testing.T) {
	w, c := clearingWorld(t)
	s1, first := w.NewSession("", ws.Who{})
	defer s1.Close()
	wantText(t, "alone", texts(first), "Nobody else is here yet.")
	s2, first := w.NewSession("", ws.Who{})
	defer s2.Close()
	wantText(t, "second", texts(first), "By the fire sit traveler.")
	text, _ := heard(t, c, s1)
	wantText(t, "news", text, "traveler 2 comes out of the forest.")

	got := click(s1, "dice")
	text = texts(got)
	for _, want := range []string{"You pick up the dice", "Your turn!", "Round 1. Order: traveler (you) (acting), traveler 2."} {
		wantText(t, "start", text, want)
	}
	if m := find(got, "timer"); m == nil || m.ID != "room:round" || m.Title != "traveler's turn" || m.Ms != 30000 {
		t.Fatalf("turn timer = %+v", m)
	}
	if !hasItem(got, "done") {
		t.Fatalf("the acting player has no done: %s", text)
	}
	text, got = heard(t, c, s2)
	wantText(t, "p2 hears", text, "traveler picks up the dice")
	wantText(t, "p2 status", text, "Order: traveler (acting), traveler 2 (you).")
	if hasItem(got, "done") {
		t.Errorf("the waiting player can end a turn: %s", text)
	}
	for _, try := range []func() []ws.Message{
		func() []ws.Message { return click(s2, "dice") },
		func() []ws.Message { return input(s2, "done") },
	} {
		if text := texts(try()); !strings.HasPrefix(text, "Wait for your turn: traveler is acting.") {
			t.Errorf("p2 out of turn: %q", text)
		}
	}

	wantText(t, "roll", texts(click(s1, "dice")), "You roll ")
	wantText(t, "roll again", texts(click(s1, "dice")), "You rolled already.")
	got = click(s1, "done")
	wantText(t, "done", texts(got), "You end your turn.")
	if hasItem(got, "done") {
		t.Errorf("done stays after the turn")
	}
	text, got = heard(t, c, s2)
	wantText(t, "p2 hears the roll", text, "traveler rolls ")
	wantText(t, "p2's turn", text, "Your turn!")
	if !hasItem(got, "done") {
		t.Errorf("p2 has no done on their turn: %s", text)
	}
	wantText(t, "p2 rolls", texts(click(s2, "dice")), "You roll ")
	text = texts(input(s2, "I am done"))
	if !strings.Contains(text, "wins the dice with") && !strings.Contains(text, " tie with ") {
		t.Fatalf("round end = %q", text)
	}
	text, _ = heard(t, c, s1)
	if !strings.Contains(text, "wins the dice with") && !strings.Contains(text, " tie with ") {
		t.Fatalf("p1 hears the end = %q", text)
	}
	// The game is over: anyone may start the next.
	wantText(t, "again", texts(click(s2, "dice")), "You pick up the dice")
}

// When the turn's time runs out, the turn is skipped; a round where
// everyone was skipped ends with nobody rolling.
func TestClearingTurnDeadline(t *testing.T) {
	w, c := clearingWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	click(s1, "dice")
	heard(t, c, s2)
	pending(t, s1)
	c.Advance(30 * time.Second)
	text, got := heard(t, c, s2)
	wantText(t, "p2's turn", text, "Your turn!")
	if m := lastOf(got, "timer"); got[0].Type != "timer" || got[0].Ms != 0 || m == nil || m.Title != "traveler 2's turn" {
		t.Fatalf("p2's timer = %+v", got)
	}
	c.Advance(30 * time.Second)
	text, _ = heard(t, c, s1)
	wantText(t, "nobody", text, "Nobody rolled.")
	wantText(t, "free again", texts(click(s2, "dice")), "You pick up the dice")
}

func TestClearingLeaverIsSkipped(t *testing.T) {
	w, c := clearingWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	click(s1, "dice")
	heard(t, c, s2)
	s1.Close()
	text, got := heard(t, c, s2)
	wantText(t, "gone", text, "traveler has gone.")
	wantText(t, "p2's turn", text, "Your turn!")
	if !hasItem(got, "done") {
		t.Errorf("p2 has no done: %s", text)
	}
}

func TestClearingSharedItems(t *testing.T) {
	w, c := clearingWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()

	wantText(t, "p1 purse", texts(click(s1, "purse")), "your share from the purse: 6 silver.")
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	got := s3.Handle(ws.Message{Type: "input", Text: "take the purse"})
	wantText(t, "late", texts(got), "The coins were shared before you came.")
	if hasItem(got, "purse") {
		t.Errorf("p3 sees the purse")
	}
	wantText(t, "p2 purse", texts(click(s2, "purse")), "6 silver.")
	wantText(t, "p2 purse again", texts(input(s2, "take the coins")), "You have your share.")
	wantText(t, "bag", texts(input(s2, "look in my bag")), "You carry: 6 silver.")

	wantText(t, "horn", texts(click(s1, "horn")), "You take the old hunting horn.")
	_, got = heard(t, c, s2)
	if hasItem(got, "horn") {
		t.Errorf("p2 still sees the horn")
	}
	wantText(t, "horn gone", texts(input(s2, "blow the horn")), "traveler took the old horn already.")
	s1.Close()
	text, got := heard(t, c, s2)
	wantText(t, "horn back", text, "traveler has gone. The old horn lies by the fire again.")
	if !hasItem(got, "horn") {
		t.Errorf("the horn is not back: %s", text)
	}
	heard(t, c, s3)

	wantText(t, "apple", texts(click(s2, "apples")), "You take a red apple.")
	wantText(t, "apple again", texts(input(s2, "an apple please")), "You have your apple already.")
	wantText(t, "apple p3", texts(click(s3, "apples")), "You take a red apple.")

	click(s2, "torches")
	click(s3, "torches")
	got = click(s2, "torches")
	if hasItem(got, "torches") {
		t.Errorf("torches left after three")
	}
	wantText(t, "torches", texts(input(s3, "take a torch")), "The torches are all gone.")
	wantText(t, "bag", texts(input(s2, "inventory")), "You carry: 1 apple, 6 silver, 2 torch.")
}

// However many players take the horn at once, one gets it.
func TestClearingOnceUnderConcurrentTakes(t *testing.T) {
	w, _ := clearingWorld(t)
	const n = 20
	ss := make([]ws.Session, n)
	for i := range ss {
		ss[i], _ = w.NewSession("", ws.Who{})
		defer ss[i].Close()
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for _, s := range ss {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if strings.Contains(texts(click(s, "horn")), "You take the old hunting horn.") {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d players took the horn", won)
	}
}

func TestClearingBlessingWearsOff(t *testing.T) {
	w, c := clearingWorld(t)
	s, _ := w.NewSession("", ws.Who{})
	defer s.Close()
	got := click(s, "shrine")
	wantText(t, "bless", texts(got), "A warm light settles on you")
	if m := find(got, "timer"); m == nil || m.ID != "fx_blessing" || m.Title != "Forest's blessing" || m.Ms != 60000 {
		t.Fatalf("blessing timer = %+v", got)
	}
	wantText(t, "blessed", texts(click(s, "shrine")), "still on you") // move 1
	for range 3 {                                                     // moves 2 to 4
		if text := texts(input(s, "bag")); strings.Contains(text, "fades") {
			t.Fatalf("faded early: %q", text)
		}
	}
	got = input(s, "bag") // move 5
	wantText(t, "after five moves", texts(got), "The warm light of the shrine fades.")
	if m := find(got, "timer"); m == nil || m.ID != "fx_blessing" || m.Ms != 0 {
		t.Errorf("the countdown stays: %+v", got)
	}

	click(s, "shrine")
	c.Advance(time.Minute)
	if text := texts(pending(t, s)); !strings.Contains(text, "The warm light of the shrine fades.") {
		t.Fatalf("after a minute: %q", text)
	}
}

// roundRoom uses lib/round.lua as it ships, with rounds that go on.
const roundRoom = `
local round = require("round")
function on_input(p, text)
  if text == "start" then round.turns(p, nil)
  elseif text == "done" then say(tostring(round.done(p)))
  elseif text == "act" then if round.may_act(p) then say("acts") end
  elseif text == "status" then say(round.status(p))
  elseif text == "leave" then round.leave(p) room.go("b")
  end
end
function on_turn(p) say("turn " .. p.id) end
function on_round_end(p, n) say("end " .. n) end
function on_change(p, keys) round.changed(p, keys) end
function on_leave(p) round.leave(p) end`

func roundWorld(t *testing.T) (*World, *fakeClock) {
	t.Helper()
	lib, err := fs.ReadFile(content.Demo(), "lib/round.lua")
	if err != nil {
		t.Fatal(err)
	}
	fsys := roomsFS(roundRoom, partyRoomB)
	fsys["lib/round.lua"] = &fstest.MapFile{Data: lib}
	p, err := pack.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	c := newClock()
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithClock(c.Now, c.After))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w, c
}

// A player who comes in during a round waits for the next; one who
// leaves the room is skipped.
func TestRoundJoinerWaitsLeaverSkipped(t *testing.T) {
	w, c := roundWorld(t)
	s1, _ := w.NewSession("", ws.Who{})
	defer s1.Close()
	s2, _ := w.NewSession("", ws.Who{})
	defer s2.Close()
	if got := texts(input(s1, "start")); got != "turn p1" {
		t.Fatalf("start = %q", got)
	}
	s3, _ := w.NewSession("", ws.Who{})
	defer s3.Close()
	if got := texts(input(s3, "act")); got != "Wait for your turn: traveler is acting." {
		t.Fatalf("p3 = %q", got)
	}
	input(s1, "done")
	if got, _ := heard(t, c, s2); got != "turn p2" {
		t.Fatalf("p2 = %q", got)
	}
	if got := texts(input(s3, "status")); got != "Round 1. Order: traveler, traveler 2 (acting)." {
		t.Errorf("status = %q", got)
	}
	// p2 ends the round: the next one has p3 at its end.
	if got := texts(input(s2, "done")); got != "end 1 | true" {
		t.Fatalf("round end = %q", got)
	}
	if got, _ := heard(t, c, s1); got != "turn p1" {
		t.Fatalf("p1 in round 2 = %q", got)
	}
	if got := texts(input(s3, "status")); got != "Round 2. Order: traveler (acting), traveler 2, traveler 3 (you)." {
		t.Errorf("status = %q", got)
	}
	// p2 walks out: skipped. p1 ends the turn: p3 is next.
	input(s2, "leave")
	input(s1, "done")
	if got, _ := heard(t, c, s3); got != "turn p3" {
		t.Fatalf("p3 = %q", got)
	}
	if got := texts(input(s3, "status")); got != "Round 2. Order: traveler, traveler 3 (you) (acting)." {
		t.Errorf("status = %q", got)
	}
	// p3 leaves the world in its turn: p1's round 3 starts.
	s3.Close()
	if got, _ := heard(t, c, s1); got != "turn p1" {
		t.Fatalf("p1 after p3 left = %q", got)
	}
}
