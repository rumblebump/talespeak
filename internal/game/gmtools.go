package game

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// The game master's commands (T-0011): /players, /rooms, /go, /say,
// /reset and /audit, and what happens when an account's role in the world
// changes. The commands do not check player.gm themselves: runCore only
// calls them after command has checked it (commands.go). Like every
// session method here, they run with w.mu held.

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
		out := p.restart(room, gm)
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
// refused for want of Lua time; its time is charged all the same, to the
// game master by who asked for it. The caller holds w.mu.
func (s *session) restart(room string, by *session) []ws.Message {
	w := s.w
	s.drop()
	s.moves = 0
	if env, err := s.env(room); err != nil {
		w.log.Warn("room script failed", "room", room, "hook", "on_reset", "err", logText(err))
	} else if env != nil && env.HasHook("on_reset") {
		h := s.hook(script.Hook{Name: "on_reset", NoWait: true})
		err := w.charged(by.client, func() error {
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
