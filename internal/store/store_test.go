package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAuditDetail(t *testing.T) {
	if got := AuditDetail("short"); got != "short" {
		t.Errorf("short = %q", got)
	}
	ascii := strings.Repeat("x", 2*MaxAuditDetail)
	if got := AuditDetail(ascii); len(got) != MaxAuditDetail {
		t.Errorf("ascii: %d bytes", len(got))
	}
	// Three bytes each: MaxAuditDetail falls inside a letter.
	wide := "x" + strings.Repeat("€", MaxAuditDetail)
	got := AuditDetail(wide)
	if !utf8.ValidString(got) || len(got) > MaxAuditDetail || len(got) < MaxAuditDetail-3 {
		t.Errorf("wide: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}
