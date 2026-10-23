package game

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// Player input to hooks: entering rooms, answering questions, typed lines
// and clicks, running a hook and turning its errors into what the player
// reads.

// enterLocked moves the player into room and runs its on_enter hook; without
// moved, the player has just joined. The caller holds w.mu. After room.go
// (moved), the output so far stays in front of the new room's, and the client is
// told the new room's items first, since its on_enter may not describe it.
// A move whose room cannot start, or whose on_enter cannot run yet because
// the player's address used up its Lua time, leaves the player where they
// were.
func (s *session) enterLocked(room string, moved bool) []ws.Message {
	s.drop()
	if s.w.closed {
		return s.after(nil, script.ErrClosed)
	}
	if !moved {
		s.place(room)
		if room != s.w.pack.Start {
			// A player who continues elsewhere learns the room's items as
			// after a move: its on_enter may not describe it.
			s.out = append(s.out, ws.Message{Type: "items", Items: wsItems(s.items)})
		}
		env, err := s.env(room)
		if err != nil {
			return s.after(nil, err)
		}
		if env == nil {
			s.DescribeRoom()
			return s.flush()
		}
		return s.after(s.run1(func() (*script.Thread, error) { return s.w.rt.RunHook(s, env, s.onEnter()) }))
	}
	env, victim, started, err := s.start(room)
	if err != nil {
		return s.refuseMove(room, err)
	}
	oldRoom, oldItems, oldVisited, oldArrived, mark := s.room, s.items, slices.Clone(s.visited), s.arrived, len(s.out)
	s.place(room)
	s.out = append(s.out, ws.Message{Type: "items", Items: wsItems(s.items)})
	if env == nil {
		s.DescribeRoom()
		return s.flush()
	}
	th, err := s.run1(func() (*script.Thread, error) { return s.w.rt.RunHook(s, env, s.onEnter()) })
	if errors.Is(err, ErrBusy) { // nothing ran: undo the move, and keep the old instances
		s.room, s.items, s.visited, s.arrived, s.out = oldRoom, oldItems, oldVisited, oldArrived, s.out[:mark]
		if started {
			s.free(room)
		}
		return s.after(th, err)
	}
	if victim != "" {
		s.free(victim)
	}
	return s.after(th, err)
}

// hook is h for the player: their name, id and GM role.
func (s *session) hook(h script.Hook) script.Hook {
	h.Player, h.PlayerID, h.GM = s.name, s.id, s.gm
	return h
}

// onEnter is the on_enter call for the player.
func (s *session) onEnter() script.Hook {
	return s.hook(script.Hook{Name: "on_enter", ReadOnlyWorld: s.readOnlyWorld})
}

// place puts the player in room with its YAML items, and shows them the
// room's timers instead of the old room's.
func (s *session) place(room string) {
	if room != s.room {
		s.moved = true
		s.showRoomTimers(s.room, room)
		s.w.arrivals++
		s.arrived = s.w.arrivals
	}
	s.room = room
	s.items = nil
	for _, it := range s.w.pack.Rooms[room].Items {
		s.items = append(s.items, script.Item{ID: it.ID, Label: it.Label})
	}
	s.visited = append(slices.DeleteFunc(s.visited, func(r string) bool { return r == room }), room)
}

// refuseMove tells the player a move to room did not happen. The caller
// holds w.mu.
func (s *session) refuseMove(room string, err error) []ws.Message {
	if errors.Is(err, ErrBusy) {
		s.out = append(s.out, ws.Message{Type: "error", Message: busyMessage})
		return s.flush()
	}
	s.w.log.Warn("room script failed", "room", room, "err", logText(err))
	s.out = append(s.out, ws.Message{Type: "error", Message: playerError(err)})
	return s.flush()
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
		return s.refused("no such option: " + err.Error())
	}
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
		return s.refused("That is too long: at most " + strconv.Itoa(MaxInputBytes) + " bytes.")
	}
	text := cleanInput(m.Text)
	if text == "" {
		return s.refused("Type an answer.")
	}
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
		return s.refused(busyMessage)
	}
	if errors.Is(err, script.ErrAnswer) {
		if s.pending.Dialog.Ask {
			return s.refused("Type an answer.")
		}
		return s.refused("no such option")
	}
	s.unpend()
	return s.after(th, err)
}

// refused answers an answer to the open question that was not taken: the
// error carries the question's id, which tells the client the question is
// still open and can be answered again.
func (s *session) refused(msg string) []ws.Message {
	return []ws.Message{{Type: "error", ID: s.dialogID, Message: msg}}
}

// input runs the room's on_input(player, text) hook with a typed line, or
// the slash command it is.
func (s *session) input(m ws.Message) []ws.Message {
	if len(m.Text) > MaxInputBytes {
		return []ws.Message{{Type: "error", Message: "That is too long: at most " + strconv.Itoa(MaxInputBytes) + " bytes."}}
	}
	text := cleanInput(m.Text)
	if text == "" {
		return nil
	}
	if isCommand(text) {
		return s.command(text)
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
	h := s.hook(script.Hook{Name: hook, Args: []string{arg}})
	return s.after(s.run1(func() (*script.Thread, error) { return s.w.rt.RunHook(s, env, h) }))
}

// after turns the end of a hook call into messages: the output so far, then
// the dialog it waits on, or an error the player can read.
func (s *session) after(th *script.Thread, err error) []ws.Message {
	if errors.Is(err, ErrBusy) {
		s.out = append(s.out, ws.Message{Type: "error", Message: busyMessage})
		return s.flush()
	}
	if err != nil {
		s.next = "" // a failed hook moves no one
		s.w.log.Warn("room script failed", "room", s.room, "err", logText(err))
		if errors.Is(err, script.ErrStateMemory) {
			s.reset(s.room)
		}
		s.out = append(s.out, ws.Message{Type: "error", Message: playerError(err)})
		return s.flush()
	}
	if th != nil {
		s.dialogs++
		s.pending = th
		s.dialogID = "d" + strconv.Itoa(s.dialogs)
		d := th.Dialog
		m := ws.Message{Type: "dialog", ID: s.dialogID, Title: d.Title, Options: d.Options, Speak: d.Voice.Speak, Hide: d.Voice.Hide, From: d.Voice.From}
		switch {
		case d.Multiple:
			m.Type, m.Min, m.Max = "select", d.Min, d.Max
		case d.Ask:
			m.Type, m.Strict = "ask", d.Strict
		}
		if d.Timeout > 0 {
			m.Ms = d.Timeout.Milliseconds()
			s.expireAfter(d.Timeout)
		}
		s.out = append(s.out, m)
	} else if s.next != "" {
		room := s.next
		s.next = ""
		if s.moves++; s.moves > MaxMoves {
			s.w.log.Warn("room script moved a player too often", "room", s.room, "to", room)
			s.out = append(s.out, ws.Message{Type: "error", Message: "This room's script moved you too often at once. Tell the game master."})
			return s.flush()
		}
		return s.enterLocked(room, true)
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
	case errors.Is(err, script.ErrFull):
		return "This world is full right now. Try again later."
	case errors.Is(err, ErrClientFull):
		return "Too many players from your network are in this world right now. Try again later."
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
