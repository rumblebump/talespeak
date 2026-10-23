package game

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// What lasts in a world with a store: state.world, each character's
// state.player and room, and the timers (lasting.go), read when the world
// starts or the player joins, and written after every hook, in one
// transaction per hook call.

// errNoAccount refuses a player without an account in a world with a
// store.
var errNoAccount = errors.New("not signed in")

// endedMessage answers a session that has ended, such as one the player
// replaced by connecting again elsewhere.
const endedMessage = "This session has ended. Reload the page to play here."

// replacedMessage is the last message of a session the player replaced.
const replacedMessage = "You are playing in another window or on another device now."

// ErrClosedMessage is what a player joining a world with a store that
// closed meanwhile is told.
const ErrClosedMessage = "This world is not running any more. Try again."

// joinError is what a player who could not join is told.
func joinError(err error) string {
	if errors.Is(err, errNoAccount) {
		return "Sign in to play."
	}
	if errors.Is(err, errFull) {
		return "This world is full right now. Try again later."
	}
	return "Your character could not be loaded. Try again later."
}

// errFull is a player's stored state that does not fit into the world's
// or their network's share of script memory now.
var errFull = errors.New("no room for the player's state")

// load finds who's character, ends the session it still has, and reads
// its state.player. It returns the room the player continues in. The
// caller holds w.mu.
func (s *session) load(who ws.Who) (string, error) {
	w := s.w
	if who.Account == 0 || who.Name == "" {
		return "", errNoAccount
	}
	ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
	defer cancel()
	c, err := w.store.Character(ctx, w.worldID, who.Account, who.Name)
	if err != nil {
		return "", err
	}
	room := c.Room
	if old := w.chars[c.ID]; old != nil {
		room = old.room
		old.replace() // saves what it changed last, so the read below has it
	}
	if lost, ok := w.lost[c.ID]; ok && lost.Room != "" {
		room = lost.Room
	}
	if err := w.writeLost(ctx, c.ID); err != nil {
		return "", err // reading now would bring back older values
	}
	if err := w.writeTimers(ctx); err != nil {
		return "", err
	}
	vals, err := w.store.PlayerState(ctx, c.ID)
	if err != nil {
		return "", err
	}
	timers, err := w.store.PlayerTimers(ctx, c.ID)
	if err != nil {
		return "", err
	}
	if err := s.state.Load(vals); err != nil {
		return "", errors.Join(errFull, err)
	}
	// The role as it is now, not as it was when the connection signed
	// in: a demotion between the two would otherwise keep GM powers.
	// RoleChanged writes the store before it looks for sessions, under
	// w.mu like this, so either this read sees the change or it finds
	// this session.
	role, err := w.store.Member(ctx, w.worldID, who.Account)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	s.gm = role == store.MemberGM
	s.char, s.id, s.name = c.ID, "p"+strconv.FormatInt(c.ID, 10), c.Name
	s.state.Changed = s.changedState
	s.restoreTimers(timers)
	if w.pack.Rooms[room] == nil {
		return w.pack.Start, nil // a new character, or its room left the pack
	}
	return room, nil
}

// changedState notes a key of state.player the running hook changed. The
// caller holds w.mu.
func (s *session) changedState(key string) {
	if !slices.Contains(s.dirty, key) {
		s.dirty = append(s.dirty, key)
	}
}

// replace ends the session for a newer one of the same player: on_leave
// runs as if the player left, and the connection closes with a last
// message. The caller holds w.mu.
func (s *session) replace() {
	s.end()
	s.replaced = true
	s.queue, s.queueBytes = []ws.Message{{Type: "error", Message: replacedMessage}}, 0
	if s.notify != nil {
		s.notify()
	}
}

// Replaced implements ws.Replacer.
func (s *session) Replaced() bool {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	return s.replaced
}

// maxLost bounds the saves of players who left that wait to be written
// again; past it the oldest is dropped (and logged).
const maxLost = 1000

// save writes what the hook that just ended changed: state.world, and s's
// state.player and room. Its time counts toward s's Lua time, like the
// hook's. A failed write is logged; the world plays on with what it has in
// memory, and the next save tries the keys again, also those of a player
// who left meanwhile. The caller holds w.mu.
func (w *World) save(s *session) {
	if w.store == nil {
		return
	}
	sv := store.Save{World: w.worldID}
	keys := w.unsaved
	for _, k := range w.changed {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		v, _ := w.world.Encoded(k)
		sv.WorldState = append(sv.WorldState, store.Change{Key: k, Value: v})
	}
	if s.char != 0 {
		sv.Character = s.char
		for _, k := range s.dirty {
			v, _ := s.state.Encoded(k)
			sv.PlayerState = append(sv.PlayerState, store.Change{Key: k, Value: v})
		}
		if s.moved {
			sv.Room = s.room
		}
	}
	sv.Timers = w.timerChanges()
	if sv.Empty() && len(w.lost) == 0 {
		return
	}
	err := w.charged(s.client, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
		defer cancel()
		if err := w.writeLost(ctx, 0); err != nil {
			return err
		}
		return w.store.Save(ctx, sv)
	})
	if err != nil {
		w.log.Error("saving state failed", "world", w.worldID, "character", s.char, "err", err)
		w.unsaved = keys
		if s.leaving && s.char != 0 && (len(sv.PlayerState) > 0 || sv.Room != "") {
			w.keepLost(store.Save{World: w.worldID, Character: s.char, PlayerState: sv.PlayerState, Room: sv.Room})
		}
		return
	}
	w.unsaved, s.dirty, s.moved = nil, nil, false
	clear(w.timerSaves)
}

// keepLost keeps the failed save of a player who is leaving, merged with
// one kept before, to write with the next save or when they come back.
// The caller holds w.mu.
func (w *World) keepLost(sv store.Save) {
	if old, ok := w.lost[sv.Character]; ok {
		for _, c := range sv.PlayerState {
			old.PlayerState = slices.DeleteFunc(old.PlayerState, func(o store.Change) bool { return o.Key == c.Key })
		}
		sv.PlayerState = append(old.PlayerState, sv.PlayerState...)
		if sv.Room == "" {
			sv.Room = old.Room
		}
		w.lostOrder = slices.DeleteFunc(w.lostOrder, func(c int64) bool { return c == sv.Character })
	}
	if len(w.lostOrder) >= maxLost {
		w.log.Error("dropping a save that failed", "world", w.worldID, "character", w.lostOrder[0])
		delete(w.lost, w.lostOrder[0])
		w.lostOrder = w.lostOrder[1:]
	}
	w.lost[sv.Character] = sv
	w.lostOrder = append(w.lostOrder, sv.Character)
}

// writeLost writes the kept saves of players who left: only character's
// when it is not 0. The caller holds w.mu.
func (w *World) writeLost(ctx context.Context, character int64) error {
	for _, c := range slices.Clone(w.lostOrder) {
		if character != 0 && c != character {
			continue
		}
		if err := w.store.Save(ctx, w.lost[c]); err != nil {
			return err
		}
		delete(w.lost, c)
		w.lostOrder = slices.DeleteFunc(w.lostOrder, func(o int64) bool { return o == c })
	}
	return nil
}
