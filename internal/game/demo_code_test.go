package game

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The demo's key box code (box_code in content/demo/lib/rooms.lua, T-0032)
// with the dice fixed: these tests pick the code instead of reading a
// random one, so codes with leading zeros and which room picked first can
// be checked every run.

// fakeRandom replaces math.random for box_code only: it returns the
// FAKE_CODE of the room script that asked, and fails the hook when the
// range is not the three digits 0 to 999.
const fakeRandom = `local math = { random = function(lo, hi)
  if lo ~= 0 or hi ~= 999 then
    error("box_code draws from " .. tostring(lo) .. " to " .. tostring(hi))
  end
  return FAKE_CODE
end }
`

// demoWithCodes is the shipped demo whose box_code draws corridor when the
// corridor picks the code (the note under the stone) and guard when the
// guard room does (the box). Everything else is the shipped pack.
func demoWithCodes(t testing.TB, corridor, guard int) *World {
	t.Helper()
	demo := content.Demo()
	fsys := fstest.MapFS{}
	err := fs.WalkDir(demo, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(demo, p)
		if err != nil {
			return err
		}
		fsys[p] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	lib := string(fsys["lib/rooms.lua"].Data)
	if !strings.Contains(lib, "math.random(0, 999)") {
		t.Fatal("lib/rooms.lua no longer draws the box code with math.random(0, 999): update fakeRandom")
	}
	fsys["lib/rooms.lua"].Data = []byte(fakeRandom + lib)
	for file, code := range map[string]int{"scripts/corridor.lua": corridor, "scripts/guardroom.lua": guard} {
		fsys[file].Data = append([]byte(fmt.Sprintf("FAKE_CODE = %d\n", code)), fsys[file].Data...)
	}
	p, err := pack.Load(fsys)
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

// spaced is code as the note shows it: "007" is "0 0 7".
func spaced(code string) string {
	return strings.Join(strings.Split(code, ""), " ")
}

// toGuardRoom takes a new player of w out of the cell and through the
// guards' door without lifting the stone, so the code is not read yet.
func toGuardRoom(t *testing.T, w *World) ws.Session {
	t.Helper()
	s, _ := w.NewSession("", ws.Who{})
	escapeCell(t, s)
	play(t, s, ws.Message{Type: "click", Item: "door"})
	if roomOf(s) != "guardroom" {
		t.Fatalf("not in the guard room: %q", roomOf(s))
	}
	return s
}

// tryBox clicks the box and answers the wheels' question with answer.
func tryBox(t *testing.T, s ws.Session, answer string) string {
	t.Helper()
	_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
	text, _ := play(t, s, ws.Message{Type: "answer", ID: question(t, got), Text: answer})
	return text
}

// A code with leading zeros keeps them: math.random gives a number, the
// note shows all three digits and the box wants all three, so "7" is not
// "007".
func TestDemoBoxCodeLeadingZeros(t *testing.T) {
	cases := []struct {
		drawn int
		code  string
	}{
		{0, "000"},
		{7, "007"},
		{70, "070"},
		{100, "100"},
		{999, "999"},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			w := demoWithCodes(t, c.drawn, c.drawn)
			s, _ := w.NewSession("", ws.Who{})
			defer s.Close()
			escapeCell(t, s)
			text, _ := play(t, s, ws.Message{Type: "click", Item: "stone"})
			wantText(t, "note", text, "BOX "+spaced(c.code))
			if got := noteCode(t, text); got != c.code {
				t.Fatalf("note shows %q, want %q", got, c.code)
			}
			text, _ = play(t, s, ws.Message{Type: "click", Item: "stone"})
			wantText(t, "stone again", text, "The note said: BOX "+spaced(c.code)+".")

			answers := map[string]bool{
				c.code:                  true,
				spaced(c.code):          true,
				"BOX " + spaced(c.code): true,
				strings.Join(strings.Split(c.code, ""), "-"): true,
				"0" + c.code:      false,
				c.code + "0":      false,
				c.code[1:]:        false,
				c.code[:2]:        false,
				otherCode(c.code): false,
			}
			if n := strconv.Itoa(c.drawn); n != c.code {
				answers[n] = false // the number without its zeros
			}
			reversed := []byte(c.code)
			slices.Reverse(reversed)
			if string(reversed) != c.code {
				answers[string(reversed)] = false
			}
			for answer, open := range answers {
				p := inGuardRoom(t, w) // a new player: the guard's countdown allows few tries
				text := tryBox(t, p, answer)
				p.Close()
				if opened := strings.Contains(text, "big iron key"); opened != open {
					t.Errorf("code %q, answer %q: opened %v, want %v: %q", c.code, answer, opened, open, text)
				}
			}
		})
	}
}

