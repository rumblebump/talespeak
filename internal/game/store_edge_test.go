package game

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Edge cases of a world with a store (T-0034): saves that fail and are
// tried again, a player who leaves while saves fail, a takeover while a
// dialog waits, the save's time charged as Lua time, and stored state
// that cannot load.

// flakyStore is a store whose next saves can be made to fail, and whose
// saves can take (fake) time.
type flakyStore struct {
	*sqlite.Store
	mu        sync.Mutex
	failSaves int
	failChar  bool          // Character fails
	saveTime  time.Duration // each save advances clock by this
	clock     *fakeClock
	saves     int
}

var errFlaky = errors.New("disk unplugged")

func (f *flakyStore) fail(n int) {
	f.mu.Lock()
	f.failSaves = n
	f.mu.Unlock()
}

func (f *flakyStore) Save(ctx context.Context, sv store.Save) error {
	f.mu.Lock()
	f.saves++
	if f.clock != nil && f.saveTime > 0 {
		f.clock.mu.Lock()
		f.clock.now = f.clock.now.Add(f.saveTime)
		f.clock.mu.Unlock()
	}
	if f.failSaves > 0 {
		f.failSaves--
		f.mu.Unlock()
		return errFlaky
	}
	f.mu.Unlock()
	return f.Store.Save(ctx, sv)
}

func (f *flakyStore) Character(ctx context.Context, world, account int64, name string) (store.Character, error) {
	f.mu.Lock()
	fail := f.failChar
	f.mu.Unlock()
	if fail {
		return store.Character{}, errFlaky
	}
	return f.Store.Character(ctx, world, account, name)
}

// Room a: a click keeps a nail and opens the door, then moves to b. Room
// b: a click rings the bell (world state only); input changes nothing.
const (
	edgeStoreA = `
function on_enter(p) say("A " .. p.id .. " nail=" .. tostring(state.player.get("nail")) .. " door=" .. tostring(state.world.get("door"))) end
function on_click() state.player.set("nail", 3) state.world.set("door", "open") room.go("b") end
function on_input() say("a hears") end`
	edgeStoreB = `
function on_enter(p) say("B " .. p.id .. " nail=" .. tostring(state.player.get("nail")) .. " door=" .. tostring(state.world.get("door"))) end
function on_click() state.world.set("bell", "rung") say("ding") end
function on_input() say("b hears") end`
)

