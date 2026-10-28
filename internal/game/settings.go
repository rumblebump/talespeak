package game

import (
	"strconv"
	"strings"

	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// /set (T-0018): the game master lists the world's settings (pack.yaml
// settings) and changes one. A setting is a key of state.game, so scripts
// read it with state.game.get(name). Every change is in the audit log,
// and every player hears of it. Like the other GM commands, runCore only
// calls it in GM mode, with w.mu held.

func (s *session) setCommand(args string) []ws.Message {
	w := s.w
	if len(w.pack.Settings) == 0 {
		return []ws.Message{notice("This world's pack has no settings.")}
	}
	name, value, _ := strings.Cut(strings.ToLower(args), " ")
	value = strings.TrimSpace(value)
	if name == "" {
		return []ws.Message{notice(s.settingsText())}
	}
	st, ok := w.pack.Setting(name)
	if !ok {
		return []ws.Message{notice("There is no setting " + strconv.Quote(shorten(name, pack.MaxSettingName)) + ". /set lists them.")}
	}
	now, _ := w.game.String(st.Name)
	if value == "" {
		return []ws.Message{notice(settingLine(st, now))}
	}
	if !st.ValidChoice(value) {
		return []ws.Message{notice(st.Name + " takes one of: " + strings.Join(st.Choices, ", ") + ".")}
	}
	if value == now {
		return []ws.Message{notice(st.Name + " is " + value + " already.")}
	}
	if w.closed {
		return s.after(nil, script.ErrClosed)
	}
	if s.slowDown() {
		return []ws.Message{notice(slowDownMessage)}
	}
	if err := s.audit("set", st.Name+" "+value); err != nil {
		return []ws.Message{{Type: "error", Message: auditFailed}}
	}
	if err := w.game.SetString(st.Name, value); err != nil {
		w.log.Warn("setting not changed", "setting", st.Name, "err", err)
		return []ws.Message{notice("The setting could not be changed: " + err.Error() + ".")}
	}
	w.save(s)
	m := s.gmNotice("set " + strconv.Quote(st.Label) + " to " + value + ".")
	for _, p := range w.online() {
		if p != s {
			p.push([]ws.Message{m})
		}
	}
	return []ws.Message{m}
}

// settingsText lists the world's settings with their values now.
func (s *session) settingsText() string {
	var b strings.Builder
	b.WriteString("Settings of this world (type /set NAME VALUE to change one):")
	for _, st := range s.w.pack.Settings {
		now, _ := s.w.game.String(st.Name)
		b.WriteString("\n" + settingLine(st, now))
	}
	return b.String()
}

// settingLine is one setting as /set shows it: its name, label, value
// and choices.
func settingLine(st pack.Setting, now string) string {
	return st.Name + " (" + st.Label + "): " + now + "; choices: " + strings.Join(st.Choices, ", ")
}
