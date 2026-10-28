package game

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// timeRoom starts every kind of timer, asks with a timeout, and tells the
// world's time (T-0012).
const timeRoom = `
function on_enter(p) say("in a") end
function on_input(p, text)
  if text == "start" then timer.start("guards", 60, {show = "Guards"})
  elseif text == "secret" then timer.start("hush", 60)
  elseif text == "game" then timer.start("tick", 60, {game = true, show = "Tick"})
  elseif text == "room" then timer.start("turn", 60, {room = true, show = "Turn"})
  elseif text == "sand" then timer.start("sand", 60, {room = true, game = true})
  elseif text == "ask" then
    local n = ui.dialog("Fight or flee?", {"Fight", "Flee"}, {timeout = 30})
    say("answer " .. tostring(n))
  elseif text == "time" then say(tostring(math.floor(world.time())))
  elseif text == "paused" then say(tostring(world.paused()))
  elseif text == "go" then room.go("b")
  elseif text == "left" then
    say(tostring(timer.left("guards")) .. " " .. tostring(timer.left("hush")) .. " " .. tostring(timer.left("turn", {room = true})))
  end
end
function on_click(p, id) say("clicked " .. id) end
function on_command(p, name, args) say("command " .. name) end
commands = { wave = { help = "wave" } }
function on_timer(p, name, kind) say("up " .. name .. " " .. tostring(kind)) end`

// timeWorld is a world of timeRoom on a fake clock, with a game master
// and a player in room a.
func timeWorld(t *testing.T) (w *World, c *fakeClock, gm, p ws.Session) {
	t.Helper()
	w, c = clockWorld(t, timeRoom, timerRoomB)
	gm, _ = w.NewSession("", ws.Who{GM: true})
	p, _ = w.NewSession("", ws.Who{})
	t.Cleanup(func() { p.Close(); gm.Close() })
	return w, c, gm, p
}

// ms finds the timer message with id in ms and returns its time left.
func ms(t *testing.T, list []ws.Message, id string) int64 {
	t.Helper()
	m, ok := timerMsg(list, id)
	if !ok {
		t.Fatalf("no timer %q in %+v", id, list)
	}
	return m.Ms
}

// A pause stops every timer, room timer and question timeout with its
// time left; resuming sends the countdowns again and they run out on time.
func TestPauseFreezesEveryTimer(t *testing.T) {
	w, c, gm, p := timeWorld(t)
	input(p, "start")
	input(p, "room")
	input(p, "ask")
	c.Advance(10 * time.Second)
	pending(t, gm)
	pending(t, p)

	got := input(gm, "/pause")
	if len(got) < 2 || got[0].Type != "pause" || !got[0].Paused || got[1].Type != "notice" || got[1].Role != NoticeGM {
		t.Fatalf("/pause answers %+v", got)
	}
	pushed := pending(t, p)
	if len(pushed) != 2 || pushed[0].Type != "pause" || !pushed[0].Paused || !strings.Contains(pushed[1].Text, "paused the game") {
		t.Fatalf("the player heard %+v", pushed)
	}
	c.Advance(time.Hour)
	if got := pending(t, p); len(got) != 0 {
		t.Fatalf("while paused: %+v", got)
	}
	if got := texts(input(gm, "paused")); got != "true" {
		t.Errorf("world.paused() = %s", got)
	}

	got = input(gm, "/resume")
	if len(got) == 0 || got[0].Type != "pause" || got[0].Paused {
		t.Fatalf("/resume answers %+v", got)
	}
	pushed = pending(t, p)
	if pushed[0].Type != "pause" || pushed[0].Paused {
		t.Fatalf("resume reached the player as %+v", pushed)
	}
	if ms(t, pushed, "guards") != 50000 || ms(t, pushed, "room:turn") != 50000 || ms(t, pushed, "question:d1") != 20000 {
		t.Errorf("countdowns after resume: %+v", pushed)
	}
	if got := texts(input(gm, "paused")); got != "false" {
		t.Errorf("world.paused() after resume = %s", got)
	}
	c.Advance(19 * time.Second)
	if got := pending(t, p); len(got) != 0 {
		t.Fatalf("before the question's deadline: %+v", got)
	}
	c.Advance(time.Second)
	if got := texts(pending(t, p)); got != "close | answer nil" {
		t.Errorf("the question timed out with %q", got)
	}
	c.Advance(30 * time.Second)
	got = pending(t, p)
	if s := texts(got); !strings.Contains(s, "up guards nil") || !strings.Contains(s, "up turn room") {
		t.Errorf("at the deadline: %q", s)
	}
	_ = w
}

