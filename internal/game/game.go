// Package game runs a world: one story pack, its Lua runtime and the players
// connected to it. It turns WebSocket messages into Lua hook calls and the
// hooks' output back into messages.
package game

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// DefaultPlayerName is used until accounts exist (T-0010).
const DefaultPlayerName = "traveler"

// World is one running game. Its Lua state is used by one session at a time.
type World struct {
	log  *slog.Logger
	pack *pack.Pack

	mu     sync.Mutex // guards rt, envs and closed: gopher-lua states are not goroutine safe
	rt     *script.Runtime
	envs   map[string]*script.Env // room id -> its script's globals
	closed bool
}

// NewWorld loads every room script of p. A script that fails to load fails
// the world, with the room's file in the error.
func NewWorld(p *pack.Pack, limits script.Limits, log *slog.Logger) (*World, error) {
	w := &World{log: log, pack: p, rt: script.New(limits), envs: map[string]*script.Env{}}
	for id, room := range p.Rooms {
		if room.Source == "" {
			continue
		}
		env, err := w.rt.Load("scripts/"+room.Script, room.Source)
		if err != nil {
			w.rt.Close()
			return nil, fmt.Errorf("%s: %w", room.File, err)
		}
		w.envs[id] = env
	}
	return w, nil
}

// Close ends every paused hook and frees the world's Lua state. Sessions
// still connected get an error for anything they send.
func (w *World) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.rt.Close()
}

// NewSession places a new player in the start room.
func (w *World) NewSession() (ws.Session, []ws.Message) {
	s := &session{w: w, name: DefaultPlayerName}
	return s, s.enter(w.pack.Start)
}

// session is one player. Its methods are called from the connection's
// goroutine only.
type session struct {
	w    *World
	name string
	room string

	pending  *script.Thread // the hook waiting in ui.dialog, if any
	dialogID string
	dialogs  int // dialogs shown, for unique ids

	out []ws.Message // collects output during one hook call
}

func (s *session) Handle(m ws.Message) []ws.Message {
	switch m.Type {
	case "choose":
		return s.choose(m)
	default:
		return []ws.Message{{Type: "error", Message: "unknown message type"}}
	}
}

// Close ends the hook waiting on this player, if any.
func (s *session) Close() {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	s.drop()
}

// drop cancels the pending hook. The caller holds w.mu.
func (s *session) drop() {
	if s.pending != nil && !s.w.closed {
		s.w.rt.Cancel(s.pending)
	}
	s.pending = nil
}

// enter moves the player into room and runs its on_enter hook.
func (s *session) enter(room string) []ws.Message {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	s.drop()
	s.room = room
	env := s.w.envs[room]
	if env == nil {
		s.DescribeRoom()
		return s.flush()
	}
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	th, err := s.w.rt.Run(s, env, "on_enter", s.name)
	return s.after(th, err)
}

// choose answers the open dialog.
func (s *session) choose(m ws.Message) []ws.Message {
	if s.pending == nil || m.ID != s.dialogID {
		return []ws.Message{{Type: "error", Message: "no such dialog"}}
	}
	if m.Option < 1 || m.Option > len(s.pending.Dialog.Options) {
		return []ws.Message{{Type: "error", Message: "no such option"}}
	}
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	th := s.pending
	s.pending = nil
	th, err := s.w.rt.Resume(s, th, m.Option)
	return s.after(th, err)
}

// after turns the end of a hook call into messages: the output so far, then
// the dialog it waits on, or an error the player can read.
func (s *session) after(th *script.Thread, err error) []ws.Message {
	if err != nil {
		s.w.log.Warn("room script failed", "room", s.room, "err", logText(err))
		s.out = append(s.out, ws.Message{Type: "error", Message: playerError(err)})
		return s.flush()
	}
	if th != nil {
		s.dialogs++
		s.pending = th
		s.dialogID = "d" + strconv.Itoa(s.dialogs)
		s.out = append(s.out, ws.Message{Type: "dialog", ID: s.dialogID, Title: th.Dialog.Title, Options: th.Dialog.Options})
	}
	return s.flush()
}

// playerError says what went wrong without script details; the log has them.
func playerError(err error) string {
	switch {
	case errors.Is(err, script.ErrClosed):
		return "This world has closed."
	case errors.Is(err, script.ErrTimeout), errors.Is(err, script.ErrMemory), errors.Is(err, script.ErrStateMemory):
		return "This room's script hit its limits. Tell the game master."
	default:
		return "This room's script failed. Tell the game master."
	}
}

// maxLogBytes caps a script error in the log: error() messages are chosen
// by the script and could be megabytes, or contain fake log lines.
const maxLogBytes = 500

// logText is err's message cut to maxLogBytes and quoted, so newlines and
// control characters show as escapes.
func logText(err error) string {
	msg := err.Error()
	if len(msg) > maxLogBytes {
		msg = msg[:maxLogBytes] + "..."
	}
	return strconv.Quote(msg)
}

func (s *session) flush() []ws.Message {
	out := s.out
	s.out = nil
	return out
}

// Say and DescribeRoom implement script.Host.
func (s *session) Say(text string) { s.out = append(s.out, ws.Message{Type: "text", Text: text}) }

func (s *session) DescribeRoom() {
	r := s.w.pack.Rooms[s.room]
	s.out = append(s.out, ws.Message{Type: "room", ID: r.ID, Title: r.Title, Text: r.Description})
}
