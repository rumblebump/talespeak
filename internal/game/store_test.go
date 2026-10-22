package game

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
	"github.com/rumblebump/talespeak/internal/ws"
)

// storeRooms writes to all three kinds of lasting state: a click in room a
// sets the player's nail and the world's door and moves to b, whose
// on_enter reports what it sees.
const (
	storeRoomA = `
function on_enter(p) say("A " .. p.id .. " nail=" .. tostring(state.player.get("nail")) .. " door=" .. tostring(state.world.get("door"))) end
function on_click() state.player.set("nail", {n = 3}) state.world.set("door", "open") room.go("b") end`
	storeRoomB = `
function on_enter(p) say("B " .. p.id .. " nail=" .. tostring((state.player.get("nail") or {}).n) .. " door=" .. tostring(state.world.get("door"))) end
function on_input(p, text) state.world.set("door", nil) state.player.set("nail", nil) say("cleared") end`
)

// openStore opens a SQLite store in dir with two accounts in it.
func openStore(t *testing.T, path string) (*sqlite.Store, store.World, []ws.Who) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: real SQLite file")
	}
	ctx := context.Background()
	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	world, err := st.OpenWorld(ctx, "t", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var who []ws.Who
	for _, name := range []string{"Mira", "Zed"} {
		a, _, err := st.AccountByName(ctx, name)
		if err != nil {
			if a, err = st.CreateAccount(ctx, name, "x", store.RolePlayer); err != nil {
				t.Fatal(err)
			}
		}
		who = append(who, ws.Who{Account: a.ID, Name: a.Name})
	}
	return st, world, who
}

