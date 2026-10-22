package game

import (
	"slices"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/ws"
)

var partyFuzzInputs = []string{"start x", "secret x", "cancel x", "left x", "start y", "fill",
	"go b", "go a", "ask", "list", "fail x", "who"}

// FuzzPartyOps runs random sequences of room timer commands, moves,
// questions, joins, leaves, clock steps and used-up Lua time on three
// players in two rooms (edgeParty, whose on_leave writes state.world or
// starts a room timer). Whatever the order: no panic, known messages,
// players have unique ids and names, at most MaxRoomTimers per room and no
// empty timer maps, and once everyone left and the clock ran on, no room
// timer and no clock timer is left. world.players lists the players in
// the order they (last) joined, with player 1 as the GM (T-0036).
func FuzzPartyOps(f *testing.F) {
	f.Add([]byte{0, 32, 64, 13, 6, 38, 14, 15, 18, 19})
	f.Add([]byte{5, 37, 6, 16, 17, 16, 18, 69, 19})
	f.Add([]byte{0, 20, 13, 21, 15, 38, 50, 18})
	f.Add([]byte{8, 40, 72, 0, 18, 12, 44, 76, 20, 17})
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 200 {
			ops = ops[:200]
		}
		w, c := edgeWorld(t, WithClientBudget(time.Second, time.Second))
		clients := []string{"", "192.0.2.1", "192.0.2.2"}
		players := make([]*fuzzPlayer, 3)
		var order []ws.Session // by join
		join := func(i int) *fuzzPlayer {
			s, first := w.NewSession(clients[i], ws.Who{GM: i == 1})
			p := &fuzzPlayer{s: s, shown: map[string]bool{}}
			p.see(t, first)
			order = append(order, s)
			return p
		}
		for i := range players {
			players[i] = join(i)
		}
		for _, op := range ops {
			i := int(op>>5) % len(players)
			p := players[i]
			var got []ws.Message
			switch code := int(op & 31); {
			case code < len(partyFuzzInputs):
				got = p.s.Handle(ws.Message{Type: "input", Text: partyFuzzInputs[code]})
			case code == 12:
				got = p.s.Handle(ws.Message{Type: "choose", ID: p.open, Option: 1})
			case code == 13:
				c.Advance(DeliverGap)
			case code == 14:
				c.Advance(10 * time.Second)
			case code == 15:
				c.Advance(30 * time.Second)
			case code == 16:
				c.Advance(retryBusy)
			case code == 17:
				if clients[i] != "" { // the player's address uses up its Lua time
					w.mu.Lock()
					if b := w.budgets[clients[i]]; b != nil {
						b.left = -1
					}
					w.mu.Unlock()
				}
			case code == 18:
				p.s.Close()
				order = slices.DeleteFunc(order, func(s ws.Session) bool { return s == p.s })
				players[i] = join(i)
			default:
				c.Advance(0)
			}
			p.see(t, got)
			for _, q := range players {
				ms, ok := q.s.(ws.Pusher).Pending()
				if !ok {
					t.Fatal("a player lagged")
				}
				q.see(t, ms)
			}
			checkParty(t, w)
			checkWorldPlayers(t, w, order, players[1].s)
		}
		for _, p := range players {
			p.s.Close()
		}
		c.Advance(time.Hour)
		c.Advance(time.Hour)
		w.mu.Lock()
		rooms := len(w.roomTimers)
		w.mu.Unlock()
		if rooms != 0 {
			t.Fatalf("%d rooms with timers after everyone left", rooms)
		}
		if n := c.running(); n != 0 {
			t.Fatalf("%d clock timers after everyone left", n)
		}
	})
}

// checkParty checks what holds between any two messages: unique ids and
// names, room.players order by arrival, and room timer limits.
func checkParty(t *testing.T, w *World) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	ids, names := map[string]bool{}, map[string]bool{}
	for s := range w.sessions {
		if ids[s.id] || names[s.name] {
			t.Fatalf("id %q or name %q twice", s.id, s.name)
		}
		ids[s.id], names[s.name] = true, true
		if s.closed || s.leaving {
			t.Fatalf("%s listed while closed or leaving", s.id)
		}
	}
	for _, room := range []string{"a", "b"} {
		list := w.inRoom(room)
		for j := 1; j < len(list); j++ {
			if list[j-1].arrived >= list[j].arrived {
				t.Fatalf("room %s: arrivals out of order", room)
			}
		}
	}
	for room, ts := range w.roomTimers {
		if len(ts) == 0 || len(ts) > MaxRoomTimers {
			t.Fatalf("room %s has %d timers", room, len(ts))
		}
	}
}

// checkWorldPlayers checks that world.players lists order, each player's
// room, and gm only for the GM's session.
func checkWorldPlayers(t *testing.T, w *World, order []ws.Session, gm ws.Session) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	online := w.online()
	list := players(online)
	if len(online) != len(order) {
		t.Fatalf("world.players lists %d, %d joined", len(online), len(order))
	}
	for i, s := range online {
		if ws.Session(s) != order[i] {
			t.Fatalf("world.players[%d] = %s, out of join order", i, s.id)
		}
		if list[i].ID != s.id || list[i].Room != s.room || list[i].GM != (ws.Session(s) == gm) {
			t.Fatalf("world.players[%d] = %+v for %s in %s", i, list[i], s.id, s.room)
		}
	}
}