func edgeStoreWorld(t *testing.T, st store.Worlds, world int64, opts ...Option) *World {
	t.Helper()
	p, err := pack.Load(roomsFS(edgeStoreA, edgeStoreB))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet, append([]Option{WithStore(st, world)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}

// stored reads what the database has for who: their state.player, their
// room, and state.world.
func stored(t *testing.T, st *sqlite.Store, world store.World, who ws.Who) (player map[string][]byte, room string, wstate map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	c, err := st.Character(ctx, world.ID, who.Account, who.Name)
	if err != nil {
		t.Fatal(err)
	}
	if player, err = st.PlayerState(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if wstate, err = st.WorldState(ctx, world.ID); err != nil {
		t.Fatal(err)
	}
	return player, c.Room, wstate
}

// A failed save leaves the game playing from memory; the next save of the
// same player writes what the failed one did not, and the next save of
// anyone writes the world keys.
func TestStoreSaveFailureRetried(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	fs := &flakyStore{Store: st}
	w := edgeStoreWorld(t, fs, world.ID)
	mira, _ := w.NewSession("", who[0])
	defer mira.Close()

	fs.fail(1)
	if got := texts(mira.Handle(ws.Message{Type: "click", Item: "box"})); !strings.HasSuffix(got, "nail=3 door=open") {
		t.Fatalf("click with a failing save = %q, want the game to go on", got)
	}
	p, room, ws1 := stored(t, st, world, who[0])
	if len(p) != 0 || room != "a" || len(ws1) != 0 {
		t.Fatalf("after a failed save: player %q, room %q, world %q; want nothing new", p, room, ws1)
	}
	if got := texts(mira.Handle(ws.Message{Type: "input", Text: "x"})); got != "b hears" {
		t.Fatalf("input = %q", got)
	}
	p, room, ws1 = stored(t, st, world, who[0])
	if len(p) != 1 || room != "b" || string(ws1["door"]) == "" {
		t.Errorf("after the next save: player %q, room %q, world %q; want nail, b and door", p, room, ws1)
	}

	// Mira's world key fails; Zed's next hook, which changes nothing, writes it.
	zed, _ := w.NewSession("", who[1])
	defer zed.Close()
	fs.fail(1)
	if got := texts(mira.Handle(ws.Message{Type: "click", Item: "rope"})); got != "ding" {
		t.Fatalf("ring = %q", got)
	}
	if _, _, ws2 := stored(t, st, world, who[0]); ws2["bell"] != nil {
		t.Fatalf("bell stored by a failed save")
	}
	if got := texts(zed.Handle(ws.Message{Type: "input", Text: "x"})); got != "a hears" {
		t.Fatalf("zed input = %q", got)
	}
	if _, _, ws2 := stored(t, st, world, who[0]); ws2["bell"] == nil {
		t.Errorf("another player's save did not write the world key a failed save left: %q", ws2)
	}
}

// A player who leaves while saves fail keeps what they changed: the next
// save writes it, and they continue there.
func TestStoreLostSaveOfLeaverWrittenLater(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	fs := &flakyStore{Store: st}
	w := edgeStoreWorld(t, fs, world.ID)
	mira, _ := w.NewSession("", who[0])
	fs.fail(2) // the click's save and the one when leaving
	mira.Handle(ws.Message{Type: "click", Item: "box"})
	mira.Close()
	if p, room, _ := stored(t, st, world, who[0]); len(p) != 0 || room != "a" {
		t.Fatalf("stored after failing saves: %q, room %q", p, room)
	}
	// Zed joining saves his room, and with it Mira's lost save.
	zed, _ := w.NewSession("", who[1])
	defer zed.Close()
	if p, room, wst := stored(t, st, world, who[0]); len(p) != 1 || room != "b" || wst["door"] == nil {
		t.Errorf("after another player's save: %q, room %q, world %q; want nail, b, door", p, room, wst)
	}
	mira, first := w.NewSession("", who[0])
	defer mira.Close()
	if got := texts(first); !strings.HasPrefix(got, "items:rope | B ") || !strings.HasSuffix(got, "nail=3 door=open") {
		t.Errorf("back = %q", got)
	}
}

// A player who comes back before any save worked gets what they left:
// joining writes it first, and refuses the join while it cannot.
func TestStoreLostSaveOfLeaverOnReturn(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	fs := &flakyStore{Store: st}
	w := edgeStoreWorld(t, fs, world.ID)
	mira, _ := w.NewSession("", who[0])
	fs.fail(3) // click, leave, and the first try to come back
	mira.Handle(ws.Message{Type: "click", Item: "box"})
	mira.Close()

	s, first := w.NewSession("", who[0])
	if s != nil || len(first) != 1 || first[0].Type != "error" || !strings.Contains(first[0].Message, "could not be loaded") {
		t.Fatalf("join while the lost save cannot be written: %v, %+v", s, first)
	}
	if p, _, _ := stored(t, st, world, who[0]); len(p) != 0 {
		t.Fatalf("stored %q", p)
	}
	s, first = w.NewSession("", who[0])
	if s == nil {
		t.Fatalf("join once saves work: %+v", first)
	}
	defer s.Close()
	if got := texts(first); !strings.HasPrefix(got, "items:rope | B ") || !strings.HasSuffix(got, "nail=3 door=open") {
		t.Errorf("back = %q, want room b with the nail", got)
	}
	if p, room, _ := stored(t, st, world, who[0]); len(p) != 1 || room != "b" {
		t.Errorf("stored after return: %q, room %q", p, room)
	}
}

// A takeover while the old session waits for a dialog: the old one's
// dialog ends unanswered, what it kept before the dialog lasts, and the
// new session asks again.
func TestStoreTakeoverWhileDialogPending(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	p, err := pack.Load(roomsFS(`
function on_enter(p)
  local n = (state.player.get("n") or 0) + 1
  state.player.set("n", n)
  say("enter " .. n)
  ui.dialog("Stay?", {"yes"})
  state.player.set("answered", n)
  say("answered " .. n)
end`, `function on_enter() end`))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, quiet, WithStore(st, world.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	old, first := w.NewSession("", who[0])
	if got := texts(first); got != "enter 1 | dialog" {
		t.Fatalf("old first = %q", got)
	}
	cur, first := w.NewSession("", who[0])
	defer cur.Close()
	if got := texts(first); got != "enter 2 | dialog" {
		t.Errorf("new first = %q, want the kept n and the dialog again", got)
	}
	ms, ok := old.(ws.Pusher).Pending()
	if ok || len(ms) != 1 || ms[0].Message != replacedMessage {
		t.Errorf("old pending = %+v, %v", ms, ok)
	}
	if got := old.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1}); len(got) != 1 || got[0].Message != endedMessage {
		t.Errorf("old answers its dialog: %+v", got)
	}
	vals, _, _ := stored(t, st, world, who[0])
	if _, ok := vals["answered"]; ok {
		t.Error("the old session's dialog went on after the takeover")
	}
	old.Close()
	if got := texts(cur.Handle(ws.Message{Type: "choose", ID: "d1", Option: 1})); got != "answered 2" {
		t.Errorf("new answers = %q", got)
	}
	vals, _, _ = stored(t, st, world, who[0])
	if len(vals) != 2 {
		t.Errorf("stored = %q, want n and answered", vals)
	}
}

// Several takeovers in a row: only the newest session plays; each older
// one is told and closes.
func TestStoreTakeoverChain(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	w := edgeStoreWorld(t, st, world.ID)
	var sessions []ws.Session
	for range 4 {
		s, _ := w.NewSession("", who[0])
		sessions = append(sessions, s)
	}
	for i, s := range sessions[:3] {
		if !s.(ws.Replacer).Replaced() {
			t.Errorf("session %d not replaced", i)
		}
		s.Close()
	}
	last := sessions[3]
	defer last.Close()
	if last.(ws.Replacer).Replaced() {
		t.Error("the newest session is replaced")
	}
	if got := texts(last.Handle(ws.Message{Type: "input", Text: "x"})); got != "a hears" {
		t.Errorf("newest = %q", got)
	}
	w.mu.Lock()
	n, chars := len(w.sessions), len(w.chars)
	w.mu.Unlock()
	if n != 1 || chars != 1 {
		t.Errorf("sessions = %d, chars = %d; want 1 and 1", n, chars)
	}
}

// The time a save takes counts toward the player's Lua time.
func TestStoreSaveChargesLuaTime(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	clk := newClock()
	fs := &flakyStore{Store: st, clock: clk}
	w := edgeStoreWorld(t, fs, world.ID, WithClock(clk.Now, clk.After), WithClientBudget(time.Second, 10*time.Second))
	const client = "198.51.100.4"
	mira, _ := w.NewSession(client, who[0])
	defer mira.Close()
	left := func() float64 {
		w.mu.Lock()
		defer w.mu.Unlock()
		if b := w.budgets[client]; b != nil {
			return b.left
		}
		return 10
	}
	before := left()
	fs.mu.Lock()
	fs.saveTime = 3 * time.Second
	fs.mu.Unlock()
	mira.Handle(ws.Message{Type: "click", Item: "box"}) // a save with changes
	if spent := before - left(); spent < 3 {
		t.Errorf("a save of 3 s took %.1f s of Lua time, want 3", spent)
	}
	// A hook that changes nothing does not save.
	fs.mu.Lock()
	saves := fs.saves
	fs.mu.Unlock()
	mira.Handle(ws.Message{Type: "input", Text: "x"})
	fs.mu.Lock()
	if fs.saves != saves {
		t.Errorf("a hook that changed nothing saved")
	}
	fs.mu.Unlock()
}

// Stored state that breaks the limits does not load: a world whose
// state.world cannot load does not start, a player whose state.player
// cannot is refused.
func TestStoreStateOutOfBounds(t *testing.T) {
	ctx := context.Background()
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	c, err := st.Character(ctx, world.ID, who[0].Account, who[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	var many []store.Change
	for i := range script.MaxPlayerStateKeys + 1 {
		many = append(many, store.Change{Key: "k" + strconv.Itoa(i), Value: []byte{2}})
	}
	if err := st.Save(ctx, store.Save{World: world.ID, Character: c.ID, PlayerState: many}); err != nil {
		t.Fatal(err)
	}
	w := edgeStoreWorld(t, st, world.ID)
	s, first := w.NewSession("", who[0])
	if s != nil || len(first) != 1 || first[0].Type != "error" || first[0].Message != joinError(errFull) {
		t.Errorf("player with too many stored keys: %v, %+v", s, first)
	}
	zed, first := w.NewSession("", who[1])
	if zed == nil {
		t.Fatalf("another player is refused: %+v", first)
	}
	zed.Close()

	long := strings.Repeat("k", script.MaxStateKeyBytes+1)
	if err := st.Save(ctx, store.Save{World: world.ID, WorldState: []store.Change{{Key: long, Value: []byte{2}}}}); err != nil {
		t.Fatal(err)
	}
	p, _ := pack.Load(roomsFS(edgeStoreA, edgeStoreB))
	if w2, err := NewWorld(p, script.DefaultLimits, quiet, WithStore(st, world.ID)); err == nil || !strings.Contains(err.Error(), "world state") {
		if w2 != nil {
			w2.Close()
		}
		t.Errorf("NewWorld with a stored key past the limit = %v", err)
	}
}

// A store that cannot find the character refuses the join with a message,
// and a sign-in without a name is no sign-in.
func TestStoreJoinErrors(t *testing.T) {
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	fs := &flakyStore{Store: st, failChar: true}
	w := edgeStoreWorld(t, fs, world.ID)
	s, first := w.NewSession("", who[0])
	if s != nil || len(first) != 1 || first[0].Message != "Your character could not be loaded. Try again later." {
		t.Errorf("Character fails: %v, %+v", s, first)
	}
	s, first = w.NewSession("", ws.Who{Account: who[0].Account})
	if s != nil || len(first) != 1 || first[0].Message != "Sign in to play." {
		t.Errorf("no name: %v, %+v", s, first)
	}
	s, first = w.NewSession("", ws.Who{Name: "Mira"})
	if s != nil || len(first) != 1 || first[0].Message != "Sign in to play." {
		t.Errorf("no account: %v, %+v", s, first)
	}
}

// player.id is the character id, the same in every session, and two
// accounts never share one.
func TestStorePlayerIDIsCharacter(t *testing.T) {
	ctx := context.Background()
	st, world, who := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	w := edgeStoreWorld(t, st, world.ID)
	for _, wh := range who {
		c, err := st.Character(ctx, world.ID, wh.Account, wh.Name)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			s, first := w.NewSession("", wh)
			if got := texts(first); !strings.HasPrefix(got, "A p"+strconv.FormatInt(c.ID, 10)+" ") {
				t.Errorf("%s: %q, want player.id p%d", wh.Name, got, c.ID)
			}
			s.Close()
		}
	}
}

// logCounter counts log records whose message is msg.
type logCounter struct {
	mu  sync.Mutex
	msg string
	n   int
}

func (c *logCounter) Enabled(context.Context, slog.Level) bool { return true }
func (c *logCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Message == c.msg {
		c.mu.Lock()
		c.n++
		c.mu.Unlock()
	}
	return nil
}
func (c *logCounter) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCounter) WithGroup(string) slog.Handler      { return c }

// While saves fail, at most maxLost players who left keep what they
// changed; past it the oldest is dropped and logged. Once saves work, the
// next save writes all that were kept.
func TestStoreLostSavesBounded(t *testing.T) {
	ctx := context.Background()
	st, world, _ := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	fs := &flakyStore{Store: st}
	logs := &logCounter{msg: "dropping a save that failed"}
	p, err := pack.Load(roomsFS(edgeStoreA, edgeStoreB))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(p, script.DefaultLimits, slog.New(logs), WithStore(fs, world.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	fs.fail(1 << 30) // every save fails
	var who []ws.Who
	for i := range maxLost + 2 {
		a, err := st.CreateAccount(ctx, "lost"+strconv.Itoa(i), "x", store.RolePlayer)
		if err != nil {
			t.Fatal(err)
		}
		wh := ws.Who{Account: a.ID, Name: a.Name}
		who = append(who, wh)
		s, first := w.NewSession("", wh)
		if s == nil {
			t.Fatalf("join %d: %+v", i, first)
		}
		s.Close() // its room was never saved: kept as lost
	}
	w.mu.Lock()
	lost, order := len(w.lost), len(w.lostOrder)
	_, firstKept := w.lost[mustChar(t, st, world, who[0])]
	w.mu.Unlock()
	if lost != maxLost || order != maxLost || firstKept {
		t.Errorf("lost = %d, order = %d, oldest kept = %v; want %d and the oldest dropped", lost, order, firstKept, maxLost)
	}
	logs.mu.Lock()
	if logs.n != 2 {
		t.Errorf("dropped saves logged %d times, want 2", logs.n)
	}
	logs.mu.Unlock()

	fs.fail(0)
	s, _ := w.NewSession("", who[len(who)-1]) // its own lost save first, then everyone's with its join save
	defer s.Close()
	w.mu.Lock()
	lost = len(w.lost)
	w.mu.Unlock()
	if lost != 0 {
		t.Errorf("lost after saves work again = %d", lost)
	}
	for _, i := range []int{2, maxLost} {
		c, err := st.Character(ctx, world.ID, who[i].Account, who[i].Name)
		if err != nil || c.Room != "a" {
			t.Errorf("player %d: room %q, %v; want the kept room written", i, c.Room, err)
		}
	}
	if c, _ := st.Character(ctx, world.ID, who[0].Account, who[0].Name); c.Room != "" {
		t.Errorf("the dropped save was written: room %q", c.Room)
	}
}

func mustChar(t *testing.T, st *sqlite.Store, world store.World, who ws.Who) int64 {
	t.Helper()
	c, err := st.Character(context.Background(), world.ID, who.Account, who.Name)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}
