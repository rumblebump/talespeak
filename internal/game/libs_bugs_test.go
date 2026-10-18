package game

// Failing tests for bugs found by the test-engineer (T-0019, T-0020).
// Drop into internal/game/ next to libs_edge_test.go.

import (
	"errors"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

// An item the world could not keep for the player is not lost: the next
// take tries again. This is the each rule; split, once and count change
// state.world before the player's inventory in the same way.
func TestItemsTakeBusyEach(t *testing.T) {
	_, _, ss := itemPlayers(t, 1)
	sess := ss[0].(*session)
	st := &sess.state
	reserve := st.Reserve
	grows := 0
	st.Reserve = func(delta int64) error {
		if delta > 0 {
			grows++
			if grows == 2 { // the second write of this take: the inventory
				return errors.New("full")
			}
		}
		return reserve(delta)
	}
	runSteps(t, ss, []step{{0, "take torch", "no busy nil"}})
	st.Reserve = reserve
	runSteps(t, ss, []step{{0, "take torch", "got 1 torch"}})
}

// round.status names a skipped player only on the turn after the skip,
// not for the rest of the round.
func TestRoundSkippedNoteGoesAway(t *testing.T) {
	_, c, ss := roundPlayers(t, 3)
	input(ss[0], "mode skip")
	input(ss[0], "timed")
	c.Advance(30 * time.Second) // p1 took too long: p2 acts
	heard(t, c, ss[1])
	input(ss[1], "done") // p3 acts
	heard(t, c, ss[2])
	if got := texts(input(ss[2], "status")); got != "Round 1. Order: traveler, traveler 2, traveler 3 (you) (acting)." {
		t.Errorf("status two turns after the skip = %q", got)
	}
}

// A blessing with only seconds wears off when its timer runs out, even
// if the player is in a room without the item library then.
func TestItemsEffectEndsInOtherRoom(t *testing.T) {
	_, c, ss := itemPlayers(t, 1)
	input(ss[0], "bless seconds")
	input(ss[0], "go b")
	c.Advance(10 * time.Second)
	pending(t, ss[0]) // "b timer fx_glow"
	input(ss[0], "back")
	// It wears off when asked, with on_effect_end.
	runSteps(t, []ws.Session{ss[0]}, []step{{0, "blessed", "effect ends glow | false"}, {0, "blessed", "false"}})
}
