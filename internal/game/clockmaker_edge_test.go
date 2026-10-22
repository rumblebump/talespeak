package game

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of the clockmaker pack: players leaving at awkward moments,
// coming back, a fifth player, odd answers, a full world.

// travelers are the names a world without a store gives its players.
var travelers = []string{"traveler", "traveler 2", "traveler 3", "traveler 4"}

// joinN joins n players without accounts.
func (g *caseGame) joinN(n int) []ws.Session {
	g.t.Helper()
	var ss []ws.Session
	for range n {
		s, _ := g.join(ws.Who{})
		ss = append(ss, s)
	}
	return ss
}

// upTo plays the case with ss (at least two) from the telegram to the
// first room of level. The last of ss carries the brass key, and from
// level 3 on the storm lantern too.
func (g *caseGame) upTo(ss []ws.Session, level int) {
	g.t.Helper()
	g.startCase(ss[0], len(ss))
	g.level1(ss)
	g.saveSpot(ss, "cab", "lane")
	if level == 2 {
		return
	}
	rot := append([]ws.Session{ss[len(ss)-1]}, ss[:len(ss)-1]...)
	g.level2(rot)
	g.saveSpot(ss, "cab", "wharf")
	if level == 3 {
		return
	}
	g.level3(rot)
	g.saveSpot(ss, "cab", "club")
	if level == 4 {
		return
	}
	g.level4(ss, travelers[:len(ss)])
	g.saveSpot(ss, "cab", "yard")
}

// raw sends m as s without failing on an error reply.
func (g *caseGame) raw(s ws.Session, m ws.Message) []ws.Message {
	g.t.Helper()
	return s.Handle(m)
}

func errorOf(ms []ws.Message) string {
	for _, m := range ms {
		if m.Type == "error" {
			return m.Message
		}
	}
	return ""
}

const fullWorld = "(The game cannot remember that right now. Try again in a moment.)"

// refuseWorldAfter makes w refuse state.world writes that grow it once
// the returned counter is not negative and has run down: each growing
// write takes one from it while it is above zero, and the writes after
// that are refused. -1 (the start) refuses nothing. So 1 lets one write
// through and refuses the next, as a world at its limit does.
func refuseWorldAfter(w *World) *atomic.Int64 {
	var left atomic.Int64
	left.Store(-1)
	w.mu.Lock()
	defer w.mu.Unlock()
	reserve := w.world.Reserve
	w.world.Reserve = func(delta int64) error {
		if delta > 0 {
			switch n := left.Load(); {
			case n == 0:
				return errors.New("full")
			case n > 0:
				left.Store(n - 1)
			}
		}
		return reserve(delta)
	}
	return &left
}

// The lead is the first Irregular online: when the first one is away, the
// next one decides, at the save spot and in the casebook; the others are
// never asked.
func TestClockmakerLeadFallback(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	a, b, c := ss[0], ss[1], ss[2]
	g.startCase(a, 3)
	g.level1(ss) // c takes the key, a the lens
	a.Close()

	text, got := g.click(b, "casebook")
	want(t, "b's casebook", text, "traveler (away)", "traveler 2 (you) (lead)", "traveler 3.")
	dontWant(t, "a away is not lead", text, "traveler (away) (lead)")
	if d := lastOf(got, "dialog"); d == nil || len(d.Options) != 3 || d.Options[2] != "Play this level again without traveler" {
		t.Fatalf("lead b's casebook: %+v", d)
	}
	g.choose(b, got, 2)
	_, got = g.click(c, "casebook")
	if d := lastOf(got, "dialog"); d == nil || len(d.Options) != 2 {
		t.Fatalf("c is not the lead: %+v", d)
	}
	g.choose(c, got, 2)

	text, got = g.click(c, "cab")
	want(t, "c waits", text, "Still to come: traveler, traveler 2.")
	if lastOf(got, "dialog") != nil {
		t.Fatalf("c was asked: %s", text)
	}
	text, got = g.click(b, "cab")
	want(t, "b asked", text, "Still to come: traveler.")
	d := lastOf(got, "dialog")
	if d == nil || !strings.HasPrefix(d.Title, "traveler ") || !strings.Contains(d.Title, "Go on without them?") {
		t.Fatalf("b's dialog: %+v", d)
	}
	text, _ = g.choose(b, got, 1)
	want(t, "moved on", text, "room:lane")
	want(t, "c moved", g.hear(c), "room:lane")
	text, _ = g.click(b, "satchel")
	want(t, "a's lens came to b", text, "Holmes's magnifying glass")
	text, got = g.click(c, "casebook")
	want(t, "made for two", text, "Made for 2.", "traveler 2 (lead)")
	g.choose(c, got, 2)
}

