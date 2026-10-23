package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// Timers are written with a save, replaced by name, deleted, and read
// back with their deadlines to the millisecond.
func TestTimersSaveAndRead(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	c, err := s.Character(ctx, w.ID, ann.ID, "Ann")
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(time.Now().UnixMilli()).Add(time.Minute)
	sv := store.Save{World: w.ID, Timers: []store.Timer{
		{Kind: store.TimerPlayer, Character: c.ID, Name: "guards", Deadline: at, Show: "Guards"},
		{Kind: store.TimerQuestion, Character: c.ID, Room: "cell", Name: "0123456789abcdef0123456789abcdef", Deadline: at},
		{Kind: store.TimerRoom, Character: c.ID, Room: "hall", Name: "turn", Deadline: at, ReadOnly: true},
		{Kind: store.TimerRoom, Room: "hall", Name: "bell", Deadline: at},
	}}
	if sv.Empty() {
		t.Fatal("a save of timers only is empty")
	}
	if err := s.Save(ctx, sv); err != nil {
		t.Fatal(err)
	}
	list, err := s.PlayerTimers(ctx, c.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("player timers = %+v, %v", list, err)
	}
	if q, p := list[0], list[1]; q.Kind != store.TimerQuestion || q.Room != "cell" || p.Name != "guards" || p.Show != "Guards" ||
		!p.Deadline.Equal(at) || p.Character != c.ID {
		t.Errorf("player timers = %+v", list)
	}
	rooms, err := s.RoomTimers(ctx, w.ID)
	if err != nil || len(rooms) != 2 {
		t.Fatalf("room timers = %+v, %v", rooms, err)
	}
	if b, tu := rooms[0], rooms[1]; b.Name != "bell" || b.Character != 0 || tu.Name != "turn" || tu.Character != c.ID || !tu.ReadOnly || !tu.Deadline.Equal(at) {
		t.Errorf("room timers = %+v", rooms)
	}

	later := at.Add(time.Hour)
	if err := s.Save(ctx, store.Save{World: w.ID, Timers: []store.Timer{
		{Kind: store.TimerPlayer, Character: c.ID, Name: "guards", Deadline: later},
		{Kind: store.TimerQuestion, Character: c.ID, Room: "cell", Name: "0123456789abcdef0123456789abcdef", Delete: true},
		{Kind: store.TimerRoom, Room: "hall", Name: "bell", Delete: true},
	}}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.PlayerTimers(ctx, c.ID)
	if len(list) != 1 || !list[0].Deadline.Equal(later) || list[0].Show != "" {
		t.Errorf("after replacing and deleting: %+v", list)
	}
	if rooms, _ = s.RoomTimers(ctx, w.ID); len(rooms) != 1 || rooms[0].Name != "turn" {
		t.Errorf("room timers after deleting: %+v", rooms)
	}
	if err := s.Save(ctx, store.Save{World: w.ID, Timers: []store.Timer{{Kind: "alarm", Character: c.ID, Name: "x"}}}); err == nil {
		t.Error("an unknown kind was saved")
	}
}

// A save of one world never writes or deletes the timers of another
// world's characters, and a room timer's starter is only one of its own.
func TestTimersStayInTheirWorld(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w1, _ := s.OpenWorld(ctx, "demo", "", nil)
	w2, _ := s.OpenWorld(ctx, "other", "", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	c1, _ := s.Character(ctx, w1.ID, ann.ID, "Ann")
	c2, _ := s.Character(ctx, w2.ID, ann.ID, "Ann")
	at := time.Now().Add(time.Minute)
	if err := s.Save(ctx, store.Save{World: w2.ID, Timers: []store.Timer{{Kind: store.TimerPlayer, Character: c2.ID, Name: "mine", Deadline: at}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, store.Save{World: w1.ID, Timers: []store.Timer{
		{Kind: store.TimerPlayer, Character: c2.ID, Name: "theirs", Deadline: at},
		{Kind: store.TimerPlayer, Character: c2.ID, Name: "mine", Delete: true},
		{Kind: store.TimerRoom, Character: c2.ID, Room: "hall", Name: "turn", Deadline: at},
	}}); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.PlayerTimers(ctx, c2.ID); len(list) != 1 || list[0].Name != "mine" {
		t.Errorf("the other world's character: %+v", list)
	}
	if rooms, _ := s.RoomTimers(ctx, w1.ID); len(rooms) != 1 || rooms[0].Character != 0 {
		t.Errorf("a starter from another world: %+v", rooms)
	}
	if rooms, _ := s.RoomTimers(ctx, w2.ID); len(rooms) != 0 {
		t.Errorf("the other world's rooms: %+v", rooms)
	}

	// Finishing a world deletes only its timers.
	_ = s.Save(ctx, store.Save{World: w1.ID, Timers: []store.Timer{{Kind: store.TimerPlayer, Character: c1.ID, Name: "a", Deadline: at}}})
	if err := s.FinishWorld(ctx, w1.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.PlayerTimers(ctx, c1.ID); len(list) != 0 {
		t.Errorf("a finished world's player timers: %+v", list)
	}
	if rooms, _ := s.RoomTimers(ctx, w1.ID); len(rooms) != 0 {
		t.Errorf("a finished world's room timers: %+v", rooms)
	}
	if list, _ := s.PlayerTimers(ctx, c2.ID); len(list) != 1 {
		t.Errorf("another world's timers after finishing: %+v", list)
	}
}

// A save is all or nothing: a timer that cannot be written undoes the
// state and the timers of the same save. Deleting a timer that is not
// there is no error, and deadlines before 1970 and read_only round-trip.
func TestTimersSaveIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	c, err := s.Character(ctx, w.ID, ann.ID, "Ann")
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(time.Now().UnixMilli())
	if err := s.Save(ctx, store.Save{World: w.ID, WorldState: []store.Change{{Key: "door", Value: []byte(`"open"`)}},
		Character: c.ID, Room: "hall", Timers: []store.Timer{
			{Kind: store.TimerPlayer, Character: c.ID, Name: "guards", Deadline: at},
			{Kind: store.TimerRoom, Room: "hall", Name: "turn", Deadline: at},
			{Kind: "alarm", Character: c.ID, Name: "x", Deadline: at},
		}}); err == nil {
		t.Fatal("a save with an unknown timer kind worked")
	}
	if vals, _ := s.WorldState(ctx, w.ID); len(vals) != 0 {
		t.Errorf("world state of a failed save: %q", vals)
	}
	if list, _ := s.PlayerTimers(ctx, c.ID); len(list) != 0 {
		t.Errorf("player timers of a failed save: %+v", list)
	}
	if rooms, _ := s.RoomTimers(ctx, w.ID); len(rooms) != 0 {
		t.Errorf("room timers of a failed save: %+v", rooms)
	}

	old := time.UnixMilli(-86400000)
	if err := s.Save(ctx, store.Save{World: w.ID, Timers: []store.Timer{
		{Kind: store.TimerPlayer, Character: c.ID, Name: "missing", Delete: true},
		{Kind: store.TimerQuestion, Character: c.ID, Room: "hall", Name: "missing", Delete: true},
		{Kind: store.TimerRoom, Room: "hall", Name: "missing", Delete: true},
		{Kind: store.TimerPlayer, Character: c.ID, Name: "old", Deadline: old, ReadOnly: true, Show: "Old"},
	}}); err != nil {
		t.Fatalf("deleting timers that are not there: %v", err)
	}
	list, err := s.PlayerTimers(ctx, c.ID)
	if err != nil || len(list) != 1 || !list[0].Deadline.Equal(old) || !list[0].ReadOnly || list[0].Show != "Old" || list[0].Kind != store.TimerPlayer {
		t.Errorf("player timers = %+v, %v", list, err)
	}
	// A character of no world: nothing written, no error.
	if err := s.Save(ctx, store.Save{World: w.ID, Timers: []store.Timer{{Kind: store.TimerPlayer, Character: 9999, Name: "ghost", Deadline: at}}}); err != nil {
		t.Errorf("a timer of an unknown character: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM player_timers WHERE character_id = 9999`); n != 0 {
		t.Errorf("%d rows for an unknown character", n)
	}
}
