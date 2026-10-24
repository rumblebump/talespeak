package game

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The world's clocks (T-0012). Timers count on one of two:
//
//   - the world clock, which is the wall clock less the time the world
//     spent paused: it stands still while a game master has paused the
//     world (/pause), also through a restart, and runs while nobody plays;
//   - the game clock (timer.start with game = true, world.time()), which
//     runs only while the world is not paused and a player is online.
//
// Deadlines are times on their timer's clock, so pausing stops every
// timer without rewriting one, and the store keeps them as they are. A
// timer is armed (a w.after on the wall clock) only while its clock runs;
// when its clock stops it is disarmed, and armed again when it starts.

// gameEpoch is where the game clock starts: a game-time deadline is this
// plus the game time it runs out at, so deadlines on both clocks are
// times and are stored the same way.
var gameEpoch = time.UnixMilli(0)

// noStop is the stop function of a timer that is not armed.
func noStop() bool { return false }

// clockNow is the time now on the game clock (game) or the world clock.
// The caller holds w.mu.
func (w *World) clockNow(game bool) time.Time {
	if game {
		return gameEpoch.Add(w.gameNow())
	}
	if !w.pausedAt.IsZero() {
		return w.pausedAt.Add(-w.pausedFor)
	}
	return w.now().Add(-w.pausedFor)
}

// gameNow is the world's game time. The caller holds w.mu.
func (w *World) gameNow() time.Duration {
	if !w.gameRuns {
		return w.gameTime
	}
	return w.gameTime + w.now().Sub(w.gameSince)
}

// running reports whether the game clock (game) or the world clock runs.
// The caller holds w.mu.
func (w *World) running(game bool) bool {
	if game {
		return w.gameRuns
	}
	return w.pausedAt.IsZero()
}

// arm schedules t to fire at its deadline while its clock runs; while it
// stands, t waits for it (resume, checkGame). The caller holds w.mu.
func (w *World) arm(t *timer) {
	if t.stop != nil {
		t.stop()
	}
	t.stop = noStop
	if !w.closed && w.running(t.game) {
		t.stop = w.after(max(0, t.deadline.Sub(w.clockNow(t.game))), t.fire)
	}
}

// disarm stops t until its clock runs again. The caller holds w.mu.
func (w *World) disarm(t *timer) {
	if t.stop != nil {
		t.stop()
	}
	t.stop = noStop
}

// ready reports whether t, about to fire, is due: its clock runs and its
// deadline came. A call that came early (the deadline moved, /extend) arms
// it again. The caller holds w.mu.
func (w *World) ready(t *timer) bool {
	if !w.running(t.game) {
		return false
	}
	if t.deadline.After(w.clockNow(t.game)) {
		w.arm(t)
		return false
	}
	return true
}

// eachTimer calls f for every timer of the world that waits on a clock,
// in a fixed order: each player's (the earliest to join first) with
// their open question's timeout, then the rooms'. A room timer that ran
// out in an empty room waits for a player, not for a clock, and is left
// out. The caller holds w.mu.
func (w *World) eachTimer(f func(*timer)) {
	list := slices.SortedFunc(maps.Keys(w.sessions), func(a, b *session) int { return cmp.Compare(a.joined, b.joined) })
	for _, s := range list {
		for _, name := range slices.Sorted(maps.Keys(s.timers)) {
			f(s.timers[name])
		}
		if s.dialogTimer != nil {
			f(s.dialogTimer)
		}
	}
	for _, room := range slices.Sorted(maps.Keys(w.roomTimers)) {
		for _, name := range slices.Sorted(maps.Keys(w.roomTimers[room])) {
			if t := w.roomTimers[room][name]; !t.due {
				f(&t.timer)
			}
		}
	}
}

// checkGame starts the game clock when the world runs and a player is
// online, and stops it otherwise, arming or disarming the game-time
// timers. The caller holds w.mu.
func (w *World) checkGame() {
	want := w.pausedAt.IsZero() && !w.closed && w.anyOnline()
	if want == w.gameRuns {
		return
	}
	now := w.now()
	if want {
		w.gameSince, w.gameRuns = now, true
	} else {
		w.gameTime += now.Sub(w.gameSince)
		w.gameRuns = false
	}
	w.clockDirty = true
	w.eachTimer(func(t *timer) {
		if t.game {
			w.arm(t)
		}
	})
}

