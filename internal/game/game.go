// Package game runs a world: one story pack, its Lua runtime and the players
// connected to it. It turns WebSocket messages into Lua hook calls and the
// hooks' output back into messages.
package game

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// DefaultPlayerName is used until accounts exist (T-0010).
const DefaultPlayerName = "traveler"

// MaxInputBytes bounds one line the player types.
const MaxInputBytes = 500

// DefaultClientRate and DefaultClientBurst share a world's Lua time between
// client addresses: one address may use DefaultClientBurst at once, then
// DefaultClientRate per second. Every typed line and click runs a hook under
// the world's lock, so without this one address with many connections could
// keep the world busy for everyone. A class behind one NAT address stays far
// below it with ordinary packs.
const (
	DefaultClientRate  = 250 * time.Millisecond
	DefaultClientBurst = 2 * time.Second
)

// ErrBusy is returned when a client address has used up its Lua time.
var ErrBusy = errors.New("this address used up its script time")

// maxBudgets bounds the per-client budget map, as in package ws.
const maxBudgets = 10000

// Option configures a World.
type Option func(*World)

// WithClientBudget sets how much Lua time one client address may use: burst
// at once, then rate per second.
func WithClientBudget(rate, burst time.Duration) Option {
	return func(w *World) { w.rate, w.burst = rate.Seconds(), burst.Seconds() }
}

// World is one running game. Its Lua state is used by one session at a time.
//
// Every player runs their own instance of each room script, with their own
// globals: nothing a player types or does reaches another player through
// script variables. State that lasts or is shared goes to the database
// (T-0018).
type World struct {
	log  *slog.Logger
	pack *pack.Pack

	mu      sync.Mutex // guards rt, budgets and closed, and every Lua call: gopher-lua states are not goroutine safe
	rt      *script.Runtime
	scripts map[string]*script.Script // room id -> its compiled script
	closed  bool

	rate, burst float64            // Lua seconds per second and at once, per client
	budgets     map[string]*budget // client -> Lua time left
	now         func() time.Time
}

// budget is a token bucket of Lua time in seconds. It may go below zero
// after a long call; the client then waits until it is positive again.
type budget struct {
	left float64
	last time.Time
}

