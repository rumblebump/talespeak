package game

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	gm    bool // only in GM mode
	// member is set for a command of every game master of the world, in
	// GM mode or not: /gm.
	member bool
}

// coreCommands are the engine's commands, in the order /help lists them.
// A room script's command of the same name is never used.
var coreCommands = []coreCommand{
	{name: "help", usage: "/help", help: "what you can type here"},
	{name: "gm", usage: "/gm [on|off]", help: "switch GM mode on or off", member: true},
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
		if (c.gm && !s.gm) || (c.member && !s.member) {
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
		if s.slowDown() {
			return []ws.Message{notice(slowDownMessage)}
		}
		if err := s.audit("/"+name, args); err != nil {
			return []ws.Message{{Type: "error", Message: auditFailed}}
		}
		s.tellRoom(s.room, s.gmNotice(strings.TrimSpace("used /"+name+" "+shorten(args, maxMarkArgs))))
	}
	h := s.hook(script.Hook{Name: "on_command", Args: []string{name, args}})
	return s.after(s.run1(func() (*script.Thread, error) { return s.w.rt.RunHook(s, env, h) }))
}

// runCore runs the engine's command name.
func (s *session) runCore(name, args string) []ws.Message {
	switch name {
	case "help":
		return s.helpCommand()
	case "gm":
		return s.gmModeCommand(args)
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
	case "audit":
		return s.auditCommand()
	default:
		return unknownCommand(name)
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
	detail = store.AuditDetail(detail)
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
// and one per broadcastGap after. The count belongs to the account (the
// player without one), so connecting again does not refill it. The
// caller holds w.mu.
func (s *session) slowDown() bool {
	w := s.w
	key := "p" + s.id
	if s.account != 0 {
		key = "a" + strconv.FormatInt(s.account, 10)
	}
	now := w.now()
	b := w.broadcasts[key]
	if b == nil {
		if w.broadcasts == nil {
			w.broadcasts = make(map[string]*budget)
		}
		for k, old := range w.broadcasts { // full again: forget it
			if now.Sub(old.last) >= broadcastBurst*broadcastGap {
				delete(w.broadcasts, k)
			}
		}
		b = &budget{left: broadcastBurst}
		w.broadcasts[key] = b
	} else {
		b.left = min(broadcastBurst, b.left+now.Sub(b.last).Seconds()/broadcastGap.Seconds())
	}
	b.last = now
	if b.left < 1 {
		return true
	}
	b.left--
	return false
}

const slowDownMessage = "Wait a moment before the next one."

// maxMarkArgs is how much of a room command's arguments the mark the
// other players see shows; the audit log keeps them all.
const maxMarkArgs = 60

// shorten cuts s to at most n bytes, on a letter's boundary, marking the cut.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// helpCommand lists the commands the player may use: the engine's, then
// the room's.
func (s *session) helpCommand() []ws.Message {
	var b strings.Builder
	b.WriteString("You can type what you do, or one of these:")
	for _, c := range coreCommands {
		if (!c.gm || s.gm) && (!c.member || s.member) {
			b.WriteString("\n" + c.usage + ": " + c.help)
		}
	}
	switch {
	case s.gm:
		b.WriteString("\n(GM mode is on: the other players see a mark when you act.)")
	case s.member:
		b.WriteString("\n(You are a game master here, playing as a player: /gm switches GM mode on.)")
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
