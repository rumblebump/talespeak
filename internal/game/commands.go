package game

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Slash commands (T-0011): a typed line that starts with "/" never reaches
// on_input. The engine's own commands come first: /help for everyone, and
// the game master's tools. Every other name is a command of the room's
// script (script.Command), run by its on_command hook; a command the
// script marks gm is refused to everyone else. The engine answers with
// notice messages, whose look a pack's say cannot copy.

// Who a notice comes from.
const (
	NoticeServer = "server" // the engine: command answers, /help
	NoticeGM     = "gm"     // a game master, named in from
)

// MaxAuditShown is how many entries /audit shows.
const MaxAuditShown = 20

// broadcastBurst and broadcastGap limit a game master's commands that
// reach other players (/say, /reset): broadcastBurst at once, then one
// per broadcastGap, so a held-down key cannot flood them.
const (
	broadcastBurst = 5
	broadcastGap   = time.Second
)

// coreCommand is one of the engine's commands.
type coreCommand struct {
	name  string
	usage string // how to type it, for /help
	help  string
	gm    bool
}

// coreCommands are the engine's commands, in the order /help lists them.
// A room script's command of the same name is never used.
var coreCommands = []coreCommand{
	{name: "help", usage: "/help", help: "what you can type here"},
	{name: "players", usage: "/players", help: "who plays now, and where", gm: true},
	{name: "rooms", usage: "/rooms", help: "the rooms of this world", gm: true},
	{name: "go", usage: "/go ROOM", help: "go to a room yourself", gm: true},
	{name: "say", usage: "/say TEXT", help: "tell every player something, as the game master", gm: true},
	{name: "reset", usage: "/reset room [ROOM]", help: "start a room over for everyone in it", gm: true},
	{name: "audit", usage: "/audit", help: "what game masters did here last", gm: true},
}

func findCore(name string) *coreCommand {
	for i := range coreCommands {
		if coreCommands[i].name == name {
			return &coreCommands[i]
		}
	}
	return nil
}

// isCommand reports whether a typed line is a slash command.
func isCommand(text string) bool { return strings.HasPrefix(text, "/") }

// splitCommand splits "/reset room cell" into "reset" and "room cell".
func splitCommand(text string) (name, args string) {
	text = strings.TrimPrefix(text, "/")
	name, args, _ = strings.Cut(text, " ")
	return strings.ToLower(name), strings.TrimSpace(args)
}

// notice is a message from the engine to the player.
func notice(text string) ws.Message {
	return ws.Message{Type: "notice", Role: NoticeServer, Text: text}
}

// gmNotice is a message of the game master s, or about what they did.
func (s *session) gmNotice(text string) ws.Message {
	return ws.Message{Type: "notice", Role: NoticeGM, From: s.name, Text: text}
}

// unknownCommand answers a command the player may not use as one that
// does not exist, so the game master's tools stay out of sight.
func unknownCommand(name string) []ws.Message {
	if !script.ValidCommand(name) {
		return []ws.Message{notice("That is not a command. Type /help to see what you can type.")}
	}
	return []ws.Message{notice("There is no command /" + name + " here. Type /help to see what you can type.")}
}

// command runs a slash command the player typed. The engine's commands
// also work while a question is open; a room's need it answered first, as
// on_input does. The caller holds w.mu.
func (s *session) command(text string) []ws.Message {
	name, args := splitCommand(text)
	if c := findCore(name); c != nil {
		if c.gm && !s.gm {
			return unknownCommand(name)
		}
		return s.runCore(name, args)
	}
	if !script.ValidCommand(name) {
		return unknownCommand(name)
	}
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	env, err := s.env(s.room)
	if err != nil {
		return s.after(nil, err)
	}
	cmd, ok := findCommand(env, name)
	if !ok || (cmd.GM && !s.gm) || !env.HasHook("on_command") {
		return unknownCommand(name)
	}
	if s.pending != nil {
		return []ws.Message{{Type: "error", Message: "Answer the open question first."}}
	}
	if cmd.GM {
		if err := s.audit("/"+name, args); err != nil {
			return []ws.Message{{Type: "error", Message: auditFailed}}
		}
		s.tellRoom(s.room, s.gmNotice(strings.TrimSpace("used /"+name+" "+args)))
	}
	h := s.hook(script.Hook{Name: "on_command", Args: []string{name, args}})
	return s.after(s.run1(func() (*script.Thread, error) { return s.w.rt.RunHook(s, env, h) }))
}

// runCore runs the engine's command name.
func (s *session) runCore(name, args string) []ws.Message {
	switch name {
	case "help":
		return s.helpCommand()
	case "players":
		return s.playersCommand()
	case "rooms":
		return s.roomsCommand()
	case "go":
		return s.goCommand(args)
	case "say":
		return s.sayCommand(args)
	case "reset":
		return s.resetCommand(args)
	default: // "audit"
		return s.auditCommand()
	}
}

// findCommand looks name up in the commands of env's script.
func findCommand(env *script.Env, name string) (script.Command, bool) {
	for _, c := range env.Commands() {
		if c.Name == name {
			return c, true
		}
	}
	return script.Command{}, false
}