// A fifth player waits on the pavement: never an Irregular, never moved by
// the party, never counted, and their leaving changes nothing.
func TestClockmakerFifthPlayer(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(4)
	fifth, first := g.join(ws.Who{})
	want(t, "fifth", first, "room:pavement", "already has four Irregulars")
	text, _ := g.send(fifth, ws.Message{Type: "input", Text: "let me in, I am a detective too"})
	want(t, "pavement", text, "Cabs rattle past.")
	text, got := g.click(ss[0], "telegram")
	want(t, "four, not five", text, "Start the case with 4 Irregulars")
	g.choose(ss[0], got, 1)
	dontWant(t, "fifth hears nothing", g.hear(fifth), "case begins")

	// the fifth leaves: the four play on, nothing rewinds
	fifth.Close()
	text, _ = g.click(ss[1], "slipper")
	want(t, "still on", text, "Strip 2 of 4")

	// a member leaves; a sixth still waits outside: the party keeps the
	// member's place
	ss[3].Close()
	_, first = g.join(ws.Who{})
	want(t, "sixth", first, "room:pavement")
	text, got = g.click(ss[0], "casebook")
	want(t, "party", text, "traveler 4 (away)")
	dontWant(t, "no fifth in the party", text, "traveler 5")
	dontWant(t, "no sixth in the party", text, "traveler 6")
	g.choose(ss[0], got, 2)
}

// The party goes on without a member: their seat is free, and the fifth
// player waiting outside can knock and join, for the levels to come. A
// sixth finds the case full again.
func TestClockmakerKnockForFreeSeat(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(4)
	fifth, first := g.join(ws.Who{})
	want(t, "fifth", first, "room:pavement")
	dontWant(t, "no seat yet", first, "knock=")
	text, _ := g.send(fifth, ws.Message{Type: "input", Text: "knock knock"})
	want(t, "full", text, "Nobody opens.")
	g.startCase(ss[0], 4)
	g.level1(ss) // ss[3] takes the key
	ss[3].Close()
	for _, s := range ss[1:3] {
		g.click(s, "cab")
	}
	_, got := g.click(ss[0], "cab")
	text, _ = g.choose(ss[0], got, 1)
	want(t, "on without the fourth", text, "room:lane")
	want(t, "a seat for the fifth", g.hear(fifth), "knock=")
	text, _ = g.click(fifth, "knock")
	want(t, "fifth joins", text, "room:lane")
	text, got = g.click(fifth, "casebook")
	want(t, "in the party", text, "traveler 5 (you)", "Made for 3.")
	g.choose(fifth, got, 2)
	text, _ = g.click(fifth, "satchel")
	want(t, "empty satchel", text, "Your satchel is empty.")
	_, first = g.join(ws.Who{})
	want(t, "sixth", first, "room:pavement")
	dontWant(t, "full again", first, "knock=")

	// the fifth is needed at the next save spot (the level is made for
	// three: ss[2] waits in the lane)
	g.level2([]ws.Session{ss[0], fifth, ss[1]})
	text, _ = g.click(ss[0], "satchel")
	want(t, "the lead has the lantern", text, "storm lantern")
	for _, s := range ss[:3] {
		g.click(s, "cab")
	}
	text, _ = g.click(fifth, "cab")
	want(t, "fifth moves the party", text, "room:wharf")
}

