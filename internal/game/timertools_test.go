package game

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// /timers lists the world's timers with numbers; /extend and /cancel act
// on them, the players affected see the change and a mark, and the audit
// log has every change.
func TestTimerTools(t *testing.T) {
	w, c, gm, p := timeWorld(t)
	input(p, "start")
	input(p, "room")
	input(p, "ask")
	pending(t, gm)

	list := notices(input(gm, "/timers"))
	if len(list) != 1 {
		t.Fatalf("/timers: %v", list)
	}
	for _, want := range []string{
		`1. room A (a): "Turn" (turn), 1m left`,
		`2. traveler 2: "Guards" (guards), 1m left`,
		`3. traveler 2's question "Fight or flee?", 30s left`,
	} {
		if !strings.Contains(list[0], want) {
			t.Errorf("/timers lacks %q:\n%s", want, list[0])
		}
	}

	got := input(gm, "/extend 2 5m")
	if n := notices(got); len(n) != 1 || n[0] != "server: Done: 6m left." {
		t.Errorf("/extend answers %v", n)
	}
	pushed := pending(t, p)
	if ms(t, pushed, "guards") != 360000 || !strings.Contains(strings.Join(notices(pushed), "|"), `gm traveler: gave 5m more: "Guards".`) {
		t.Errorf("the player heard %+v", pushed)
	}
	input(gm, "/extend 3 1m")
	if got := ms(t, pending(t, p), "question:d1"); got != 90000 {
		t.Errorf("the question has %d ms", got)
	}
	c.Advance(31 * time.Second)
	if got := texts(pending(t, p)); strings.Contains(got, "close") {
		t.Fatalf("the question timed out at its old deadline: %q", got)
	}

	input(gm, "/cancel 1")
	if got := notices(pending(t, p)); len(got) != 1 || got[0] != `gm traveler: stopped "Turn".` {
		t.Errorf("the room heard %v", got)
	}
	c.Advance(DeliverGap)
	if got := ms(t, pending(t, p), "room:turn"); got != 0 {
		t.Errorf("the room's countdown is still shown: %d", got)
	}
	if got := notices(input(gm, "/cancel 1")); len(got) != 1 || !strings.Contains(got[0], "not running as listed") {
		t.Errorf("a stale number: %v", got)
	}
	input(gm, "/cancel 3")
	if got := pending(t, p); ms(t, got, "question:d1") != 0 || !strings.Contains(texts(got), "notice") {
		t.Errorf("the question's limit went with %+v", got)
	}
	c.Advance(time.Hour)
	got = pending(t, p)
	if s := texts(got); strings.Contains(s, "close") || strings.Contains(s, "up turn") || !strings.Contains(s, "up guards nil") {
		t.Errorf("after an hour: %q", s)
	}
	if got := texts(p.Handle(ws.Message{Type: "choose", ID: "d1", Option: 2})); got != "answer 2" {
		t.Errorf("the question still waits: %q", got)
	}

	var actions []string
	for _, e := range w.auditLog {
		actions = append(actions, e.Action+" "+e.Detail)
	}
	want := []string{
		`extend timer traveler 2: "Guards" (guards) +5m`,
		`extend timer traveler 2's question "Fight or flee?" +1m`,
		`cancel timer room A (a): "Turn" (turn)`,
		`cancel timer traveler 2's question "Fight or flee?"`,
	}
	if !slices.Equal(actions, want) {
		t.Errorf("audit = %q", actions)
	}
}

// What /extend and /cancel refuse: no list yet, a number off the list, a
// bad time, a timer past a day; a secret timer is named by its name only.
func TestTimerToolsRefuse(t *testing.T) {
	w, _, gm, p := timeWorld(t)
	if got := notices(input(gm, "/timers")); len(got) != 1 || got[0] != "server: No timers run here now." {
		t.Errorf("no timers: %v", got)
	}
	if got := notices(input(gm, "/extend 1 5m")); len(got) != 1 || !strings.Contains(got[0], "/timers first") {
		t.Errorf("no list: %v", got)
	}
	input(p, "secret")
	if got := strings.Join(notices(input(gm, "/timers")), ""); !strings.Contains(got, "1. traveler 2: secret timer hush, 1m left") {
		t.Errorf("a secret timer: %s", got)
	}
	for _, args := range []string{"0 5m", "2 5m", "x 5m", "1", "1 soon", "1 -5m", "1 0s", "1 25h", "1 99999999999999999999999"} {
		if got := notices(input(gm, "/extend "+args)); len(got) != 1 || strings.Contains(got[0], "Done") {
			t.Errorf("/extend %s: %v", args, got)
		}
	}
	if got := notices(input(gm, "/extend 1 23h59m30s")); len(got) != 1 || got[0] != "server: A timer runs at most a day." {
		t.Errorf("past a day: %v", got)
	}
	if got := texts(input(p, "left")); got != "nil 60 nil" {
		t.Errorf("refused changes changed the timer: %s", got)
	}
	if len(w.auditLog) != 0 {
		t.Errorf("refused changes were logged: %+v", w.auditLog)
	}
	input(gm, "/extend 1 1m")
	if got := pending(t, p); len(notices(got)) != 1 || notices(got)[0] != "gm traveler: gave a secret timer 1m more." {
		t.Errorf("the player heard %+v", got)
	}
	if got := texts(input(p, "left")); got != "nil 120 nil" {
		t.Errorf("left = %s", got)
	}
}

// A room timer that ran out in an empty room shows as waiting, and
// /extend gives it a new deadline from now.
func TestExtendWaitingRoomTimer(t *testing.T) {
	_, c, gm, p := timeWorld(t)
	input(p, "room")
	input(p, "go")
	input(gm, "/go b")
	c.Advance(time.Minute)
	if got := strings.Join(notices(input(gm, "/timers")), ""); !strings.Contains(got, "ran out, waits for a player to come in") {
		t.Fatalf("/timers: %s", got)
	}
	input(gm, "/extend 1 30s")
	input(gm, "/go a")
	c.Advance(29 * time.Second)
	if got := texts(pending(t, gm)); strings.Contains(got, "up turn") {
		t.Fatalf("too early: %q", got)
	}
	c.Advance(time.Second)
	if got := texts(pending(t, gm)); !strings.Contains(got, "up turn room") {
		t.Errorf("at the new deadline: %q", got)
	}
}