func storeWorld(t *testing.T, st *sqlite.Store, world store.World) *World {
	t.Helper()
	p, err := pack.Load(roomsFS(storeRoomA, storeRoomB))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithStore(st, world.ID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// A restart keeps state.player, state.world, where the player is and
// their player.id; another player's state.player stays theirs.
func TestStoreRestartKeepsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, world, who := openStore(t, path)
	w := storeWorld(t, st, world)
	mira, first := w.NewSession("", who[0])
	if got := texts(first); !strings.HasPrefix(got, "A p") || !strings.HasSuffix(got, " nail=nil door=nil") {
		t.Fatalf("first = %q", got)
	}
	id := strings.Fields(texts(first))[1]
	if got := texts(mira.Handle(ws.Message{Type: "click", Item: "box"})); got != "items:rope | B "+id+" nail=3 door=open" {
		t.Fatalf("click = %q", got)
	}
	mira.Close()
	w.Close()
	st.Close()

	st, world, who = openStore(t, path)
	w = storeWorld(t, st, world)
	mira, first = w.NewSession("", who[0])
	defer mira.Close()
	if got := texts(first); got != "items:rope | B "+id+" nail=3 door=open" {
		t.Errorf("after restart: %q, want the same player in room b with their state", got)
	}
	zed, first := w.NewSession("", who[1])
	defer zed.Close()
	if got := texts(first); strings.Contains(got, id) || !strings.HasSuffix(got, "nail=nil door=open") {
		t.Errorf("another player: %q, want a new id, no nail and the shared door", got)
	}

	// Removing keys lasts too.
	if got := texts(mira.Handle(ws.Message{Type: "input", Text: "x"})); got != "cleared" {
		t.Fatalf("input = %q", got)
	}
	ctx := context.Background()
	vals, _ := st.WorldState(ctx, world.ID)
	if len(vals) != 0 {
		t.Errorf("world state after removing = %v", vals)
	}
}

// Connecting again ends the older session: it runs on_leave, gets a last
// message and is told to close, and the new one has its state.
func TestStoreReplacesOlderSession(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	w := storeWorld(t, st, world)
	old, _ := w.NewSession("", who[0])
	notified := 0
	old.(ws.Pusher).SetNotify(func() { notified++ })
	old.Handle(ws.Message{Type: "click", Item: "box"})

	cur, first := w.NewSession("", who[0])
	defer cur.Close()
	if got := texts(first); !strings.HasPrefix(got, "items:rope | B p") || !strings.HasSuffix(got, "nail=3 door=open") {
		t.Errorf("new session = %q", got)
	}
	ms, ok := old.(ws.Pusher).Pending()
	if ok || len(ms) != 1 || ms[0].Message != replacedMessage || !old.(ws.Replacer).Replaced() || notified == 0 {
		t.Errorf("old session: %+v, ok=%v, replaced=%v, notified %d", ms, ok, old.(ws.Replacer).Replaced(), notified)
	}
	if got := old.Handle(ws.Message{Type: "input", Text: "x"}); len(got) != 1 || got[0].Message != endedMessage {
		t.Errorf("old session still plays: %+v", got)
	}
	old.Close() // the connection ends later; that must not end the new session
	w.mu.Lock()
	n := len(w.sessions)
	w.mu.Unlock()
	if n != 1 {
		t.Errorf("sessions = %d, want the new one", n)
	}
	if got := texts(cur.Handle(ws.Message{Type: "input", Text: "x"})); got != "cleared" {
		t.Errorf("new session after the old one closed: %q", got)
	}
}

// A world with a store refuses players without an account.
func TestStoreWantsAccount(t *testing.T) {
	st, world, _ := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	w := storeWorld(t, st, world)
	s, first := w.NewSession("", ws.Who{})
	if s != nil || len(first) != 1 || first[0].Type != "error" || first[0].Message != "Sign in to play." {
		t.Errorf("guest: %v, %+v", s, first)
	}
}

// A character whose stored room is no longer in the pack starts over in
// the start room, with its state.
func TestStoreUnknownRoomStartsOver(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	ctx := context.Background()
	c, err := st.Character(ctx, world.ID, who[0].Account, who[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(ctx, store.Save{World: world.ID, Character: c.ID, Room: "gone"}); err != nil {
		t.Fatal(err)
	}
	w := storeWorld(t, st, world)
	s, first := w.NewSession("", who[0])
	defer s.Close()
	if got := texts(first); !strings.HasPrefix(got, "A p") {
		t.Errorf("first = %q, want the start room", got)
	}
}

// failingStore fails Save while fail is set.
type failingStore struct {
	store.Worlds
	fail bool
}

func (f *failingStore) Save(ctx context.Context, sv store.Save) error {
	if f.fail {
		return context.DeadlineExceeded
	}
	return f.Worlds.Save(ctx, sv)
}

// A save that fails as the player leaves is kept, and written before the
// player comes back, so they do not get older values.
func TestStoreKeepsFailedSaveOfLeaver(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	fs := &failingStore{Worlds: st}
	p, err := pack.Load(roomsFS(storeRoomA, `
function on_enter(p) say("B nail=" .. tostring((state.player.get("nail") or {}).n)) end
function on_leave(p) state.player.set("nail", {n = 9}) end`))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithStore(fs, world.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	s, _ := w.NewSession("", who[0])
	s.Handle(ws.Message{Type: "click", Item: "box"})
	fs.fail = true
	s.Close() // on_leave sets nail 9; the save fails
	w.mu.Lock()
	n := len(w.lost)
	w.mu.Unlock()
	if n != 1 {
		t.Fatalf("lost = %d, want the leaver's save kept", n)
	}
	s, first := w.NewSession("", who[0])
	if s != nil {
		t.Errorf("joined while the kept save cannot be written: %q", texts(first))
		s.Close()
	}
	fs.fail = false
	s, first = w.NewSession("", who[0])
	defer s.Close()
	if got := texts(first); got != "items:rope | B nail=9" {
		t.Errorf("after the store is back: %q, want room b with on_leave's nail", got)
	}
}

// A world with a store that closed refuses players before it reads or
// writes their character: it may have been finished meanwhile.
func TestClosedWorldRefusesWithStore(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	w := storeWorld(t, st, world)
	w.Close()
	s, ms := w.NewSession("", who[0])
	if s != nil || len(ms) != 1 || ms[0].Message != ErrClosedMessage {
		t.Fatalf("NewSession = %v, %+v", s, ms)
	}
}