// Who looked in and left before the telegram gives their seat back: the
// case starts for those in the study, and a new player takes the seat.
func TestClockmakerTelegramFreesSeat(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	ss[2].Close()
	text, got := g.click(ss[0], "telegram")
	want(t, "two here", text, "Start the case with 2 Irregulars (traveler, traveler 2)")
	g.choose(ss[0], got, 1)
	d, first := g.join(ws.Who{})
	want(t, "d takes the seat", first, "room:study")
	text, _ = g.click(d, "violin")
	want(t, "made for two", text, "Only Holmes's clutter.")
	// (a world without a store names d "traveler 3" again)
	text, got = g.click(d, "casebook")
	want(t, "party", text, "Irregulars: traveler (lead), traveler 2, traveler 3 (you).")
	dontWant(t, "c gone", text, "(away)")
	g.choose(d, got, 2)
}

// The save spot goes by who is in its room: a member still in the shop
// counts as not here, and when the lead goes on without them, they find
// themselves outside on the pavement, where the free seat lets them knock
// and join again.
func TestClockmakerGoOnWithoutOneInAnotherRoom(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	g.upTo(ss, 2)
	a, b, c := ss[0], ss[1], ss[2]
	g.level2([]ws.Session{c, a, b}) // b stays in the lane, a and c come back
	g.click(c, "shopdoor")          // c goes back into the shop
	g.click(b, "cab")
	text, got := g.click(a, "cab")
	want(t, "c not here", text, "Still to come: traveler 3.")
	d := lastOf(got, "dialog")
	if d == nil || !strings.HasPrefix(d.Title, "traveler 3 ") {
		t.Fatalf("dialog: %s", text)
	}
	text, _ = g.choose(a, got, 1)
	want(t, "on", text, "room:wharf")
	want(t, "c outside", g.hear(c), "room:pavement", "knock=")
	text, _ = g.click(a, "satchel")
	want(t, "c's lantern came to a", text, "storm lantern")
	text, _ = g.click(c, "knock")
	want(t, "c back in", text, "room:wharf")
}

// The Irregular locked in the workshop leaves the game: the lock opens
// for the others, and the next one in is locked in instead.
func TestClockmakerLockedInLeaves(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	g.upTo(ss, 2)
	a, b, c := ss[0], ss[1], ss[2] // c has the key
	text, _ := g.click(c, "shopdoor")
	want(t, "unlock", text, "room:shop")
	text, _ = g.click(a, "shopdoor")
	want(t, "a in", text, "room:shop")
	text, _ = g.click(a, "workshop")
	want(t, "a locked in", text, "You are locked in!")
	text, _ = g.click(c, "workshop")
	want(t, "c locked out", text, "The workshop door is locked.", "traveler inside")

	a.Close()
	want(t, "door unlocked for c", g.hear(c), "workshop=door to the workshop")
	text, _ = g.click(c, "workshop")
	want(t, "c locked in now", text, "room:workshop", "You are locked in!")
	text, _ = g.click(c, "door")
	want(t, "c stays", text, "The door does not move.")
	// b, the lead now, may play the level again without a
	text, got := g.click(b, "casebook")
	want(t, "b leads", text, "traveler 2 (you) (lead)")
	text, _ = g.choose(b, got, 3)
	want(t, "again", text, "room:lane")
	want(t, "c out of the workshop", g.hear(c), "room:lane")
	text, _ = g.click(c, "satchel")
	want(t, "c keeps the key", text, "brass key")
}

// In the warehouse: the one on the lift who leaves steps off it, and when
// the lantern holder leaves, the warehouse goes dark for the others.
func TestClockmakerWarehouseLeaves(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	g.upTo(ss, 3)
	a, b, c := ss[0], ss[1], ss[2] // c has the lantern
	text, _ := g.click(a, "warehouse")
	want(t, "dark", text, "pitch dark")
	text, _ = g.click(c, "warehouse")
	want(t, "lit", text, "You raise the storm lantern.")
	text, _ = g.click(b, "warehouse")
	dontWant(t, "b sees", text, "pitch dark")
	text, _ = g.click(a, "lift")
	want(t, "a on the lift", text, "It needs more weight!")

	a.Close()
	text, _ = g.click(b, "lift")
	want(t, "a stepped off when leaving", text, "It needs more weight!")
	dontWant(t, "not up", text, "rises to the loft")

	c.Close()
	want(t, "dark again", g.hear(b), "dark=darkness")
	text, _ = g.click(b, "dark")
	want(t, "stumble", text, "You need light!")
}