// Whoever needs the code first picks it, and it stays: a player paused at
// the box's question picked it, and the note another player reads later
// shows that one, not the one the corridor would have drawn; and the other
// way round.
func TestDemoBoxCodeFirstPickStays(t *testing.T) {
	t.Run("box first", func(t *testing.T) {
		w := demoWithCodes(t, 111, 222)
		a := toGuardRoom(t, w)
		defer a.Close()
		_, got := play(t, a, ws.Message{Type: "click", Item: "box"})
		id := question(t, got) // a waits at the wheels; the code is picked
		b, _ := w.NewSession("", ws.Who{})
		defer b.Close()
		escapeCell(t, b)
		text, _ := play(t, b, ws.Message{Type: "click", Item: "stone"})
		if code := noteCode(t, text); code != "222" {
			t.Fatalf("note after the box picked 222: %q", code)
		}
		text, _ = play(t, a, ws.Message{Type: "answer", ID: id, Text: "2 2 2"})
		wantText(t, "box", text, "big iron key")
	})
	t.Run("stone first", func(t *testing.T) {
		w := demoWithCodes(t, 111, 222)
		b, _ := w.NewSession("", ws.Who{})
		defer b.Close()
		escapeCell(t, b)
		text, _ := play(t, b, ws.Message{Type: "click", Item: "stone"})
		if code := noteCode(t, text); code != "111" {
			t.Fatalf("note: %q", code)
		}
		a := toGuardRoom(t, w)
		defer a.Close()
		wantText(t, "guard room's own draw", tryBox(t, a, "222"), "The box stays shut.")
		wantText(t, "the note's code", tryBox(t, a, "111"), "big iron key")
	})
}

// Many players of one world need the code at the same moment, some at the
// note and some at the box: they all get one code, and the box opens with
// it. With the dice fixed, the corridor and the guard room would draw
// different codes, so a second pick would show.
func TestDemoBoxCodeConcurrent(t *testing.T) {
	worlds := []struct {
		name  string
		world func(t *testing.T) *World
		codes []string // the codes the world may pick; nil for any
	}{
		{"fixed dice", func(t *testing.T) *World { return demoWithCodes(t, 111, 222) }, []string{"111", "222"}},
		{"shipped", shippedDemo, nil},
	}
	const each = 6
	for _, wc := range worlds {
		t.Run(wc.name, func(t *testing.T) {
			w := wc.world(t)
			var readers, boxers []ws.Session
			for range each {
				s, _ := w.NewSession("", ws.Who{})
				defer s.Close()
				escapeCell(t, s)
				readers = append(readers, s)
				b := toGuardRoom(t, w)
				defer b.Close()
				boxers = append(boxers, b)
			}
			start := make(chan struct{})
			notes := make([][]ws.Message, each)
			asks := make([][]ws.Message, each)
			var wg sync.WaitGroup
			for i := range each {
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					notes[i] = readers[i].Handle(ws.Message{Type: "click", Item: "stone"})
				}()
				go func() {
					defer wg.Done()
					<-start
					asks[i] = boxers[i].Handle(ws.Message{Type: "click", Item: "box"})
				}()
			}
			close(start)
			wg.Wait()

			code := ""
			for i, got := range notes {
				var texts []string
				for _, m := range got {
					if m.Type == "error" {
						t.Fatalf("reader %d: %+v", i, got)
					}
					texts = append(texts, m.Text)
				}
				c := noteCode(t, strings.Join(texts, "\n"))
				if code == "" {
					code = c
				}
				if c != code {
					t.Fatalf("reader %d read %q, another %q", i, c, code)
				}
			}
			if wc.codes != nil && !slices.Contains(wc.codes, code) {
				t.Fatalf("code %q, want one of %v", code, wc.codes)
			}
			for i, got := range asks {
				text, _ := play(t, boxers[i], ws.Message{Type: "answer", ID: question(t, got), Text: code})
				wantText(t, fmt.Sprintf("boxer %d", i), text, "big iron key")
			}
		})
	}
}

// refuseWorld makes w refuse state.world writes that grow it while the
// returned flag is set, as a full server does.
func refuseWorld(w *World) *atomic.Bool {
	var full atomic.Bool
	w.mu.Lock()
	defer w.mu.Unlock()
	reserve := w.world.Reserve
	w.world.Reserve = func(delta int64) error {
		if full.Load() && delta > 0 {
			return errors.New("full")
		}
		return reserve(delta)
	}
	return &full
}

