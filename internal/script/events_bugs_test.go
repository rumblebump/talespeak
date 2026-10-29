package script

// Tests for bugs the test-engineer found in T-0046, fixed since.

import (
	"strings"
	"testing"
)

// ReservedName splits words where a capital follows a small letter only,
// so a run of capitals before a word ("GMCarl") is one word, and a capital
// inside a reserved word ("AdMin Bob") cuts it in two. Both read as staff
// and both are account names self-registration takes. A name reserved in
// small letters should be reserved in any letter case.
func TestReservedNameMixedCase(t *testing.T) {
	for _, name := range []string{"GMCarl", "GMBot", "ADMINBOB", "SERVERbot", "TEACHERAnna", "AdMin Bob", "gM Carl", "TeAcher Anna", "MoD_Anna"} {
		if !ReservedName(name) {
			t.Errorf("ReservedName(%q) = false", name)
		}
		if ReservedName(strings.ToLower(name)) && !ReservedName(name) {
			t.Errorf("%q is reserved in small letters only", name)
		}
	}
}