// tellRoom pushes m to the other players in room. The caller holds w.mu.
func (s *session) tellRoom(room string, m ws.Message) {
	for _, p := range s.w.inRoom(room) {
		if p != s {
			p.push([]ws.Message{m})
		}
	}
}

// auditFailed answers a game master whose action the audit log could not
// record: it did not happen.
const auditFailed = "The server cannot record that right now, so it did not happen. Try again in a moment."

// audit records a game master's action before it happens. Without an
// audit store (tests and local tools) the world keeps the latest entries
// in memory. The caller holds w.mu.
func (s *session) audit(action, detail string) error {
	w := s.w
	if len(detail) > store.MaxAuditDetail {
		detail = detail[:store.MaxAuditDetail]
	}
	e := store.AuditEntry{World: w.worldID, Account: s.account, Name: s.name, Action: action, Detail: detail, At: w.now()}
	w.log.Info("game master action", "account", s.account, "player", s.id, "action", action, "detail", strconv.Quote(detail))
	if w.auditStore == nil {
		w.auditLog = append(w.auditLog, e)
		if n := len(w.auditLog) - MaxAuditShown; n > 0 {
			w.auditLog = slices.Delete(w.auditLog, 0, n)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
	defer cancel()
	if err := w.auditStore.AddAudit(ctx, e); err != nil {
		w.log.Error("audit log not written", "err", err)
		return err
	}
	return nil
}

// slowDown refuses a game master's broadcast past broadcastBurst at once
// and one per broadcastGap after. The caller holds w.mu.
func (s *session) slowDown() bool {
	now := s.w.now()
	if s.lastBroadcast.IsZero() {
		s.broadcasts = broadcastBurst
	} else {
		s.broadcasts = min(broadcastBurst, s.broadcasts+now.Sub(s.lastBroadcast).Seconds()/broadcastGap.Seconds())
	}
	s.lastBroadcast = now
	if s.broadcasts < 1 {
		return true
	}
	s.broadcasts--
	return false
}

const slowDownMessage = "Wait a moment before the next one."

// helpCommand lists the commands the player may use: the engine's, then
// the room's.
func (s *session) helpCommand() []ws.Message {
	var b strings.Builder
	b.WriteString("You can type what you do, or one of these:")
	for _, c := range coreCommands {
		if !c.gm || s.gm {
			b.WriteString("\n" + c.usage + ": " + c.help)
		}
	}
	if s.gm {
		b.WriteString("\n(You are a game master here: the other players see a mark when you act.)")
	}
	var env *script.Env
	if !s.w.closed {
		env = s.envs[s.room] // never starts one: /help runs no Lua
	}
	first := true
	for _, c := range env.Commands() {
		if (c.GM && !s.gm) || findCore(c.Name) != nil {
			continue
		}
		if first {
			b.WriteString("\nIn this room:")
			first = false
		}
		line := "\n/" + c.Name
		if c.Help != "" {
			line += ": " + c.Help
		}
		if c.GM {
			line += " (game master)"
		}
		b.WriteString(line)
	}
	return []ws.Message{notice(b.String())}
}

func (s *session) playersCommand() []ws.Message {
	list := s.w.online()
	var b strings.Builder
	b.WriteString(strconv.Itoa(len(list)) + " online:")
	for _, p := range list {
		b.WriteString("\n" + p.name + " in " + s.roomTitle(p.room))
		if p.gm {
			b.WriteString(" (game master)")
		}
	}
	return []ws.Message{notice(b.String())}
}

// roomTitle names room for the game master: its title and id.
func (s *session) roomTitle(room string) string {
	if r := s.w.pack.Rooms[room]; r != nil && r.Title != "" {
		return r.Title + " (" + room + ")"
	}
	return room
}

func (s *session) roomsCommand() []ws.Message {
	var b strings.Builder
	b.WriteString("Rooms (type /go and the name in brackets):")
	for _, id := range slices.Sorted(maps.Keys(s.w.pack.Rooms)) {
		b.WriteString("\n" + s.roomTitle(id))
		if id == s.room {
			b.WriteString(" (you are here)")
		}
	}
	return []ws.Message{notice(b.String())}
}

func (s *session) goCommand(args string) []ws.Message {
	room := strings.ToLower(args)
	if s.w.pack.Rooms[room] == nil {
		return []ws.Message{notice("Type /go and one of the rooms /rooms lists.")}
	}
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	if err := s.audit("go", room); err != nil {
		return []ws.Message{{Type: "error", Message: auditFailed}}
	}
	s.next = ""
	s.moves = 0
	return s.enterLocked(room, true)
}

func (s *session) sayCommand(args string) []ws.Message {
	if args == "" {
		return []ws.Message{notice("Type /say and what every player should read.")}
	}
	if s.slowDown() {
		return []ws.Message{notice(slowDownMessage)}
	}
	if err := s.audit("say", args); err != nil {
		return []ws.Message{{Type: "error", Message: auditFailed}}
	}
	m := s.gmNotice(args)
	for _, p := range s.w.online() {
		if p != s {
			p.push([]ws.Message{m})
		}
	}
	return []ws.Message{m}
}

func (s *session) resetCommand(args string) []ws.Message {
	what, room, _ := strings.Cut(args, " ")
	room = strings.ToLower(strings.TrimSpace(room))
	if strings.ToLower(what) != "room" {
		return []ws.Message{notice("Type /reset room to start the room you are in over, or /reset room and a room from /rooms.")}
	}
	if room == "" {
		room = s.room
	}
	if s.w.pack.Rooms[room] == nil {
		return []ws.Message{notice("There is no room " + strconv.Quote(room) + ". /rooms lists them.")}
	}
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	if s.slowDown() {
		return []ws.Message{notice(slowDownMessage)}
	}
	if err := s.audit("reset room", room); err != nil {
		return []ws.Message{{Type: "error", Message: auditFailed}}
	}
	return s.w.resetRoom(s, room)
}

// resetRoom starts room over for every player in it, for the game master
// gm: its room timers stop, every player's on_reset runs (where the pack
// clears what it keeps of the room in state.player and state.world), their
// instance of the room's script starts over and on_enter runs again. A
// question they had open closes. Players elsewhere keep what they have.
// It returns gm's messages. The caller holds w.mu.
func (w *World) resetRoom(gm *session, room string) []ws.Message {
	for _, name := range slices.Sorted(maps.Keys(w.roomTimers[room])) {
		t := w.roomTimers[room][name]
		t.stop()
		w.dropRoomTimer(room, name)
		if t.show != "" {
			w.timerNews[roomTimerKey{room, name}] = struct{}{}
		}
	}
	text := "started this room over."
	var mine []ws.Message
	if gm.room != room {
		mine = []ws.Message{gm.gmNotice("started " + gm.roomTitle(room) + " over for the " + strconv.Itoa(len(w.inRoom(room))) + " players in it.")}
	}
	for _, p := range w.inRoom(room) {
		p.drop() // the question goes first, then the mark
		p.out = append(p.out, gm.gmNotice(text))
		out := p.restart(room)
		if p == gm {
			mine = append(mine, out...)
		} else {
			p.push(out)
		}
	}
	if gm.room != room {
		mine = append(mine, gm.flush()...) // the timers it stopped, and what on_reset changed
	}
	return mine
}

// restart is resetRoom for one player: on_reset, then a new instance of
// room's script and on_enter. on_reset cannot ask, move the player or be
// refused for want of Lua time; its time is charged all the same. The
// caller holds w.mu.
func (s *session) restart(room string) []ws.Message {
	w := s.w
	s.drop()
	s.moves = 0
	if env, err := s.env(room); err != nil {
		w.log.Warn("room script failed", "room", room, "hook", "on_reset", "err", logText(err))
	} else if env != nil && env.HasHook("on_reset") {
		h := s.hook(script.Hook{Name: "on_reset", NoWait: true})
		err := w.charged(s.client, func() error {
			_, err := w.rt.RunHook(s, env, h)
			return err
		})
		if err != nil {
			w.log.Warn("room script failed", "room", room, "hook", "on_reset", "err", logText(err))
		}
	}
	s.next = ""
	s.free(room)
	return s.enterLocked(room, false)
}

func (s *session) auditCommand() []ws.Message {
	w := s.w
	var list []store.AuditEntry
	if w.auditStore == nil {
		list = slices.Clone(w.auditLog)
		slices.Reverse(list)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
		defer cancel()
		var err error
		if list, err = w.auditStore.AuditLog(ctx, w.worldID, MaxAuditShown); err != nil {
			w.log.Error("audit log not read", "err", err)
			return []ws.Message{{Type: "error", Message: "The server cannot read the log right now. Try again in a moment."}}
		}
	}
	if len(list) == 0 {
		return []ws.Message{notice("No game master has done anything here yet.")}
	}
	var b strings.Builder
	b.WriteString("What game masters did here last, the latest first (times in UTC):")
	for _, e := range list {
		b.WriteString("\n" + e.At.UTC().Format("2006-01-02 15:04") + " " + e.Name + ": " + e.Action)
		if e.Detail != "" {
			b.WriteString(" " + strconv.Quote(e.Detail))
		}
	}
	return []ws.Message{notice(b.String())}
}

// RoleChanged ends the sessions of account whose role differs from role,
// its role in the world now ("" when it is no member any more): they get
// a last error and their connections close, and the client connects again
// with the new role (or finds it may no longer play). It reports how many
// ended.
func (w *World) RoleChanged(account int64, role string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for s := range w.sessions {
		if s.account == 0 || s.account != account || (role != "" && s.gm == (role == store.MemberGM)) {
			continue
		}
		s.end()
		s.lagged = true // Pending reports the session over: the connection closes
		s.queue, s.queueBytes = []ws.Message{{Type: "error", Message: roleChangedMessage}}, 0
		if s.notify != nil {
			s.notify()
		}
		n++
	}
	return n
}

// roleChangedMessage is the last message of a session whose role in the
// world changed.
const roleChangedMessage = "Your role in this world has changed. Connecting again…"
