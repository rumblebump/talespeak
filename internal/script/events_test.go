package script

import "testing"

// Names that read as the engine's, a game master's or staff's are
// reserved; names that only look alike are not.
func TestReservedName(t *testing.T) {
	for name, want := range map[string]bool{
		"GM": true, "gm": true, "G.M.": true, "GM Carl": true, "Carl GM": true, "Game Master": true, "TheGameMaster": true,
		"game-master": true, "Admin": true, "AdminBob": true, "admin_2": true, "Administrator": true, "Moderator": true,
		"Teacher": true, "Teacher Owl": true, "System": true, "Server": true, "Talespeak": true, "Staff": true, "Mods": true,
		"Whisp": false, "Holmes": false, "Badminton": false, "Gemma": false, "Agma": false, "Systemic": false,
		"Mira": false, "Wiggins": false, "Master Wu": false, "Modest Mouse": false, "": false,
	} {
		if got := ReservedName(name); got != want {
			t.Errorf("ReservedName(%q) = %v, want %v", name, got, want)
		}
	}
}