// When the world cannot keep a new code, box_code says so and picks none:
// no note, no question at the box. Once the code is kept, a full world
// does not matter any more: box_code only reads it.
func TestDemoBoxCodeRefused(t *testing.T) {
	const refused = "(The game cannot remember that right now. Try again in a moment.)"
	t.Run("note", func(t *testing.T) {
		w := shippedDemo(t)
		full := refuseWorld(w)
		s, _ := w.NewSession("", ws.Who{})
		defer s.Close()
		escapeCell(t, s)
		full.Store(true)
		text, got := play(t, s, ws.Message{Type: "click", Item: "stone"})
		wantText(t, "refused", text, refused)
		if strings.Contains(text, "BOX") || strings.Contains(text, "You lift the stone") {
			t.Fatalf("stone lifted without a code: %q", text)
		}
		for _, m := range got {
			if m.Type == "items" {
				t.Fatalf("items changed without a code: %+v", got)
			}
		}
		w.mu.Lock()
		size := w.world.Size()
		w.mu.Unlock()
		if size != 0 {
			t.Fatalf("world state after the refused write: %d bytes", size)
		}
		full.Store(false)
		text, got = play(t, s, ws.Message{Type: "click", Item: "stone"})
		wantText(t, "again", text, "You lift the stone with the X.")
		code := noteCode(t, text)
		if items := lastItems(t, got); items["stone"] != "lifted stone" {
			t.Fatalf("items after the stone: %v", items)
		}
		full.Store(true)
		text, _ = play(t, s, ws.Message{Type: "click", Item: "stone"})
		wantText(t, "kept", text, "The note said: BOX "+spaced(code)+".")
		if strings.Contains(text, refused) {
			t.Fatalf("reading a kept code was refused: %q", text)
		}
	})
	t.Run("box", func(t *testing.T) {
		w := shippedDemo(t)
		full := refuseWorld(w)
		s := toGuardRoom(t, w)
		defer s.Close()
		full.Store(true)
		text, got := play(t, s, ws.Message{Type: "click", Item: "box"})
		wantText(t, "refused", text, refused)
		for _, m := range got {
			if m.Type == "ask" {
				t.Fatalf("the wheels asked without a code: %+v", got)
			}
		}
		full.Store(false)
		_, got = play(t, s, ws.Message{Type: "click", Item: "box"})
		id := question(t, got)
		full.Store(true) // the code is kept: a full world no longer matters
		code := boxCode(t, w)
		text, _ = play(t, s, ws.Message{Type: "answer", ID: id, Text: code})
		wantText(t, "box", text, "big iron key")
	})
}

// asciiDigits keeps the ASCII digits of s, as digits() in the guard room.
func asciiDigits(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Any answer at the wheels opens the box exactly when its ASCII digits are
// the code, leading zeros included; nothing a player types breaks the hook.
func FuzzDemoBoxAnswer(f *testing.F) {
	for _, seed := range []string{
		"007", "0 0 7", "BOX 0 0 7", "0-0-7", "7", "07", "0007", "0070", "700",
		"００７", "0\x000\x007", "\xff007\xfe", "0 0 7", " ", "",
		strings.Repeat("0", 500) + "7", "007" + strings.Repeat(" ", 498),
		"0٠٠7", "%d", "\\0\\0\\7",
	} {
		f.Add(seed)
	}
	const code = "007"
	w := demoWithCodes(f, 7, 7)
	f.Fuzz(func(t *testing.T, answer string) {
		s := inGuardRoom(t, w)
		defer s.Close()
		_, got := play(t, s, ws.Message{Type: "click", Item: "box"})
		got = s.Handle(ws.Message{Type: "answer", ID: question(t, got), Text: answer})
		var texts []string
		for _, m := range got {
			texts = append(texts, m.Text)
		}
		text := strings.Join(texts, "\n")
		opened := strings.Contains(text, "big iron key")
		clean := cleanInput(answer)
		taken := len(answer) <= MaxInputBytes && clean != "" && len(clean) <= script.MaxAnswerBytes
		if !taken {
			// Too long or blank: the question stays open, nothing ran.
			if !isError(got) || opened {
				t.Fatalf("answer %q not taken, got %+v", answer, got)
			}
			return
		}
		if isError(got) {
			t.Fatalf("answer %q: %+v", answer, got)
		}
		if want := asciiDigits(clean) == code; opened != want {
			t.Fatalf("answer %q: opened %v, want %v: %q", answer, opened, want, text)
		}
		if !opened {
			wantText(t, "shut", text, "The box stays shut.")
		}
	})
}
