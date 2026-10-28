package account

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/store"
)

// World settings (T-0018): the choices a pack offers each world
// (pack.yaml settings), shown in the lobby's New world form and picked
// when the world is created. They become the world's first state.game,
// where scripts read them and game masters change them with /set.

// Setting is one of a pack's settings as the lobby shows it.
type Setting struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Choices []string `json:"choices"`
	Default string   `json:"default"`
}

// settingsState checks what a New world form picked against the pack's
// settings and returns every setting's value, a pick or its default, as
// the world's first state.game. A name the pack does not have, or a value
// that is not one of its choices, is refused.
func settingsState(p Pack, picked map[string]string) ([]store.Change, error) {
	for name, v := range picked {
		i := slices.IndexFunc(p.Settings, func(s Setting) bool { return s.Name == name })
		if i < 0 {
			return nil, fail(http.StatusBadRequest, "This pack has no setting "+strconv.Quote(cutText(name, 32))+".")
		}
		if st := p.Settings[i]; !slices.Contains(st.Choices, v) {
			return nil, fail(http.StatusBadRequest, "Pick one of the choices for "+st.Label+": "+strings.Join(st.Choices, ", ")+".")
		}
	}
	var out []store.Change
	for _, st := range p.Settings {
		v, ok := picked[st.Name]
		if !ok {
			v = st.Default
		}
		out = append(out, store.Change{Key: st.Name, Value: script.EncodeString(v)})
	}
	return out, nil
}

// cutText shortens s to at most n bytes for a message.
func cutText(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
