package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// A world's time starts at zero, is written with a save and read back to
// the millisecond; NULL paused_at means running.
func TestWorldClock(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	if c, err := s.WorldClock(ctx, w.ID); err != nil || c != (store.Clock{}) {
		t.Fatalf("a new world's clock = %+v, %v", c, err)
	}
	at := time.UnixMilli(time.Now().UnixMilli())
	want := store.Clock{PausedAt: at, PausedFor: 90 * time.Second, Game: 5 * time.Minute, MinTime: 30 * time.Second}
	if err := s.Save(ctx, store.Save{World: w.ID, Clock: &want}); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorldClock(ctx, w.ID)
	if err != nil || !got.PausedAt.Equal(at) || got.PausedFor != want.PausedFor || got.Game != want.Game || got.MinTime != want.MinTime {
		t.Errorf("clock = %+v, %v; want %+v", got, err, want)
	}
	if err := s.Save(ctx, store.Save{World: w.ID, Clock: &store.Clock{Game: time.Hour}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.WorldClock(ctx, w.ID); !got.PausedAt.IsZero() || got.Game != time.Hour || got.MinTime != 0 {
		t.Errorf("after resume: %+v", got)
	}
	if _, err := s.WorldClock(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown world: %v", err)
	}
	if (store.Save{World: w.ID, Clock: &store.Clock{}}).Empty() {
		t.Error("a save of the clock alone is empty")
	}
}

// A member's extra time defaults to 1, is set by account name in any
// letter case, only for members of that world, and only from 1 to 3.
func TestExtraTime(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	other, _ := s.CreateWorld(ctx, store.NewWorld{PackID: "demo", PackVersion: "1.0.0", Visibility: store.VisibilityPublic})
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	bob := mustAccount(t, s, "Bob", store.RolePlayer)
	if err := s.AddMember(ctx, w.ID, ann.ID, store.MemberPlayer); err != nil {
		t.Fatal(err)
	}
	if f, err := s.ExtraTime(ctx, w.ID, ann.ID); err != nil || f != 1 {
		t.Errorf("default = %v, %v", f, err)
	}
	if _, err := s.ExtraTime(ctx, w.ID, bob.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no member: %v", err)
	}
	if id, err := s.SetExtraTime(ctx, w.ID, "aNN", 1.5); err != nil || id != ann.ID {
		t.Fatalf("set = %v, %v", id, err)
	}
	if f, _ := s.ExtraTime(ctx, w.ID, ann.ID); f != 1.5 {
		t.Errorf("after set = %v", f)
	}
	for _, c := range []struct {
		world int64
		name  string
		f     float64
	}{{w.ID, "Bob", 2}, {other.ID, "Ann", 2}, {w.ID, "Nobody", 2}} {
		if _, err := s.SetExtraTime(ctx, c.world, c.name, c.f); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SetExtraTime(%d, %s) = %v", c.world, c.name, err)
		}
	}
	for _, f := range []float64{0.5, 3.5} {
		if _, err := s.SetExtraTime(ctx, w.ID, "Ann", f); err == nil {
			t.Errorf("factor %v was taken", f)
		}
	}
	list, err := s.Members(ctx, w.ID, 10)
	if err != nil || len(list) != 1 || list[0].ExtraTime != 1.5 {
		t.Errorf("members = %+v, %v", list, err)
	}
}

// A timer's game flag is written and read back.
func TestGameTimersSaved(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	w, _ := s.OpenWorld(ctx, "demo", "", nil)
	ann := mustAccount(t, s, "Ann", store.RolePlayer)
	c, _ := s.Character(ctx, w.ID, ann.ID, "Ann")
	at := time.UnixMilli(60000)
	if err := s.Save(ctx, store.Save{World: w.ID, Timers: []store.Timer{
		{Kind: store.TimerPlayer, Character: c.ID, Name: "tick", Deadline: at, Game: true},
		{Kind: store.TimerRoom, Room: "hall", Name: "sand", Deadline: at, Game: true},
		{Kind: store.TimerRoom, Room: "hall", Name: "bell", Deadline: at},
	}}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.PlayerTimers(ctx, c.ID)
	rooms, _ := s.RoomTimers(ctx, w.ID)
	if len(list) != 1 || !list[0].Game || len(rooms) != 2 || rooms[0].Game || !rooms[1].Game {
		t.Errorf("player %+v, rooms %+v", list, rooms)
	}
}