// An Irregular waiting at the save spot who leaves no longer waits, and
// the lead decides about them; "Wait for them" changes nothing.
func TestClockmakerLeaveWhileWaiting(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	a, b, c := ss[0], ss[1], ss[2]
	g.startCase(a, 3)
	g.level1(ss)
	text, _ := g.click(b, "cab")
	want(t, "b waits", text, "Still to come: traveler, traveler 3.")
	b.Close()
	text, _ = g.click(c, "cab")
	want(t, "b no longer waits", text, "Still to come: traveler, traveler 2.")
	text, got := g.click(a, "cab")
	want(t, "lead", text, "Still to come: traveler 2.")
	text, _ = g.choose(a, got, 2)
	dontWant(t, "waiting", text, "room:lane")
	// the last one waiting asks again
	text, got = g.click(a, "cab")
	if d := lastOf(got, "dialog"); d == nil {
		t.Fatalf("asked again: %s", text)
	}
	text, _ = g.choose(a, got, 1)
	want(t, "on", text, "room:lane")
	want(t, "c on", g.hear(c), "room:lane")
}

// The party moves while a player has a question open: the question
// closes and the player moves with the party.
func TestClockmakerPlayAgainClosesQuestion(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	a, b, c := ss[0], ss[1], ss[2]
	g.startCase(a, 3)
	c.Close()
	_, asked := g.click(b, "desk")
	q := lastOf(asked, "ask")
	if q == nil {
		t.Fatalf("no question: %s", caseText(asked))
	}
	_, got := g.click(a, "casebook")
	text, _ := g.choose(a, got, 3)
	want(t, "again", text, "room:study")
	want(t, "b moved", g.hear(b), "room:study")
	if e := errorOf(g.raw(b, ws.Message{Type: "answer", ID: q.ID, Text: "pendulum"})); e == "" {
		t.Fatal("the old question still took an answer")
	}
	g.startCase(a, 2)
	text, _ = g.click(b, "slipper")
	want(t, "made for two", text, "Strip 2 of 2")
}

// The safe and the crate take odd answers without errors: a dotted time
// with a leading zero opens, empty and too long answers are refused by the
// engine with the question still open, and nonsense keeps the safe shut.
// The crate code works reversed and with any separator.
func TestClockmakerOddAnswers(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(2)
	a, b := ss[0], ss[1]
	g.startCase(a, 2)
	text, _ := g.send(a, ws.Message{Type: "input", Text: strings.Repeat("zq", 250)})
	want(t, "long input", text, "Holmes would know what to do with")
	g.level1(ss) // b has the key
	g.saveSpot(ss, "cab", "lane")
	g.click(b, "shopdoor")
	g.hear(a)
	g.click(a, "shopdoor")
	times := map[string]string{}
	for _, clock := range []string{"owl", "ship", "rose", "moon", "crown"} {
		text, _ = g.click(a, clock)
		times[clock] = clockRE.FindStringSubmatch(text)[2]
	}
	g.click(b, "workshop")
	text, _ = g.click(b, "note")
	var tm string
	for clock, at := range times {
		if strings.Contains(text, "the clock with the "+clock) {
			tm = at
		}
	}
	if tm == "" {
		t.Fatalf("note: %q", text)
	}

	_, got := g.click(b, "safe")
	if e := errorOf(g.raw(b, ws.Message{Type: "answer", ID: lastOf(got, "ask").ID, Text: "   "})); e != "Type an answer." {
		t.Fatalf("empty answer: %q", e)
	}
	if e := errorOf(g.raw(b, ws.Message{Type: "answer", ID: lastOf(got, "ask").ID, Text: strings.Repeat("7", 501)})); !strings.Contains(e, "too long") {
		t.Fatalf("too long: %q", e)
	}
	for _, odd := range []string{", ,", strings.Repeat("7:25 ", 100), tm + " " + tm, "half past", "0", "::", "....", "07.25.25"} {
		text, _ = g.answer(b, got, odd)
		want(t, "odd "+strconv.Quote(odd), text, "The safe stays shut.")
		_, got = g.click(b, "safe")
	}
	dotted := "0" + strings.Replace(tm, ":", ".", 1)
	text, _ = g.answer(b, got, " "+dotted+" ")
	want(t, "dotted "+dotted, text, "The safe opens")
	g.click(b, "door")
	g.click(b, "lane")
	g.click(a, "lane")
	g.saveSpot(ss, "cab", "wharf")

	// level 3: b has the lantern
	variant := ""
	for _, s := range ss {
		_, got = g.click(s, "board")
		variant = mapRE.FindStringSubmatch(lastOf(got, "image").URL)[1]
	}
	text, _ = g.click(a, "board")
	want(t, "again", text, "You look at your piece of the map again.")
	g.click(a, "warehouse")
	g.click(b, "warehouse")
	g.click(a, "lift")
	g.click(b, "lift")
	code := crates[variant]
	_, got = g.click(a, "crates")
	text, _ = g.answer(a, got, "--")
	want(t, "crate with no letters", text, "There is no crate like that.")
	for _, odd := range []string{strings.Repeat("Q", 500), code + code, code[:1] + "9" + code[1:], strings.ToLower(code[:1])} {
		_, got = g.click(a, "crates")
		text, _ = g.answer(a, got, odd)
		want(t, "crate "+strconv.Quote(odd), text, "Only old fishing nets.")
	}
	// both look at the crates; b names it first, then a does too
	_, gotA := g.click(a, "crates")
	_, gotB := g.click(b, "crates")
	text, _ = g.answer(b, gotB, code[1:]+" - "+strings.ToLower(code[:1]))
	want(t, "reversed", text, "Inside crate "+code)
	g.c.Advance(DeliverGap)
	pending(t, a)
	text, _ = g.answer(a, gotA, code)
	dontWant(t, "one card only", text, "Inside crate")
	textA, _ := g.click(a, "satchel")
	textB, _ := g.click(b, "satchel")
	if strings.Count(textA+textB, "Diogenes Club card") != 1 {
		t.Fatalf("club cards: a %q, b %q", textA, textB)
	}
}