// NewWorld compiles every room script of p and runs each top level once, so
// a script that fails fails the world at start, with the room's file in the
// error.
func NewWorld(p *pack.Pack, limits script.Limits, log *slog.Logger, opts ...Option) (*World, error) {
	w := &World{log: log, pack: p, rt: script.New(limits), scripts: map[string]*script.Script{},
		budgets: map[string]*budget{}, now: time.Now}
	WithClientBudget(DefaultClientRate, DefaultClientBurst)(w)
	for _, o := range opts {
		o(w)
	}
	for id, room := range p.Rooms {
		if room.Source == "" {
			continue
		}
		sc, err := w.rt.Compile("scripts/"+room.Script, room.Source)
		if err == nil {
			var env *script.Env
			if env, err = w.rt.Instance(sc); err == nil {
				w.rt.Release(env)
			}
		}
		if err != nil {
			w.rt.Close()
			return nil, fmt.Errorf("%s: %w", room.File, err)
		}
		w.scripts[id] = sc
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

// NewSession places a new player in the start room. client is the
// player's address key for the Lua time budget; "" has no budget (tests and
// local tools).
func (w *World) NewSession(client string) (ws.Session, []ws.Message) {
	s := &session{w: w, name: DefaultPlayerName, client: client}
	return s, s.enter(w.pack.Start)
}

// timed runs f, a call into Lua, and charges its time to client. It
// refuses with ErrBusy while the client's budget is used up. The caller
// holds w.mu.
func (w *World) timed(client string, f func() error) error {
	if client == "" {
		return f()
	}
	now := w.now()
	b := w.budgets[client]
	if b == nil {
		w.pruneBudgets(now)
		b = &budget{left: w.burst, last: now}
		w.budgets[client] = b
	}
	b.left = min(w.burst, b.left+now.Sub(b.last).Seconds()*w.rate)
	b.last = now
	if b.left <= 0 {
		return ErrBusy
	}
	err := f()
	end := w.now()
	b.left -= end.Sub(now).Seconds()
	b.last = end
	return err
}

// pruneBudgets keeps the map bounded: past maxBudgets, budgets that have
// refilled are dropped, then any. The caller holds w.mu.
func (w *World) pruneBudgets(now time.Time) {
	if len(w.budgets) < maxBudgets {
		return
	}
	for k, b := range w.budgets {
		if b.left+now.Sub(b.last).Seconds()*w.rate >= w.burst {
			delete(w.budgets, k)
		}
	}
	for k := range w.budgets {
		if len(w.budgets) < maxBudgets {
			break
		}
		delete(w.budgets, k)
	}
}

// session is one player. Its methods are called from the connection's
// goroutine only.
type session struct {
	w      *World
	name   string
	client string
	room   string

	envs     map[string]*script.Env // room id -> this player's instance of its script
	pending  *script.Thread         // the hook waiting in ui.dialog, if any
	dialogID string
	dialogs  int           // dialogs shown, for unique ids
	items    []script.Item // what the player can click now

	out []ws.Message // collects output during one hook call
}

func (s *session) Handle(m ws.Message) []ws.Message {
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

// Close ends the hook waiting on this player, if any, and frees the
// player's script instances.
func (s *session) Close() {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	s.drop()
	if !s.w.closed {
		for _, env := range s.envs {
			s.w.rt.Release(env)
		}
	}
	s.envs = nil
}

// env returns the player's instance of room's script, starting it on first
// use; nil when the room has no script. The caller holds w.mu.
func (s *session) env(room string) (*script.Env, error) {
	sc := s.w.scripts[room]
	if sc == nil {
		return nil, nil
	}
	if env := s.envs[room]; env != nil {
		return env, nil
	}
	if s.w.closed {
		return nil, script.ErrClosed
	}
	var env *script.Env
	err := s.w.timed(s.client, func() (err error) {
		env, err = s.w.rt.Instance(sc)
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.envs == nil {
		s.envs = map[string]*script.Env{}
	}
	s.envs[room] = env
	return env, nil
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
	s.items = nil
	for _, it := range s.w.pack.Rooms[room].Items {
		s.items = append(s.items, script.Item{ID: it.ID, Label: it.Label})
	}
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	env, err := s.env(room)
	if err != nil {
		return s.after(nil, err)
	}
	if env == nil {
		s.DescribeRoom()
		return s.flush()
	}
	return s.after(s.run1(func() (*script.Thread, error) { return s.w.rt.Run(s, env, "on_enter", s.name) }))
}

// choose answers the open dialog: option for ui.dialog, choices for
// ui.select.
func (s *session) choose(m ws.Message) []ws.Message {
	if s.pending == nil || m.ID != s.dialogID {
		return []ws.Message{{Type: "error", Message: "no such dialog"}}
	}
	if s.pending.Dialog.Ask {
		return []ws.Message{{Type: "error", Message: "this question wants a typed answer"}}
	}
	choices := m.Choices
	if !s.pending.Dialog.Multiple {
		choices = []int{m.Option}
	}
	if _, err := s.pending.Dialog.Check(choices); err != nil {
		return []ws.Message{{Type: "error", Message: "no such option: " + err.Error()}}
	}
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	return s.resumed(s.run1(func() (*script.Thread, error) { return s.w.rt.Resume(s, s.pending, choices...) }))
}

// answer answers the open ui.ask with a typed line.
func (s *session) answer(m ws.Message) []ws.Message {
	if s.pending == nil || m.ID != s.dialogID || !s.pending.Dialog.Ask {
		return []ws.Message{{Type: "error", Message: "no such question"}}
	}
	if len(m.Text) > MaxInputBytes {
		return []ws.Message{{Type: "error", Message: "That is too long: at most " + strconv.Itoa(MaxInputBytes) + " bytes."}}
	}
	text := cleanInput(m.Text)
	if text == "" {
		return []ws.Message{{Type: "error", Message: "Type an answer."}}
	}
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	return s.resumed(s.run1(func() (*script.Thread, error) { return s.w.rt.Answer(s, s.pending, text) }))
}

// run1 runs f, a hook call, within the player's Lua time budget. The
// caller holds w.mu.
func (s *session) run1(f func() (*script.Thread, error)) (*script.Thread, error) {
	var th *script.Thread
	err := s.w.timed(s.client, func() (err error) {
		th, err = f()
		return err
	})
	return th, err
}

// resumed handles the end of Resume or Answer. An answer the runtime did
// not accept leaves the hook paused and the question open; after anything
// else the old hook is done. The caller holds w.mu.
func (s *session) resumed(th *script.Thread, err error) []ws.Message {
	if errors.Is(err, ErrBusy) { // nothing ran: the question stays open
		return []ws.Message{{Type: "error", Message: busyMessage}}
	}
	if errors.Is(err, script.ErrAnswer) {
		if s.pending.Dialog.Ask {
			return []ws.Message{{Type: "error", Message: "Type an answer."}}
		}
		return []ws.Message{{Type: "error", Message: "no such option"}}
	}
	s.pending = nil
	return s.after(th, err)
}

// input runs the room's on_input(player, text) hook with a typed line.
func (s *session) input(m ws.Message) []ws.Message {
	if len(m.Text) > MaxInputBytes {
		return []ws.Message{{Type: "error", Message: "That is too long: at most " + strconv.Itoa(MaxInputBytes) + " bytes."}}
	}
	text := cleanInput(m.Text)
	if text == "" {
		return nil
	}
	return s.run("on_input", text)
}

// cleanInput turns control characters, newlines included, into spaces and
// trims the line, so a script only ever sees one line of text.
func cleanInput(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(text, "\uFFFD"))
	return strings.TrimSpace(text)
}

// click runs the room's on_click(player, id) hook for an item the player
// can see.
func (s *session) click(m ws.Message) []ws.Message {
	for _, it := range s.items {
		if it.ID == m.Item {
			return s.run("on_click", it.ID)
		}
	}
	return []ws.Message{{Type: "error", Message: "no such item"}}
}

// run calls hook(player, arg) of the current room. While a dialog is open,
// the player answers it first.
func (s *session) run(hook, arg string) []ws.Message {
	if s.pending != nil {
		return []ws.Message{{Type: "error", Message: "Answer the open question first."}}
	}
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	env, err := s.env(s.room)
	if err != nil {
		return s.after(nil, err)
	}
	if env == nil || !env.HasHook(hook) {
		return []ws.Message{{Type: "text", Text: "Nothing happens."}}
	}
	return s.after(s.run1(func() (*script.Thread, error) { return s.w.rt.Run(s, env, hook, s.name, arg) }))
}

// after turns the end of a hook call into messages: the output so far, then
// the dialog it waits on, or an error the player can read.
func (s *session) after(th *script.Thread, err error) []ws.Message {
	if errors.Is(err, ErrBusy) {
		s.out = append(s.out, ws.Message{Type: "error", Message: busyMessage})
		return s.flush()
	}
	if err != nil {
		s.w.log.Warn("room script failed", "room", s.room, "err", logText(err))
		s.out = append(s.out, ws.Message{Type: "error", Message: playerError(err)})
		return s.flush()
	}
	if th != nil {
		s.dialogs++
		s.pending = th
		s.dialogID = "d" + strconv.Itoa(s.dialogs)
		d := th.Dialog
		m := ws.Message{Type: "dialog", ID: s.dialogID, Title: d.Title, Options: d.Options, Speak: d.Voice.Speak, Hide: d.Voice.Hide}
		switch {
		case d.Multiple:
			m.Type, m.Min, m.Max = "select", d.Min, d.Max
		case d.Ask:
			m.Type, m.Strict = "ask", d.Strict
		}
		s.out = append(s.out, m)
	}
	return s.flush()
}

// busyMessage answers a player whose address used up its Lua time.
const busyMessage = "Too much at once: wait a moment, then try again."

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

// Say, DescribeRoom, Items and Image implement script.Host.
func (s *session) Say(text string, how script.Voice) {
	s.out = append(s.out, ws.Message{Type: "text", Text: text, Speak: how.Speak, Hide: how.Hide})
}

func (s *session) DescribeRoom() {
	r := s.w.pack.Rooms[s.room]
	s.out = append(s.out, ws.Message{Type: "room", ID: r.ID, Title: r.Title, Text: r.Description, Items: wsItems(s.items)})
}

func (s *session) Items(items []script.Item) {
	s.items = items
	s.out = append(s.out, ws.Message{Type: "items", Items: wsItems(items)})
}

func (s *session) Image(name, caption string) bool {
	url, ok := s.w.pack.Image(name)
	if ok {
		s.out = append(s.out, ws.Message{Type: "image", URL: url, Text: caption})
	}
	return ok
}

func wsItems(items []script.Item) []ws.Item {
	out := make([]ws.Item, len(items))
	for i, it := range items {
		out[i] = ws.Item{ID: it.ID, Label: it.Label}
	}
	return out
}
