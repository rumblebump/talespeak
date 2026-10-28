package game

import (
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// world.finish (T-0018) where it meets the world's clocks (T-0012): a
// paused world ends, a game-time timer ends the world, and no timer
// fires after the end.

// finishRoom starts timers on both clocks and ends the world, typed or
// when the game-time room timer "turn" runs out.
const finishRoom = `
function on_enter(p) end
function on_input(p, text)
  if text == "timers" then
    timer.start("tick", 60, {game = true})
    timer.start("guards", 90)
    timer.start("turn", 60, {room = true, game = true})
    timer.start("bell", 90, {room = true})
  elseif text == "finish" then
    local a = world.finish("The end.")
    local b = world.finish("Another end.")
    say(tostring(a) .. " " .. tostring(b))
  end
end
function on_timer(p, name, kind)
  if name == "turn" then
    say("finish " .. tostring(world.finish("Time is up.")))
  else
    say("up " .. name)
  end
end`

// finishWorld is a world of finishRoom on a fake clock whose world.finish
// ends it and reports each ending text on the channel.
func finishWorld(t *testing.T) (*World, *fakeClock, chan string) {
	t.Helper()
	done := make(chan string, 4)
	var w *World
	w, c := clockWorld(t, finishRoom, `function on_enter() end`, WithFinish(func(text string) {
		w.End("This world has ended.")
		done <- text
	}))
	return w, c, done
}

func waitEnd(t *testing.T, done chan string) string {
	t.Helper()
	select {
	case text := <-done:
		return text
	case <-time.After(10 * time.Second):
		t.Fatal("the world did not end")
		return ""
	}
}

// endedWith returns what was pushed to s once its session ended.
func endedWith(t *testing.T, s ws.Session) []ws.Message {
	t.Helper()
	ms, ok := s.(ws.Pusher).Pending()
	if ok {
		t.Errorf("session still open after the end: %+v", ms)
	}
	return ms
}

// A game master ends a paused world: it ends once, with the first text,
// and none of its stopped timers fires later.
func TestWorldFinishWhilePaused(t *testing.T) {
	w, c, done := finishWorld(t)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	defer gm.Close()
	p, _ := w.NewSession("", ws.Who{})
	defer p.Close()
	input(p, "timers")
	c.Advance(10 * time.Second)
	input(gm, "/pause")
	pending(t, p)
	if got := texts(input(gm, "finish")); got != "true true" {
		t.Fatalf("finish = %q", got)
	}
	if text := waitEnd(t, done); text != "The end." {
		t.Errorf("ended with %q", text)
	}
	if !w.Closed() {
		t.Fatal("world still open")
	}
	ms := endedWith(t, p)
	if got := texts(ms); got != "The end. | error" || ms[1].Message != "This world has ended." {
		t.Errorf("the player's last messages: %+v", ms)
	}
	c.Advance(2 * time.Hour)
	if n := c.running(); n != 0 {
		t.Errorf("%d timers still armed after the end", n)
	}
	select {
	case text := <-done:
		t.Errorf("the world ended twice, again with %q", text)
	default:
	}
}

// A game-time room timer that ends the world waits out a pause and the
// time nobody plays, and then ends it from on_timer.
func TestWorldFinishFromGameTimer(t *testing.T) {
	w, c, done := finishWorld(t)
	p, _ := w.NewSession("", ws.Who{})
	input(p, "timers")
	c.Advance(30 * time.Second)
	gm, _ := w.NewSession("", ws.Who{GM: true})
	input(gm, "/pause")
	c.Advance(time.Hour)
	input(gm, "/resume")
	p.Close()
	gm.Close() // nobody plays: game time stands still, real time runs
	c.Advance(time.Hour)
	select {
	case text := <-done:
		t.Fatalf("ended while game time stood still: %q", text)
	default:
	}
	p, _ = w.NewSession("", ws.Who{})
	defer p.Close()
	c.Advance(29 * time.Second)
	select {
	case text := <-done:
		t.Fatalf("ended early: %q", text)
	default:
	}
	c.Advance(time.Second)
	if text := waitEnd(t, done); text != "Time is up." {
		t.Errorf("ended with %q", text)
	}
	// End replaces what waits in the queue (the hook's "finish true"
	// included) with the ending: only the ending is certain to arrive.
	got := texts(endedWith(t, p))
	if !strings.HasSuffix(got, "Time is up. | error") || strings.Contains(got, "up guards") {
		t.Errorf("the player's last messages: %q", got)
	}
	c.Advance(2 * time.Hour)
	if n := c.running(); n != 0 {
		t.Errorf("%d timers still armed after the end", n)
	}
}