// A voter leaves in the middle of the vote: the vote waits for them, and
// the lead plays the level again with those here, who then hold all four
// evidence cards and can accuse together.
func TestClockmakerVoterLeaves(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	g.upTo(ss, 5)
	a, b, c := ss[0], ss[1], ss[2]
	for _, s := range []ws.Session{a, b} {
		_, got := g.click(s, "accuse")
		g.choose(s, got, 1)
	}
	c.Close()
	_, got := g.click(a, "accuse")
	text, _ := g.choose(a, got, 1)
	want(t, "waits for c", text, "Lestrade waits for the others.")

	_, got = g.click(a, "casebook")
	text, _ = g.choose(a, got, 3)
	want(t, "again", text, "room:yard")
	want(t, "b again", g.hear(b), "room:yard")
	all := ""
	cards := 0
	for _, s := range []ws.Session{a, b} {
		text, _ = g.click(s, "satchel")
		cards += len(cardRE.FindAllString(text, -1))
		all += text
	}
	if cards != 4 {
		t.Fatalf("%d cards with the two left, want 4: %q", cards, all)
	}
	thief := thiefFrom(t, all)
	_, got = g.click(a, "accuse")
	text, _ = g.choose(a, got, thief)
	want(t, "old votes gone", text, "Lestrade waits for the others.")
	_, got = g.click(b, "accuse")
	text, _ = g.choose(b, got, thief)
	want(t, "solved", text, "THE END.")
	want(t, "a too", g.hear(a), "THE END.")
}

// The end of the case: a new player finds it, a fifth one waits outside,
// and the ending says the case is closed when asked again.
func TestClockmakerAfterTheEnd(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	g.upTo(ss, 5)
	g.level5(ss)
	text, _ := g.send(ss[0], ws.Message{Type: "input", Text: "accuse the thief"})
	want(t, "closed", text, "The case is closed.")
	text, _ = g.send(ss[0], ws.Message{Type: "input", Text: "satchel"})
	want(t, "satchel works", text, "evidence card")
	d, first := g.join(ws.Who{})
	want(t, "fourth after the end", first, "room:yard", "THE END.")
	_, first = g.join(ws.Who{})
	want(t, "fifth after the end", first, "room:pavement")
	// everyone leaving after the end rewinds nothing
	for _, s := range append(ss, d) {
		s.Close()
	}
	_, first = g.join(ws.Who{})
	want(t, "still outside", first, "room:pavement")
}