// While the world is paused, players' lines, clicks, answers and room
// commands wait; a game master in GM mode still acts, one playing as a
// player waits too; /help still works.
func TestPausedPlayersWait(t *testing.T) {
	w, _, gm, p := timeWorld(t)
	asPlayer, _ := w.NewSession("", ws.Who{GM: true, AsPlayer: true})
	defer asPlayer.Close()
	input(p, "ask")
	input(gm, "/pause")
	pending(t, p)
	pending(t, asPlayer)
	for _, s := range []ws.Session{p, asPlayer} {
		if got := notices(input(s, "left")); len(got) != 1 || got[0] != "server: "+pausedText {
			t.Errorf("a typed line: %v", got)
		}
		if got := notices(input(s, "/help")); len(got) != 1 || !strings.HasPrefix(got[0], "server: You can type") {
			t.Errorf("/help: %v", got)
		}
	}
	if got := notices(input(asPlayer, "/wave")); len(got) != 1 || got[0] != "server: "+pausedText {
		t.Errorf("a room command: %v", got)
	}
	if got := notices(asPlayer.Handle(ws.Message{Type: "click", Item: "box"})); len(got) != 1 || got[0] != "server: "+pausedText {
		t.Errorf("a click: %v", got)
	}
	got := p.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})
	if len(got) != 1 || got[0].Type != "error" || got[0].ID != "d1" || got[0].Message != pausedText {
		t.Errorf("an answer: %+v", got)
	}
	if got := texts(gm.Handle(ws.Message{Type: "click", Item: "box"})); got != "clicked box" {
		t.Errorf("the game master's click: %q", got)
	}
	if got := texts(input(gm, "/wave")); !strings.Contains(got, "command wave") {
		t.Errorf("the game master's command: %q", got)
	}
	input(gm, "/resume")
	pending(t, p)
	if got := texts(p.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "answer 1" {
		t.Errorf("after resume the answer counts: %q", got)
	}
}

// Pausing twice, resuming a running world and the commands' audit
// entries; a player without GM mode has no such commands.
func TestPauseCommandRules(t *testing.T) {
	w, _, gm, p := timeWorld(t)
	if got := notices(input(gm, "/resume")); len(got) != 1 || !strings.Contains(got[0], "not paused") {
		t.Errorf("resume a running world: %v", got)
	}
	input(gm, "/pause")
	if got := notices(input(gm, "/pause")); len(got) != 1 || !strings.Contains(got[0], "paused already") {
		t.Errorf("pause twice: %v", got)
	}
	input(gm, "/resume")
	pending(t, p)
	for _, name := range []string{"/pause", "/resume", "/timers", "/extend 1 5m", "/cancel 1", "/extratime x 2", "/mintime 30s"} {
		if got := notices(input(p, name)); len(got) != 1 || !strings.Contains(got[0], "There is no command") {
			t.Errorf("%s for a player: %v", name, got)
		}
	}
	var actions []string
	for _, e := range w.auditLog {
		actions = append(actions, e.Action)
	}
	if !slices.Equal(actions, []string{"pause", "resume"}) {
		t.Errorf("audit = %v", actions)
	}
	help := strings.Join(notices(input(gm, "/help")), "\n")
	for _, c := range []string{"/pause", "/resume", "/timers", "/extend N TIME", "/cancel N", "/extratime", "/mintime"} {
		if !strings.Contains(help, c) {
			t.Errorf("/help lacks %s", c)
		}
	}
}

