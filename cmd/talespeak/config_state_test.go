package main

import (
	"strings"
	"testing"

	"github.com/rumblebump/talespeak/internal/script"
)

// The TALESPEAK_STATE_* limits (T-0018): each one reaches
// script.Limits.State, within its bounds.
func TestStateLimits(t *testing.T) {
	ok := []struct {
		key, value string
		check      func(script.StateLimits) bool
	}{
		{"TALESPEAK_STATE_KEY_BYTES", "32", func(l script.StateLimits) bool { return l.KeyBytes == 32 }},
		{"TALESPEAK_STATE_KEY_BYTES", "255", func(l script.StateLimits) bool { return l.KeyBytes == 255 }},
		{"TALESPEAK_STATE_VALUE_MAX", "1KiB", func(l script.StateLimits) bool { return l.ValueBytes == 1<<10 }},
		{"TALESPEAK_STATE_VALUE_MAX", "256KiB", func(l script.StateLimits) bool { return l.ValueBytes == 256<<10 }},
		{"TALESPEAK_STATE_DEPTH", "32", func(l script.StateLimits) bool { return l.Depth == 32 }},
		{"TALESPEAK_STATE_PLAYER_MAX", "1MiB", func(l script.StateLimits) bool { return l.PlayerBytes == 1<<20 }},
		{"TALESPEAK_STATE_PLAYER_KEYS", "10", func(l script.StateLimits) bool { return l.PlayerKeys == 10 }},
		{"TALESPEAK_STATE_WORLD_MAX", "1GiB", func(l script.StateLimits) bool { return l.WorldBytes == 1<<30 }},
		{"TALESPEAK_STATE_WORLD_KEYS", "1000000", func(l script.StateLimits) bool { return l.WorldKeys == 1_000_000 }},
		{"TALESPEAK_STATE_GAME_MAX", "64MiB", func(l script.StateLimits) bool { return l.GameBytes == 64<<20 }},
		{"TALESPEAK_STATE_GAME_KEYS", "30", func(l script.StateLimits) bool { return l.GameKeys == 30 }},
		{"TALESPEAK_STATE_WRITES", "1", func(l script.StateLimits) bool { return l.Writes == 1 }},
	}
	for _, tt := range ok {
		got, err := loadLimits(env(map[string]string{tt.key: tt.value}))
		if err != nil || !tt.check(got.script.State) {
			t.Errorf("%s=%s: %+v, %v", tt.key, tt.value, got.script.State, err)
		}
	}
	bad := map[string]string{
		"TALESPEAK_STATE_KEY_BYTES":   "31",
		"TALESPEAK_STATE_VALUE_MAX":   "2MiB",
		"TALESPEAK_STATE_DEPTH":       "1",
		"TALESPEAK_STATE_PLAYER_MAX":  "1KiB",
		"TALESPEAK_STATE_PLAYER_KEYS": "x",
		"TALESPEAK_STATE_WORLD_MAX":   "2GiB",
		"TALESPEAK_STATE_WORLD_KEYS":  "9",
		"TALESPEAK_STATE_GAME_MAX":    "-1",
		"TALESPEAK_STATE_GAME_KEYS":   "29",
		"TALESPEAK_STATE_WRITES":      "0",
	}
	for key, value := range bad {
		if _, err := loadLimits(env(map[string]string{key: value})); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%s: err = %v", key, value, err)
		}
	}
	// A value larger than a total could never be kept.
	_, err := loadLimits(env(map[string]string{"TALESPEAK_STATE_VALUE_MAX": "512KiB", "TALESPEAK_STATE_PLAYER_MAX": "256KiB"}))
	if err == nil || !strings.Contains(err.Error(), "TALESPEAK_STATE_VALUE_MAX (512KiB) is larger than") {
		t.Errorf("value over a total: %v", err)
	}
}
