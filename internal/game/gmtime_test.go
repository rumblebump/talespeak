package game

import (
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// A player's extra time stretches their question timeouts and shown
// timers, not secret ones; a room timer takes the largest factor in the
// room; /mintime raises what is shorter, secret timers again excepted.
func TestExtraTimeAndMinTime(t *testing.T) {
	w, _, gm, p := timeWorld(t)
	if got := notices(input(gm, "/extratime traveler 2 1.5")); len(got) != 1 || !strings.Contains(got[0], "traveler 2 has 1.5 times as long") {
		t.Fatalf("/extratime: %v", got)
	}
	if got := ms(t, input(p, "start"), "guards"); got != 90000 {
		t.Errorf("a shown timer runs %d ms", got)
	}
	input(p, "secret")
	if got := texts(input(p, "left")); got != "90 60 nil" {
		t.Errorf("left = %s", got)
	}
	if got := ms(t, input(gm, "start"), "guards"); got != 60000 {
		t.Errorf("the game master's own timer runs %d ms", got)
	}
	got := input(p, "room")
	if m, ok := timerMsg(got, "room:turn"); !ok || m.Ms != 90000 {
		t.Errorf("a room timer: %+v", got)
	}
	got = input(p, "ask")
	if len(got) != 1 || got[0].Type != "dialog" || got[0].Ms != 45000 {
		t.Errorf("a question: %+v", got)
	}
	p.Handle(ws.Message{Type: "choose", ID: got[0].ID, Option: 1})

	if got := notices(input(gm, "/mintime 2m")); len(got) != 1 || !strings.Contains(got[0], "at least 2m") {
		t.Fatalf("/mintime: %v", got)
	}
	if got := ms(t, input(gm, "start"), "guards"); got != 120000 {
		t.Errorf("with /mintime 2m a shown timer runs %d ms", got)
	}
	input(gm, "secret")
	if got := texts(input(gm, "left")); got != "120 60 90" { // turn: the room timer started before
		t.Errorf("left = %s", got)
	}
	if got := notices(input(gm, "/mintime")); len(got) != 1 || !strings.Contains(got[0], "at least 2m") {
		t.Errorf("/mintime alone: %v", got)
	}
	input(gm, "/mintime off")
	if got := ms(t, input(gm, "start"), "guards"); got != 60000 {
		t.Errorf("with /mintime off a shown timer runs %d ms", got)
	}
	if got := strings.Join(notices(input(gm, "/extratime")), ""); !strings.Contains(got, "\ntraveler 1\ntraveler 2 1.5") {
		t.Errorf("/extratime alone: %q", got)
	}
	var actions []string
	for _, e := range w.auditLog {
		actions = append(actions, e.Action+" "+e.Detail)
	}
	if strings.Join(actions, "|") != "extra time traveler 2 1.5|mintime 2m|mintime off" {
		t.Errorf("audit = %q", actions)
	}
}

// What /extratime and /mintime refuse, without logging anything.
func TestTimeSettingsRefuse(t *testing.T) {
	w, _, gm, _ := timeWorld(t)
	for _, c := range []string{
		"/extratime traveler", "/extratime traveler 0.5", "/extratime traveler 3.5", "/extratime traveler NaN",
		"/extratime traveler two", "/extratime nobody 2",
		"/mintime 0", "/mintime 2h", "/mintime soon", "/mintime -30s",
	} {
		if got := notices(input(gm, c)); len(got) != 1 || strings.Contains(got[0], "has") || strings.Contains(got[0], "at least") {
			t.Errorf("%s: %v", c, got)
		}
	}
	if len(w.auditLog) != 0 {
		t.Errorf("logged: %+v", w.auditLog)
	}
}

func TestParseDuration(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"90", 90 * time.Second, true},
		{"30s", 30 * time.Second, true},
		{"5m", 5 * time.Minute, true},
		{"1h", time.Hour, true},
		{"1m30s", 90 * time.Second, true},
		{"1.5m", 90 * time.Second, true},
		{" 2M ", 2 * time.Minute, true},
		{"24h", 24 * time.Hour, true},
		{"1.0004s", 1000 * time.Millisecond, true},
		{"", 0, false},
		{"0", 0, false},
		{"0.5s", 0, false},
		{"-5m", 0, false},
		{"86401", 0, false},
		{"25h", 0, false},
		{"5 m", 0, false},
		{"soon", 0, false},
		{"999999999999999999999h", 0, false},
	} {
		got, ok := parseDuration(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseDuration(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDurationAndFactorText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0s", 400 * time.Millisecond: "0s", 45 * time.Second: "45s", 5 * time.Minute: "5m",
		90 * time.Second: "1m30s", 2*time.Hour + 5*time.Minute: "2h5m", -time.Second: "0s",
	} {
		if got := durationText(d); got != want {
			t.Errorf("durationText(%v) = %q, want %q", d, got, want)
		}
	}
	for in, want := range map[string]float64{"1": 1, "1.5": 1.5, "3": 3, "2.345": 2.35, "1e0": 1} {
		if got, ok := parseFactor(in); !ok || got != want {
			t.Errorf("parseFactor(%q) = %v, %v", in, got, ok)
		}
	}
	for _, in := range []string{"0.99", "3.01", "Inf", "NaN", "", "x", strings.Repeat("1", 30)} {
		if _, ok := parseFactor(in); ok {
			t.Errorf("parseFactor(%q) is accepted", in)
		}
	}
}

// FuzzParseDuration checks that what a game master types after /extend
// or /mintime is a time of 1 second to a day, or refused.
func FuzzParseDuration(f *testing.F) {
	for _, s := range []string{"90", "30s", "1m30s", "1.5h", "-1s", "9223372036854775807ns", "1e9h"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, ok := parseDuration(s)
		if ok && (d < time.Second || d > 24*time.Hour) {
			t.Errorf("parseDuration(%q) = %v", s, d)
		}
		if f, ok := parseFactor(s); ok && (f < 1 || f > 3) {
			t.Errorf("parseFactor(%q) = %v", s, f)
		}
	})
}