// Game time runs only while the world runs and a player is online; a
// game-time timer counts it, a real-time one counts on while nobody plays.
func TestGameTime(t *testing.T) {
	w, c := clockWorld(t, timeRoom, timerRoomB)
	p, _ := w.NewSession("", ws.Who{})
	if got := ms(t, input(p, "game"), "tick"); got != 60000 {
		t.Errorf("a game timer shows %d ms", got)
	}
	input(p, "sand")
	c.Advance(30 * time.Second)
	if got := texts(input(p, "time")); got != "30" {
		t.Errorf("world.time() = %s", got)
	}
	p.Close() // nobody plays: game time stands still
	c.Advance(time.Hour)
	p2, _ := w.NewSession("", ws.Who{})
	defer p2.Close()
	if got := texts(input(p2, "time")); got != "30" {
		t.Errorf("world.time() after an hour without players = %s", got)
	}
	c.Advance(29 * time.Second)
	if got := pending(t, p2); len(got) != 0 {
		t.Fatalf("too early: %+v", got)
	}
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	input(gm, "/pause")
	c.Advance(time.Hour)
	input(gm, "/resume")
	pending(t, p2)
	if got := texts(input(p2, "time")); got != "59" {
		t.Errorf("world.time() after a pause = %s", got)
	}
	c.Advance(time.Second)
	if got := texts(pending(t, p2)); got != "up sand room" {
		t.Errorf("the game-time room timer: %q", got)
	}
}

// A shown game-time timer comes back with its game time left when the
// client gets the countdowns again.
func TestGameTimerLeftAfterResume(t *testing.T) {
	_, c, gm, p := timeWorld(t)
	input(p, "game")
	c.Advance(20 * time.Second)
	input(gm, "/pause")
	c.Advance(time.Hour)
	input(gm, "/resume")
	if got := ms(t, pending(t, p), "tick"); got != 40000 {
		t.Errorf("tick has %d ms left", got)
	}
	c.Advance(40 * time.Second)
	if got := texts(pending(t, p)); got != "timer | up tick nil" {
		t.Errorf("at the deadline: %q", got)
	}
}

// A player who joins a paused world hears so first, and its timers do
// not run until the world resumes.
func TestJoinPausedWorld(t *testing.T) {
	w, c, gm, _ := timeWorld(t)
	input(gm, "/pause")
	p, first := w.NewSession("", ws.Who{})
	defer p.Close()
	if len(first) == 0 || first[0].Type != "pause" || !first[0].Paused {
		t.Fatalf("joining a paused world: %+v", first)
	}
	if got := texts(input(gm, "start")); got != "timer" {
		t.Errorf("the game master starts a timer: %q", got)
	}
	c.Advance(time.Hour)
	if got := texts(pending(t, gm)); got != "" {
		t.Errorf("a timer ran while paused: %q", got)
	}
	input(gm, "/resume")
	c.Advance(time.Minute)
	if got := texts(pending(t, gm)); got != "timer | up guards nil" {
		t.Errorf("after resume: %q", got)
	}
}

// A timer callback that comes early (its deadline moved, or it was
// already on its way when the world paused) does not fire the timer: it
// arms it again, and the timer fires once, on time.
func TestEarlyFireWaits(t *testing.T) {
	w, c, gm, p := timeWorld(t)
	input(p, "start")
	sess := p.(*session)
	w.mu.Lock()
	tm := sess.timers["guards"]
	w.mu.Unlock()
	sess.fire("guards", tm) // a minute early
	if got := pending(t, p); len(got) != 0 {
		t.Fatalf("fired early: %+v", got)
	}
	input(gm, "/pause")
	pending(t, p)
	c.Advance(time.Minute)
	sess.fire("guards", tm) // on its way as the world paused
	if got := pending(t, p); len(got) != 0 {
		t.Fatalf("fired while paused: %+v", got)
	}
	input(gm, "/resume")
	pending(t, p)
	c.Advance(time.Minute)
	if got := texts(pending(t, p)); got != "timer | up guards nil" {
		t.Errorf("at the deadline: %q", got)
	}
}
