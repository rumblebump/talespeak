package game

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The clockmaker pack (content/clockmaker): a co-op case for 2 to 4
// players in five levels, with save spots between them. These tests play
// it as the players would, reading every secret from what the game shows.

const clockmakerDir = "../../content/clockmaker"

type caseGame struct {
	t *testing.T
	w *World
	c *fakeClock
}

func clockmakerPack(t *testing.T) *pack.Pack {
	t.Helper()
	p, err := pack.Load(os.DirFS(clockmakerDir))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newCase(t *testing.T, opts ...Option) *caseGame {
	t.Helper()
	c := newClock()
	w, err := NewWorld(clockmakerPack(t), script.DefaultLimits, quiet, append([]Option{WithClock(c.Now, c.After)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return &caseGame{t: t, w: w, c: c}
}

// join starts a session and returns it with what it saw first.
func (g *caseGame) join(who ws.Who) (ws.Session, string) {
	g.t.Helper()
	s, first := g.w.NewSession("", who)
	if s == nil {
		g.t.Fatalf("refused: %+v", first)
	}
	g.t.Cleanup(func() { s.Close() })
	return s, caseText(first)
}

// caseText is texts with the speaker before each line, as the client
// shows it.
func caseText(ms []ws.Message) string {
	var out []string
	for _, m := range ms {
		switch {
		case m.Type == "text" && m.From != "":
			m.Text = m.From + ": " + m.Text
		case m.Type == "dialog" || m.Type == "ask":
			out = append(out, m.Type+": "+m.Title)
			continue
		case m.Type == "items":
			var items []string
			for _, it := range m.Items {
				items = append(items, it.ID+"="+it.Label)
			}
			out = append(out, "items: "+strings.Join(items, ", "))
			continue
		}
		out = append(out, texts([]ws.Message{m}))
	}
	return strings.Join(out, " | ")
}

func (g *caseGame) send(s ws.Session, m ws.Message) (string, []ws.Message) {
	g.t.Helper()
	got := s.Handle(m)
	for _, x := range got {
		if x.Type == "error" {
			g.t.Fatalf("%+v: error %q", m, x.Message)
		}
	}
	return caseText(got), got
}

// click clicks item as s, once s has heard what the others did (the
// items it can click change with it).
func (g *caseGame) click(s ws.Session, item string) (string, []ws.Message) {
	g.t.Helper()
	g.c.Advance(DeliverGap)
	pending(g.t, s)
	return g.send(s, ws.Message{Type: "click", Item: item})
}

func (g *caseGame) choose(s ws.Session, ms []ws.Message, option int) (string, []ws.Message) {
	g.t.Helper()
	d := lastOf(ms, "dialog")
	if d == nil {
		g.t.Fatalf("no dialog in %s", caseText(ms))
	}
	return g.send(s, ws.Message{Type: "choose", ID: d.ID, Option: option})
}

func (g *caseGame) answer(s ws.Session, ms []ws.Message, text string) (string, []ws.Message) {
	g.t.Helper()
	q := lastOf(ms, "ask")
	if q == nil {
		g.t.Fatalf("no question in %s", caseText(ms))
	}
	return g.send(s, ws.Message{Type: "answer", ID: q.ID, Text: text})
}

// hear delivers on_change and returns what s got pushed.
func (g *caseGame) hear(s ws.Session) string {
	g.t.Helper()
	g.c.Advance(DeliverGap)
	return caseText(pending(g.t, s))
}

func want(t *testing.T, step, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Fatalf("%s: %q lacks %q", step, got, w)
		}
	}
}

func dontWant(t *testing.T, step, got, bad string) {
	t.Helper()
	if strings.Contains(got, bad) {
		t.Fatalf("%s: %q has %q", step, got, bad)
	}
}

var stripRE = regexp.MustCompile(`Strip (\d) of (\d) reads: ([A-Z]+)`)

// startCase reads the telegram as s and starts the case.
func (g *caseGame) startCase(s ws.Session, n int) {
	g.t.Helper()
	text, got := g.click(s, "telegram")
	want(g.t, "telegram", text, "Holmes: IRREGULARS.", "Start the case with "+strconv.Itoa(n)+" Irregular")
	text, _ = g.choose(s, got, 1)
	want(g.t, "start", text, "The case begins for "+strconv.Itoa(n)+" Irregular")
}

// level1 plays 221B Baker Street with players ss (the case started) up
// to the cab, and returns the word of the desk.
func (g *caseGame) level1(ss []ws.Session) {
	t := g.t
	t.Helper()
	spots := []string{"bookshelf", "slipper", "violin", "scuttle"}
	parts := make([]string, len(ss))
	for i, s := range ss {
		text, _ := g.click(s, spots[i])
		m := stripRE.FindStringSubmatch(text)
		if m == nil || m[1] != strconv.Itoa(i+1) || m[2] != strconv.Itoa(len(ss)) {
			t.Fatalf("player %d searches the %s: %q", i+1, spots[i], text)
		}
		parts[i] = m[3]
	}
	word := strings.Join(parts, "")
	text, got := g.click(ss[0], "desk")
	text, _ = g.answer(ss[0], got, "WRONGWORD")
	want(t, "wrong word", text, "The desk stays locked.")
	_, got = g.click(ss[0], "desk")
	text, _ = g.answer(ss[0], got, strings.ToLower(word))
	want(t, "desk", text, "The desk opens.", "Holmes: My secret cupboard")
	levers := []string{"bust", "candlestick", "clock", "lamp"}
	for i, s := range ss {
		text, _ = g.click(s, levers[i])
		if i < len(ss)-1 {
			want(t, "lever", text, "and hold it. Quick")
		} else {
			want(t, "last lever", text, "a secret cupboard!")
		}
	}
	text, _ = g.click(ss[len(ss)-1], "cupboard")
	want(t, "cupboard", text, "brass key")
}

// cab sends every player to the save spot; the last one moves the party.
func (g *caseGame) saveSpot(ss []ws.Session, item, next string) {
	t := g.t
	t.Helper()
	for i, s := range ss {
		text, _ := g.click(s, item)
		if i < len(ss)-1 {
			want(t, "waits", text, "You wait here. Still to come:")
			continue
		}
		want(t, "last one", text, "room:"+next)
	}
	for _, s := range ss[:len(ss)-1] {
		want(t, "moved along", g.hear(s), "The case moves on", "room:"+next)
	}
}

var clockRE = regexp.MustCompile(`The clock with the (\w+) shows (\d+:\d\d)\.`)

// level2 plays the clock shop: ss[0] has the key and goes into the
// workshop.
func (g *caseGame) level2(ss []ws.Session) {
	t := g.t
	t.Helper()
	text, _ := g.click(ss[1], "shopdoor")
	want(t, "no key", text, "The door is locked. ", " has the brass key.")
	text, _ = g.click(ss[0], "shopdoor")
	want(t, "unlock", text, "The brass key turns.", "room:shop")
	g.hear(ss[1])
	text, _ = g.click(ss[1], "shopdoor")
	want(t, "open door", text, "room:shop")
	times := map[string]string{}
	for _, clock := range []string{"owl", "ship", "rose", "moon", "crown"} {
		text, _ = g.click(ss[1], clock)
		m := clockRE.FindStringSubmatch(text)
		if m == nil || m[1] != clock {
			t.Fatalf("clock %s: %q", clock, text)
		}
		times[clock] = m[2]
	}
	text, _ = g.click(ss[0], "workshop")
	want(t, "workshop", text, "room:workshop", "You are locked in!")
	want(t, "slam", g.hear(ss[1]), "The workshop door locks behind ")
	text, _ = g.click(ss[1], "workshop")
	want(t, "locked out", text, "The workshop door is locked.")
	text, _ = g.click(ss[0], "door")
	want(t, "locked in", text, "The door does not move.")
	text, _ = g.click(ss[0], "note")
	idx := strings.Index(text, "the time of the clock")
	if idx < 0 {
		t.Fatalf("note: %q", text)
	}
	var answer []string
	for _, w := range strings.FieldsFunc(text[idx:], func(r rune) bool { return r == ' ' || r == ',' || r == '.' }) {
		if tm, ok := times[w]; ok {
			answer = append(answer, tm)
		}
	}
	if want := max(1, min(len(ss)-1, 3)); len(answer) != want {
		t.Fatalf("note names %d clocks, want %d: %q", len(answer), want, text)
	}
	_, got := g.click(ss[0], "safe")
	text, _ = g.answer(ss[0], got, "12:99")
	want(t, "wrong time", text, "The safe stays shut.")
	_, got = g.click(ss[0], "safe")
	text, _ = g.answer(ss[0], got, "0"+strings.Join(answer, ", "))
	want(t, "safe", text, "The safe opens", "WHARF 7")
	want(t, "opened", g.hear(ss[1]), "opened the safe!")
	g.click(ss[0], "door")
	for _, s := range ss[:2] {
		text, _ = g.click(s, "lane")
		want(t, "lane", text, "room:lane")
	}
	for _, s := range ss {
		g.hear(s)
	}
}

var mapRE = regexp.MustCompile(`map-([abc])-(\d)-(\d)\.png`)

var crates = map[string]string{"a": "K8", "b": "H3", "c": "S4"}

// level3 plays the wharf and the warehouse; ss[0] has the lantern.
func (g *caseGame) level3(ss []ws.Session) {
	t := g.t
	t.Helper()
	variant := ""
	for i, s := range ss {
		_, got := g.click(s, "board")
		img := lastOf(got, "image")
		if img == nil {
			t.Fatalf("no map piece: %s", caseText(got))
		}
		m := mapRE.FindStringSubmatch(img.URL)
		if m == nil || m[2] != strconv.Itoa(len(ss)) || m[3] != strconv.Itoa(i+1) {
			t.Fatalf("player %d's piece: %q", i+1, img.URL)
		}
		variant = m[1]
	}
	text, _ := g.click(ss[1], "warehouse")
	want(t, "dark", text, "pitch dark")
	text, _ = g.click(ss[1], "dark")
	want(t, "stumble", text, "You need light!")
	text, _ = g.click(ss[0], "warehouse")
	want(t, "light", text, "You raise the storm lantern.")
	want(t, "lit", g.hear(ss[1]), "The warehouse is lit!")
	for i, s := range ss[:2] {
		text, _ = g.click(s, "lift")
		if i == 0 && len(ss) > 1 {
			want(t, "lift", text, "It needs more weight!")
		}
	}
	want(t, "up", text, "the lift rises to the loft")
	g.hear(ss[0])
	_, got := g.click(ss[0], "crates")
	text, _ = g.answer(ss[0], got, "Z9")
	want(t, "wrong crate", text, "Only old fishing nets.")
	_, got = g.click(ss[0], "crates")
	code := crates[variant]
	text, _ = g.answer(ss[0], got, strings.ToLower(code[1:]+" "+code[:1]))
	want(t, "crate", text, "Inside crate "+code, "DIOGENES CLUB")
	for _, s := range ss[:2] {
		text, _ = g.click(s, "wharf")
		want(t, "wharf", text, "room:wharf")
	}
	for _, s := range ss {
		g.hear(s)
	}
}

var pegRE = regexp.MustCompile(`THE HAT OF (.+) GOES ON PEG (\d)|YOUR HAT GOES ON PEG (\d)`)

// level4 plays the Diogenes Club; names are the players' names.
func (g *caseGame) level4(ss []ws.Session, names []string) {
	t := g.t
	t.Helper()
	pegs := map[string]int{}
	for i, s := range ss {
		text, _ := g.click(s, "porter")
		m := pegRE.FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("porter: %q", text)
		}
		if m[3] != "" {
			n, _ := strconv.Atoi(m[3])
			pegs[names[i]] = n
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if strings.EqualFold(m[1], names[i]) {
			t.Fatalf("player %d got their own peg: %q", i+1, text)
		}
		pegs[strings.ToLower(m[1])] = n
	}
	if len(pegs) != len(ss) {
		t.Fatalf("pegs = %v", pegs)
	}
	// a wrong round first
	for i, s := range ss {
		peg := pegs[names[i]]
		if i == 0 {
			peg = peg%8 + 1
		}
		_, got := g.click(s, "pegs")
		text, _ := g.choose(s, got, peg)
		if i == len(ss)-1 {
			want(t, "wrong pegs", text, "Somebody's hat was on the wrong peg.")
		}
	}
	for i, s := range ss {
		_, got := g.click(s, "pegs")
		text, _ := g.choose(s, got, pegs[names[i]])
		if i == len(ss)-1 {
			want(t, "pegs", text, "The porter nods")
		}
	}
	text, _ := g.click(ss[0], "mycroft")
	want(t, "mycroft", text, "four evidence cards")
	for _, s := range ss {
		g.hear(s)
	}
}

// thiefFrom is the suspect no evidence card clears.
func thiefFrom(t *testing.T, cards string) int {
	t.Helper()
	cleared := map[int]bool{}
	for i, alibi := range []string{"Tobias Finch was", "Silas Crane was", "Mrs Pell was", "Mr Dunn rang"} {
		if strings.Contains(cards, alibi) {
			cleared[i+1] = true
		}
	}
	if len(cleared) != 3 {
		t.Fatalf("the cards clear %v: %q", cleared, cards)
	}
	for i := 1; i <= 4; i++ {
		if !cleared[i] {
			return i
		}
	}
	return 0
}

var cardRE = regexp.MustCompile(`Evidence card (\d): ([^|]+)`)

// level5 reads every player's cards and accuses together.
func (g *caseGame) level5(ss []ws.Session) {
	t := g.t
	t.Helper()
	all := ""
	cards := 0
	for _, s := range ss {
		text, _ := g.click(s, "satchel")
		n := len(cardRE.FindAllString(text, -1))
		if n == 0 {
			t.Fatalf("a player without cards: %q", text)
		}
		cards += n
		all += text
	}
	if cards != 4 {
		t.Fatalf("%d cards dealt, want 4", cards)
	}
	thief := thiefFrom(t, all)
	// vote until everyone agrees: votes from before count
	vote := func(pick int, until string) int {
		t.Helper()
		for i, s := range ss {
			_, got := g.click(s, "accuse")
			text, _ := g.choose(s, got, pick)
			if strings.Contains(text, until) {
				return i
			}
		}
		t.Fatalf("everyone voted %d, and no %q", pick, until)
		return 0
	}
	vote(thief%4+1, "It was not them!")
	text, _ := g.click(ss[0], "accuse")
	want(t, "checking", text, "Lestrade is still checking")
	g.c.Advance(31 * time.Second)
	for _, s := range ss {
		pending(t, s)
	}
	solver := vote(thief, "THE END.")
	for i, s := range ss {
		if i != solver {
			want(t, "everyone's end", g.hear(s), "THE END.")
		}
	}
}

func TestClockmakerTwoPlayers(t *testing.T) {
	g := newCase(t)
	a, first := g.join(ws.Who{})
	want(t, "a joins", first, "room:study", "Wiggins: Mr Holmes sent for us")
	b, _ := g.join(ws.Who{})
	text, _ := g.send(a, ws.Message{Type: "input", Text: "search the bookshelf"})
	want(t, "before the telegram", text, "Wiggins: First the telegram!")
	g.startCase(a, 2)
	want(t, "b hears", g.hear(b), "The case begins for 2 Irregulars")

	// one strip each, and only one
	text, _ = g.click(a, "violin")
	want(t, "no strip 3 for two", text, "Only Holmes's clutter.")
	g.level1([]ws.Session{a, b})
	text, _ = g.click(a, "slipper")
	want(t, "taken", text, "traveler 2 has that strip.")
	text, _ = g.click(a, "cupboard")
	want(t, "key taken", text, "traveler 2 has the brass key.")
	text, _ = g.click(a, "satchel")
	want(t, "a's satchel", text, "torn strip of paper (1)", "Holmes's magnifying glass", "Strip 1 of 2 reads:")

	// key holder first: b; then a in the workshop needs the key holder
	// first at the shop door: swap so a holds it.
	g.saveSpot([]ws.Session{b, a}, "cab", "lane")
	text, _ = g.click(b, "satchel")
	want(t, "carried on", text, "brass key to the clock shop")
	dontWant(t, "strips stay behind", text, "torn strip")

	g.level2([]ws.Session{b, a})
	g.saveSpot([]ws.Session{a, b}, "cab", "wharf")
	text, _ = g.click(b, "satchel")
	want(t, "lantern", text, "storm lantern", "brass key")
	g.level3([]ws.Session{b, a})
	g.saveSpot([]ws.Session{a, b}, "cab", "club")
	g.level4([]ws.Session{a, b}, []string{"traveler", "traveler 2"})
	g.saveSpot([]ws.Session{a, b}, "cab", "yard")
	g.level5([]ws.Session{a, b})

	// a player who comes later finds the end
	c, first := g.join(ws.Who{})
	want(t, "late", first, "room:yard", "THE END.")
	_ = c
}

func TestClockmakerFourPlayers(t *testing.T) {
	g := newCase(t)
	var ss []ws.Session
	for range 4 {
		s, _ := g.join(ws.Who{})
		ss = append(ss, s)
	}
	fifth, first := g.join(ws.Who{})
	want(t, "fifth", first, "room:pavement", "already has four Irregulars")
	_ = fifth
	g.startCase(ss[0], 4)
	g.level1(ss)
	// the strips came out as four parts of one word
	g.saveSpot([]ws.Session{ss[1], ss[2], ss[0], ss[3]}, "cab", "lane")
	g.level2([]ws.Session{ss[3], ss[0], ss[1], ss[2]})
	g.saveSpot(ss, "cab", "wharf")
	g.level3([]ws.Session{ss[3], ss[0], ss[1], ss[2]})
	g.saveSpot(ss, "cab", "club")
	g.level4(ss, []string{"traveler", "traveler 2", "traveler 3", "traveler 4"})
	g.saveSpot(ss, "cab", "yard")
	g.level5(ss)
}

func TestClockmakerThreePlayersAgreeToAccuse(t *testing.T) {
	g := newCase(t)
	var ss []ws.Session
	for range 3 {
		s, _ := g.join(ws.Who{})
		ss = append(ss, s)
	}
	g.startCase(ss[0], 3)
	g.level1(ss)
	g.saveSpot(ss, "cab", "lane")
	g.level2([]ws.Session{ss[2], ss[0], ss[1]})
	g.saveSpot(ss, "cab", "wharf")
	g.level3([]ws.Session{ss[2], ss[0], ss[1]})
	g.saveSpot(ss, "cab", "club")
	g.level4(ss, []string{"traveler", "traveler 2", "traveler 3"})
	g.saveSpot(ss, "cab", "yard")

	// two agree, one does not: nothing happens yet
	_, got := g.click(ss[0], "accuse")
	g.choose(ss[0], got, 1)
	_, got = g.click(ss[1], "accuse")
	g.choose(ss[1], got, 1)
	_, got = g.click(ss[2], "accuse")
	text, _ := g.choose(ss[2], got, 2)
	want(t, "split", text, "You do not agree yet.", "traveler: Tobias Finch", "traveler 3: Silas Crane")
	g.level5(ss)
}

func TestClockmakerSolo(t *testing.T) {
	g := newCase(t)
	a, _ := g.join(ws.Who{})
	g.startCase(a, 1)
	text, _ := g.click(a, "bookshelf")
	m := stripRE.FindStringSubmatch(text)
	if m == nil || m[2] != "1" {
		t.Fatalf("solo strip: %q", text)
	}
	_, got := g.click(a, "desk")
	text, _ = g.answer(a, got, m[3])
	want(t, "letter", text, "Turn the bust of Napoleon.")
	text, _ = g.click(a, "candlestick")
	want(t, "not named", text, "Nothing happens.")
	text, _ = g.click(a, "bust")
	want(t, "open", text, "a secret cupboard!")
	g.click(a, "cupboard")
	text, _ = g.click(a, "cab")
	want(t, "alone", text, "room:lane")
	g.click(a, "shopdoor")
	text, _ = g.click(a, "workshop")
	dontWant(t, "no lock for one", text, "locked in")
	text, _ = g.click(a, "note")
	want(t, "one clock", text, "the time of the clock with the ")
}

// The levers spring back when the others are too slow.
func TestClockmakerLeversSpringBack(t *testing.T) {
	g := newCase(t)
	a, _ := g.join(ws.Who{})
	b, _ := g.join(ws.Who{})
	g.startCase(a, 2)
	text, _ := g.click(a, "bookshelf")
	p1 := stripRE.FindStringSubmatch(text)[3]
	text, _ = g.click(b, "slipper")
	p2 := stripRE.FindStringSubmatch(text)[3]
	_, got := g.click(a, "desk")
	g.answer(a, got, p1+p2)
	text, _ = g.click(a, "bust")
	want(t, "hold", text, "Quick")
	text, _ = g.click(b, "bust")
	want(t, "held", text, "traveler is holding the bust of Napoleon already.")
	text, _ = g.click(a, "candlestick")
	want(t, "one at a time", text, "only hold one at a time")
	g.c.Advance(11 * time.Second)
	want(t, "sprang", caseText(pending(t, a))+caseText(pending(t, b)), "The levers spring back.")
	text, _ = g.click(b, "candlestick")
	want(t, "first again", text, "Quick")
	text, _ = g.click(a, "bust")
	want(t, "open", text, "a secret cupboard!")
}

// The lead goes on without a player who left; what they carried comes
// to the lead.
func TestClockmakerGoOnWithout(t *testing.T) {
	g := newCase(t)
	a, _ := g.join(ws.Who{})
	b, _ := g.join(ws.Who{})
	c, _ := g.join(ws.Who{})
	g.startCase(a, 3)
	g.level1([]ws.Session{a, b, c})
	// c took the key from the cupboard, and leaves
	c.Close()
	text, _ := g.click(b, "cab")
	want(t, "b waits", text, "Still to come: traveler, traveler 3.")
	if lastOf(pending(t, b), "dialog") != nil {
		t.Fatal("b is not the lead but was asked")
	}
	text, got := g.click(a, "cab")
	want(t, "lead asked", text, "Still to come: traveler 3.")
	d := lastOf(got, "dialog")
	if d == nil || !strings.Contains(d.Title, "traveler 3 is not here. Go on without them?") {
		t.Fatalf("dialog = %+v", d)
	}
	text, _ = g.choose(a, got, 1)
	want(t, "moved on", text, "room:lane")
	want(t, "b too", g.hear(b), "room:lane")
	text, _ = g.click(a, "satchel")
	want(t, "key came to the lead", text, "brass key to the clock shop")
	text, _ = g.click(a, "casebook")
	want(t, "casebook", text, "Made for 2.", "traveler (you) (lead)")
	dontWant(t, "seat freed", text, "traveler 3")
}

// The casebook lets the lead play the level again without who is away.
func TestClockmakerPlayAgainWithout(t *testing.T) {
	g := newCase(t)
	a, _ := g.join(ws.Who{})
	b, _ := g.join(ws.Who{})
	c, _ := g.join(ws.Who{})
	g.startCase(a, 3)
	text, _ := g.click(c, "bookshelf")
	want(t, "strip", text, "Strip 1 of 3")
	c.Close()
	_, got := g.click(b, "casebook")
	if d := lastOf(got, "dialog"); d == nil || len(d.Options) != 2 {
		t.Fatalf("b's casebook: %+v", d)
	}
	g.choose(b, got, 2)
	_, got = g.click(a, "casebook")
	d := lastOf(got, "dialog")
	if d == nil || len(d.Options) != 3 || d.Options[2] != "Play this level again without traveler 3" {
		t.Fatalf("lead's casebook: %+v", d)
	}
	text, _ = g.choose(a, got, 3)
	want(t, "again", text, "The level starts again", "room:study")
	want(t, "b moved", g.hear(b), "room:study")
	g.startCase(a, 2)
	text, _ = g.click(a, "bookshelf")
	want(t, "made for two", text, "Strip 1 of 2")
}

// storeCase is the pack on a SQLite store, as the server runs it.
func storeCase(t *testing.T, path string) (*caseGame, []ws.Who, func()) {
	t.Helper()
	st, world, who := openStore(t, path)
	g := newCase(t, WithStore(st, world.ID))
	return g, who, func() {
		g.w.Close()
		st.Close()
	}
}

// Everyone leaves in the middle of a level: coming back, even after a
// restart, the party starts the level again from its save spot, with
// what they carried there. One player leaving alone loses nothing.
func TestClockmakerSaveSpotAfterAllLeave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case.db")
	g, who, stop := storeCase(t, path)
	mira, _ := g.join(who[0])
	zed, _ := g.join(who[1])
	g.startCase(mira, 2)
	g.level1([]ws.Session{mira, zed}) // zed takes the key
	g.saveSpot([]ws.Session{mira, zed}, "cab", "lane")
	text, _ := g.click(zed, "shopdoor")
	want(t, "unlock", text, "room:shop")
	g.click(zed, "workshop")

	// Mira leaves alone and comes back: everything is as it was.
	mira.Close()
	mira, first := g.join(who[0])
	want(t, "mira back", first, "room:lane", "open door of the clock shop")
	text, _ = g.click(mira, "shopdoor")
	want(t, "still open", text, "room:shop")
	text, _ = g.click(mira, "workshop")
	want(t, "still locked in", text, "The workshop door is locked.", "Zed inside")

	// Both leave, and the server restarts.
	mira.Close()
	zed.Close()
	stop()
	g, who, stop = storeCase(t, path)
	defer stop()
	zed, first = g.join(who[1])
	want(t, "zed back", first, "room:lane", "locked door of the clock shop", "Who has the brass key")
	text, _ = g.click(zed, "satchel")
	want(t, "zed's satchel", text, "brass key to the clock shop")
	dontWant(t, "strips stayed behind", text, "torn strip")
	mira, first = g.join(who[0])
	want(t, "mira back again", first, "room:lane")
	text, _ = g.click(mira, "satchel")
	want(t, "mira's satchel", text, "Holmes's magnifying glass")
	text, _ = g.click(zed, "shopdoor")
	want(t, "unlock again", text, "The brass key turns.")
	text, _ = g.click(zed, "workshop")
	want(t, "workshop again", text, "You are locked in!")
}
