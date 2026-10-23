package game

import (
	"time"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// One player's connection to a world: the session, its messages, and its
// instances of the room scripts.

// session is one player. Handle and Close are called from the
// connection's goroutine; timers call in from their own. w.mu guards every
// field.
type session struct {
	w       *World
	id      string // player.id: unique in the world while it runs, and for ever with a store
	char    int64  // the character in the store; 0 without one
	name    string
	gm      bool  // a game master of the world: player.gm, and the GM commands
	account int64 // the signed-in account; 0 without one
	client  string
	room    string
	joined  int64 // when the player joined the world, for world.players
	arrived int64 // when the player came into room, for room.players
	closed  bool
	leaving bool // on_leave runs: the player is no longer among room.players

	envs     map[string]*script.Env // room id -> this player's instance of its script
	pending  *script.Thread         // the hook waiting in ui.dialog, if any
	dialogID string
	dialogs  int           // dialogs shown, for unique ids
	items    []script.Item // what the player can click now
	state    script.State  // state.player, shared by the player's rooms
	dirty    []string      // state.player keys changed since the last save
	moved    bool          // room changed since the last save
	replaced bool          // the player connected again elsewhere: this session ended
	changed  []string      // world keys others changed that on_change has not seen yet
	next     string        // the room room.go moves the player to when the hook ends
	visited  []string      // rooms entered, the latest last
	moves    int           // room.go moves done for the current message

	timers      map[string]*timer         // the player's timers, by name
	dialogTimer func() bool               // stops the open question's timeout, if it has one
	questions   map[questionKey]time.Time // deadlines of questions asked, with a store (lasting.go)
	asked       questionKey               // the open question's entry in questions, if any

	out        []ws.Message // collects output during one hook call
	queue      []ws.Message // pushed output waiting for Pending
	queueBytes int          // about what queue takes on the wire
	notify     func()       // tells the connection that queue has messages; nil until SetNotify
	lagged     bool         // queue overflowed: the connection should close

	// readOnlyWorld is set while on_change runs, and for everything it
	// starts (room.go, timers): none of it can write state.world, so a
	// change never bounces between players by itself.
	readOnlyWorld bool
}

// timer is one of the player's timers.
type timer struct {
	deadline time.Time
	stop     func() bool
	show     string // the label the player sees, "" for a secret timer
	readOnly bool   // started under on_change: on_timer cannot write state.world
}

// MaxMoves caps the rooms one player message can move through, so rooms
// whose on_enter calls room.go cannot send a player round in circles, and
// one message cannot hold the world for many hooks' time.
const MaxMoves = 4

// Handle answers m. Pushed messages still waiting go first, so a reply
// never overtakes what was pushed before it.
func (s *session) Handle(m ws.Message) []ws.Message {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.closed {
		return []ws.Message{{Type: "error", Message: endedMessage}}
	}
	out := s.handle(m)
	if len(s.queue) > 0 {
		out = append(s.queue, out...)
		s.queue, s.queueBytes = nil, 0
	}
	return out
}

func (s *session) handle(m ws.Message) []ws.Message {
	s.moves = 0
	switch m.Type {
	case "choose":
		return s.choose(m)
	case "input":
		return s.input(m)
	case "click":
		return s.click(m)
	case "answer":
		return s.answer(m)
	default:
		return []ws.Message{{Type: "error", Message: "unknown message type"}}
	}
}

// Close ends the hook waiting on this player, if any, runs on_leave, stops
// their timers and frees the player's script instances.
func (s *session) Close() {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if !s.closed {
		s.end()
	}
}

// end is Close. The open question's deadline stays with the character.
// The caller holds w.mu.
func (s *session) end() {
	s.asked = questionKey{}
	s.leave()
	s.closed = true
	delete(s.w.sessions, s)
	if len(s.w.sessions) == 0 {
		s.w.lastLeft = s.w.now()
	}
	if s.char != 0 && s.w.chars[s.char] == s {
		delete(s.w.chars, s.char)
	}
	s.stopTimers()
	s.drop()
	for _, env := range s.envs {
		s.w.release(s.client, env)
	}
	s.envs = nil
	s.out, s.queue, s.queueBytes = nil, nil, 0 // a room timer may keep s until it runs out
	_ = s.reserve(-s.state.Size())
	s.state = script.State{}
}

// env returns the player's instance of room's script, starting it on first
// use; nil when the room has no script. When the player keeps
// MaxRoomInstances already, the room entered longest ago gives way. The
// caller holds w.mu.
func (s *session) env(room string) (*script.Env, error) {
	env, victim, _, err := s.start(room)
	if victim != "" {
		s.free(victim)
	}
	return env, err
}

// start is env without freeing: it returns the room whose instance should
// give way, if any, and whether it started a new instance, so a move can
// free the one or the other once it knows whether it happens. The caller
// holds w.mu.
func (s *session) start(room string) (env *script.Env, victim string, started bool, err error) {
	sc := s.w.scripts[room]
	if sc == nil {
		return nil, "", false, nil
	}
	if env := s.envs[room]; env != nil {
		return env, "", false, nil
	}
	if s.w.closed {
		return nil, "", false, script.ErrClosed
	}
	victim = s.victim(room)
	if s.client != "" && s.w.clientKept(s.client)-s.w.kept(s.envs[victim]) >= s.w.clientMemory {
		return nil, "", false, ErrClientFull
	}
	err = s.w.timed(s.client, func() (err error) {
		env, err = s.w.rt.Instance(sc)
		return err
	})
	if err != nil {
		return nil, "", false, err
	}
	if s.envs == nil {
		s.envs = map[string]*script.Env{}
	}
	s.envs[room] = env
	if s.client != "" {
		if s.w.clientEnvs[s.client] == nil {
			s.w.clientEnvs[s.client] = map[*script.Env]struct{}{}
		}
		s.w.clientEnvs[s.client][env] = struct{}{}
	}
	return env, victim, true, nil
}

// free releases the player's instance of room's script. The caller holds
// w.mu.
func (s *session) free(room string) {
	if env := s.envs[room]; env != nil {
		s.w.release(s.client, env)
		delete(s.envs, room)
	}
}

// MaxRoomInstances is how many room script instances one player keeps:
// coming back to one of the last rooms finds its globals, older rooms
// start over. State that must last goes into state.player (T-0023).
const MaxRoomInstances = 4

// victim is the room entered longest ago, other than room and the current
// one, whose instance gives way when the player already keeps
// MaxRoomInstances; "" when none needs to. The caller holds w.mu.
func (s *session) victim(room string) string {
	if len(s.envs) < MaxRoomInstances {
		return ""
	}
	for _, old := range s.visited {
		if s.envs[old] != nil && old != room && old != s.room {
			return old
		}
	}
	for old := range s.envs { // not in visited: should not happen, but never exceed the cap
		if old != room && old != s.room {
			return old
		}
	}
	return ""
}

// reset frees the player's instance of room's script, which keeps too much
// to run again; the next call starts a new one. The caller holds w.mu.
func (s *session) reset(room string) {
	if env := s.envs[room]; env != nil {
		s.drop()
		s.w.release(s.client, env)
		delete(s.envs, room)
	}
}

// drop cancels the pending hook and tells the client its question is
// gone. The caller holds w.mu.
func (s *session) drop() {
	if s.pending == nil {
		return
	}
	if !s.w.closed {
		s.w.rt.Cancel(s.pending)
	}
	s.out = append(s.out, ws.Message{Type: "close", ID: s.dialogID})
	s.unpend()
}

// unpend forgets the pending hook, stops its question's timeout and
// forgets its deadline. The caller holds w.mu.
func (s *session) unpend() {
	s.forgetQuestion()
	if s.dialogTimer != nil {
		s.dialogTimer()
		s.dialogTimer = nil
	}
	s.pending = nil
}