// anyOnline reports whether a player is in the world. The caller holds
// w.mu.
func (w *World) anyOnline() bool {
	for p := range w.sessions {
		if !p.closed && !p.leaving {
			return true
		}
	}
	return false
}

// pause stops the world's clocks and every timer with its time left.
// The caller holds w.mu.
func (w *World) pause() {
	w.pausedAt = w.now()
	w.clockDirty = true
	w.checkGame()
	w.eachTimer(w.disarm)
}

// resume starts the world's clocks again: every timer goes on with the
// time it had left. The caller holds w.mu.
func (w *World) resume() {
	w.pausedFor += w.now().Sub(w.pausedAt)
	w.pausedAt = time.Time{}
	w.clockDirty = true
	w.checkGame()
	w.eachTimer(func(t *timer) {
		if !t.game {
			w.arm(t)
		}
	})
}

// clockState is the world's time as the store keeps it. The caller holds
// w.mu.
func (w *World) clockState() store.Clock {
	return store.Clock{PausedAt: w.pausedAt, PausedFor: w.pausedFor, Game: w.gameNow(), MinTime: w.minTime}
}

// saveClock adds the world's time to sv when it changed, or when the game
// clock runs and sv writes anyway, so a crash loses little game time.
// The caller holds w.mu.
func (w *World) saveClock(sv *store.Save) {
	if w.clockDirty || (w.gameRuns && !sv.Empty()) {
		c := w.clockState()
		sv.Clock = &c
	}
}

// loadClock reads the world's time from the store as the world loads.
func (w *World) loadClock(ctx context.Context) error {
	c, err := w.store.WorldClock(ctx, w.worldID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	w.pausedAt, w.pausedFor, w.gameTime = c.PausedAt, max(0, c.PausedFor), max(0, c.Game)
	w.minTime = min(max(0, c.MinTime), MaxMinTime)
	return nil
}

// writeClock writes the world's time as the world closes, so the game
// time since the last save lasts. The caller holds w.mu.
func (w *World) writeClock() {
	if w.store == nil || !w.clockDirty {
		return
	}
	c := w.clockState()
	ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
	defer cancel()
	if err := w.store.Save(ctx, store.Save{World: w.worldID, Clock: &c}); err != nil {
		w.log.Error("saving the world's time failed", "world", w.worldID, "err", err)
		return
	}
	w.clockDirty = false
}

// Paused and GameTime implement script.Clock.
func (s *session) Paused() bool { return !s.w.pausedAt.IsZero() }

func (s *session) GameTime() time.Duration { return s.w.gameNow() }

// pauseMessage tells the client whether the world is paused: its
// countdowns stand still until it runs again.
func pauseMessage(paused bool) ws.Message {
	return ws.Message{Type: "pause", Paused: paused}
}

// pausedText answers a player who acts while the world is paused.
const pausedText = "The game is paused."

// held reports whether the player must wait for the world to run again:
// it is paused, and they are not a game master in GM mode. The caller
// holds w.mu.
func (s *session) held() bool {
	return !s.w.pausedAt.IsZero() && !s.gm
}

// Extra time and the minimum deadline (T-0012).

// MaxMinTime is the longest /mintime: a world cannot make every question
// wait longer than this.
const MaxMinTime = time.Hour

// shownTime is how long a question or a shown timer asked to run d runs
// for players whose largest extra-time factor is factor: stretched by
// it, at least the world's /mintime, and at most a timer's longest run.
// The caller holds w.mu.
func (w *World) shownTime(d time.Duration, factor float64) time.Duration {
	if factor > 1 {
		d = time.Duration(float64(d) * min(factor, store.MaxExtraTime))
	}
	return min(max(d, w.minTime), script.MaxTimerSeconds*time.Second)
}

// roomFactor is the largest extra-time factor of the players in room.
// The caller holds w.mu.
func (w *World) roomFactor(room string) float64 {
	f := 1.0
	for _, p := range w.inRoom(room) {
		f = max(f, p.extra)
	}
	return f
}