// Once the case is solved, the casebook no longer offers to play the
// level again: nothing would start again.
func TestClockmakerPlayAgainAfterTheEnd(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(3)
	g.upTo(ss, 5)
	g.level5(ss)
	ss[2].Close()
	text, got := g.send(ss[0], ws.Message{Type: "input", Text: "casebook"})
	d := lastOf(got, "dialog")
	if d == nil {
		t.Fatalf("no casebook: %s", text)
	}
	if len(d.Options) < 3 {
		return
	}
	text, _ = g.choose(ss[0], got, 3)
	dontWant(t, "no new start after the end", text, "The level starts again")
}

// The rest need accounts, to come back as the same player.

// A member the party went on without comes back: they join again (their
// seat is free), play on in the party's level with an empty satchel, are
// needed at the next save spot, and the level after is made for them too.
func TestClockmakerRejoinAfterLeftBehind(t *testing.T) {
	g, who, stop := storeCase(t, filepath.Join(t.TempDir(), "case.db"))
	defer stop()
	mira, _ := g.join(who[0])
	zed, _ := g.join(who[1])
	g.startCase(mira, 2)
	g.level1([]ws.Session{mira, zed}) // zed takes the key
	zed.Close()
	_, got := g.click(mira, "cab")
	text, _ := g.choose(mira, got, 1)
	want(t, "on without zed", text, "room:lane")

	zed, first := g.join(who[1])
	want(t, "zed to the party's level", first, "room:lane")
	text, got = g.click(zed, "casebook")
	want(t, "zed plays again", text, "Made for 1.", "Zed (you)")
	dontWant(t, "zed not left behind", text, "left behind")
	g.choose(zed, got, 2)
	text, _ = g.click(zed, "satchel")
	want(t, "zed's things went to mira", text, "Your satchel is empty.")

	text, _ = g.click(mira, "shopdoor")
	want(t, "mira has the key", text, "The brass key turns.")
	times := map[string]string{}
	for _, clock := range []string{"owl", "ship", "rose", "moon", "crown"} {
		text, _ = g.click(mira, clock)
		times[clock] = clockRE.FindStringSubmatch(text)[2]
	}
	text, _ = g.click(mira, "workshop")
	dontWant(t, "made for one: no lock", text, "locked in")
	text, _ = g.click(mira, "note")
	var tm string
	for clock, at := range times {
		if strings.Contains(text, "the clock with the "+clock) {
			tm = at
		}
	}
	_, got = g.click(mira, "safe")
	text, _ = g.answer(mira, got, tm)
	want(t, "safe", text, "The safe opens")
	g.click(mira, "door")
	g.click(mira, "lane")
	text, _ = g.click(mira, "cab")
	want(t, "zed is needed", text, "Still to come: Zed.")
	text, _ = g.click(zed, "cab")
	want(t, "both", text, "room:wharf")
	want(t, "mira moved", g.hear(mira), "room:wharf")
	text, got = g.click(mira, "casebook")
	want(t, "made for two again", text, "Made for 2.")
	g.choose(mira, got, 2)
}

// Everyone leaves during level 1 after the telegram: coming back, the
// strips are as many as the level is made for, not one whole word.
func TestClockmakerStripsAfterEveryoneLeft(t *testing.T) {
	g, who, stop := storeCase(t, filepath.Join(t.TempDir(), "case.db"))
	defer stop()
	mira, _ := g.join(who[0])
	zed, _ := g.join(who[1])
	g.startCase(mira, 2)
	mira.Close()
	zed.Close()
	mira, _ = g.join(who[0])
	zed, _ = g.join(who[1])
	text, _ := g.click(mira, "bookshelf")
	want(t, "mira's strip", text, "Strip 1 of 2 reads:")
	text, _ = g.click(zed, "slipper")
	want(t, "zed's strip", text, "Strip 2 of 2 reads:")
}

// A player who comes back while the lead's "Go on without them?" dialog
// is open is not dropped: who is missing is checked again after the
// answer, so Zed keeps his key and his place.
func TestClockmakerGoOnWithoutComesBack(t *testing.T) {
	g, who, stop := storeCase(t, filepath.Join(t.TempDir(), "case.db"))
	defer stop()
	mira, _ := g.join(who[0])
	zed, _ := g.join(who[1])
	g.startCase(mira, 2)
	g.level1([]ws.Session{mira, zed}) // zed takes the key
	zed.Close()
	text, got := g.click(mira, "cab")
	want(t, "asked", text, "Still to come: Zed.")
	if d := lastOf(got, "dialog"); d == nil || !strings.HasPrefix(d.Title, "Zed ") || !strings.Contains(d.Title, "Go on without them?") {
		t.Fatalf("dialog: %s", text)
	}
	zed, first := g.join(who[1])
	want(t, "zed back", first, "room:study")
	g.choose(mira, got, 1)
	g.hear(zed)
	text, _ = g.click(zed, "satchel")
	want(t, "zed keeps his key", text, "brass key to the clock shop")
	text, got = g.click(zed, "casebook")
	dontWant(t, "zed is not left behind", text, "left behind")
	g.choose(zed, got, 2)
}

// When the world cannot keep the case's size, the telegram does not say
// the case begins.
func TestClockmakerTelegramWorldFull(t *testing.T) {
	g := newCase(t)
	a, _ := g.join(ws.Who{})
	g.join(ws.Who{})
	full := refuseWorld(g.w)
	full.Store(true)
	_, got := g.click(a, "telegram")
	text, _ := g.choose(a, got, 1)
	want(t, "refused", text, fullWorld)
	dontWant(t, "not begun", text, "The case begins")
	full.Store(false)
	g.startCase(a, 2)
}

// A full world at the save spot: nobody waits and nobody moves on; once
// it has room, the save spot works.
func TestClockmakerSaveSpotWorldFull(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(2)
	g.startCase(ss[0], 2)
	g.level1(ss)
	full := refuseWorld(g.w)
	full.Store(true)
	text, _ := g.click(ss[0], "cab")
	want(t, "refused", text, fullWorld)
	dontWant(t, "not waiting", text, "You wait here")
	full.Store(false)
	g.saveSpot(ss, "cab", "lane")
}

// When the world refuses the lantern and the ticket, the safe stays shut,
// so it can still be opened once there is room (the strips, the map
// pieces, the club card and the evidence cards are given the same way:
// first into the satchel, then marked taken).
func TestClockmakerSafeWorldFull(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(2)
	a, b := ss[0], ss[1]
	g.startCase(a, 2)
	g.level1(ss)
	g.saveSpot(ss, "cab", "lane")
	g.click(b, "shopdoor")
	g.hear(a)
	g.click(a, "shopdoor")
	times := map[string]string{}
	for _, clock := range []string{"owl", "ship", "rose", "moon", "crown"} {
		text, _ := g.click(a, clock)
		times[clock] = clockRE.FindStringSubmatch(text)[2]
	}
	g.click(b, "workshop")
	text, _ := g.click(b, "note")
	var tm string
	for clock, at := range times {
		if strings.Contains(text, "the clock with the "+clock) {
			tm = at
		}
	}
	full := refuseWorld(g.w)
	_, got := g.click(b, "safe")
	full.Store(true)
	text, _ = g.answer(b, got, tm)
	want(t, "refused", text, fullWorld)
	full.Store(false)
	_, got = g.click(b, "safe")
	if lastOf(got, "ask") != nil {
		g.answer(b, got, tm)
	}
	text, _ = g.click(b, "satchel")
	want(t, "lantern after the world had room", text, "storm lantern")
}

// When the world refuses the strip, it is not marked found, so it can be
// found again once there is room.
func TestClockmakerStripLost(t *testing.T) {
	g := newCase(t)
	ss := g.joinN(2)
	g.startCase(ss[0], 2)
	left := refuseWorldAfter(g.w)
	left.Store(1) // the mark is kept, the satchel refused
	text, _ := g.click(ss[0], "bookshelf")
	want(t, "refused", text, fullWorld)
	left.Store(-1)
	text, _ = g.click(ss[0], "bookshelf")
	want(t, "found once the world has room", text, "Strip 1 of 2 reads:")
}
