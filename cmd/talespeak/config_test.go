package main

import (
	"math"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/account"
	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/hub"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// env is a getenv over a map.
func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func defaults() limits {
	return limits{
		script:            script.DefaultLimits,
		luaClientRate:     game.DefaultClientRate,
		luaClientBurst:    game.DefaultClientBurst,
		luaClientMemory:   game.DefaultClientMemory,
		pack:              pack.DefaultLimits,
		maxConns:          ws.DefaultMaxConns,
		maxConnsPerClient: ws.DefaultMaxConnsPerClient,
		connectRate:       ws.DefaultConnectRate,
		connectBurst:      ws.DefaultConnectBurst,
		messageRate:       ws.DefaultRate,
		messageBurst:      ws.DefaultBurst,
		idleTimeout:       ws.DefaultIdleTimeout,
		signInBurst:       account.DefaultAddressBurst,
		worldsPerAccount:  account.DefaultWorldsPerAccount,
		maxWorlds:         account.DefaultMaxWorlds,
		loadedWorlds:      hub.DefaultMaxLoaded,
		worldIdle:         hub.DefaultIdle,
	}
}

func TestLoadLimitsDefaults(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"unset": nil,
		"empty": {"TALESPEAK_LUA_TIME": "", "TALESPEAK_MAX_CONNS": "", "TALESPEAK_PACK_IMAGE_MAX": "", "TALESPEAK_CONNECT_RATE": ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := loadLimits(env(vars))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, defaults()) {
				t.Errorf("limits = %+v, want %+v", got, defaults())
			}
		})
	}
}

func TestLoadLimitsSets(t *testing.T) {
	tests := []struct {
		key, value string
		check      func(l limits) bool
	}{
		{"TALESPEAK_LUA_TIME", "250ms", func(l limits) bool { return l.script.Time == 250*time.Millisecond }},
		{"TALESPEAK_LUA_TIME", "10ms", func(l limits) bool { return l.script.Time == 10*time.Millisecond }},
		{"TALESPEAK_LUA_TIME", "10s", func(l limits) bool { return l.script.Time == 10*time.Second }},
		{"TALESPEAK_LUA_CLIENT_RATE", "1ms", func(l limits) bool { return l.luaClientRate == time.Millisecond }},
		{"TALESPEAK_LUA_CLIENT_RATE", "10s", func(l limits) bool { return l.luaClientRate == 10*time.Second }},
		{"TALESPEAK_LUA_CLIENT_BURST", "10ms", func(l limits) bool { return l.luaClientBurst == 10*time.Millisecond }},
		{"TALESPEAK_LUA_CLIENT_BURST", "1m", func(l limits) bool { return l.luaClientBurst == time.Minute }},
		{"TALESPEAK_LUA_CALL_MEMORY", "1MiB", func(l limits) bool { return l.script.CallMemory == 1<<20 }},
		{"TALESPEAK_LUA_CALL_MEMORY", "1GiB", func(l limits) bool { return l.script.CallMemory == 1<<30 }},
		{"TALESPEAK_LUA_CALL_MEMORY", "2097152", func(l limits) bool { return l.script.CallMemory == 2<<20 }},
		{"TALESPEAK_LUA_STATE_MEMORY", "4GiB", func(l limits) bool { return l.script.StateMemory == 4<<30 }},
		{"TALESPEAK_LUA_STATE_MEMORY", "1024KiB", func(l limits) bool {
			return l.script.StateMemory == 1<<20 && l.script.PlayerMemory == 1<<20 && l.luaClientMemory == 1<<20
		}},
		{"TALESPEAK_LUA_PLAYER_MEMORY", "64KiB", func(l limits) bool { return l.script.PlayerMemory == 64<<10 }},
		{"TALESPEAK_LUA_PLAYER_MEMORY", "16MiB", func(l limits) bool { return l.script.PlayerMemory == 16<<20 }},
		{"TALESPEAK_LUA_CLIENT_MEMORY", "256MiB", func(l limits) bool { return l.luaClientMemory == 256<<20 }},
		{"TALESPEAK_LUA_CLIENT_MEMORY", "64KiB", func(l limits) bool { return l.luaClientMemory == 64<<10 }},
		{"TALESPEAK_PACK_IMAGE_MAX", "1KiB", func(l limits) bool { return l.pack.AssetBytes == 1<<10 }},
		{"TALESPEAK_PACK_IMAGE_MAX", "64MiB", func(l limits) bool { return l.pack.AssetBytes == 64<<20 }},
		{"TALESPEAK_PACK_IMAGES_TOTAL", "4GiB", func(l limits) bool { return l.pack.AssetsBytes == 4<<30 }},
		{"TALESPEAK_MAX_CONNS", "1000000", func(l limits) bool { return l.maxConns == 1_000_000 }},
		{"TALESPEAK_MAX_CONNS", "100", func(l limits) bool { return l.maxConns == 100 }},
		{"TALESPEAK_MAX_CONNS_PER_CLIENT", "1", func(l limits) bool { return l.maxConnsPerClient == 1 }},
		{"TALESPEAK_CONNECT_RATE", "0.01", func(l limits) bool { return l.connectRate == 0.01 }},
		{"TALESPEAK_CONNECT_RATE", "1e6", func(l limits) bool { return l.connectRate == 1e6 }},
		{"TALESPEAK_CONNECT_BURST", "1000000", func(l limits) bool { return l.connectBurst == 1_000_000 }},
		{"TALESPEAK_MESSAGE_RATE", "0.1", func(l limits) bool { return l.messageRate == 0.1 }},
		{"TALESPEAK_MESSAGE_RATE", "1000", func(l limits) bool { return l.messageRate == 1000 }},
		{"TALESPEAK_MESSAGE_BURST", "1", func(l limits) bool { return l.messageBurst == 1 }},
		{"TALESPEAK_MESSAGE_BURST", "10000", func(l limits) bool { return l.messageBurst == 10_000 }},
		{"TALESPEAK_IDLE_TIMEOUT", "45s", func(l limits) bool { return l.idleTimeout == 45*time.Second }},
		{"TALESPEAK_IDLE_TIMEOUT", "24h", func(l limits) bool { return l.idleTimeout == 24*time.Hour }},
		{"TALESPEAK_IDLE_TIMEOUT", "1h30m", func(l limits) bool { return l.idleTimeout == 90*time.Minute }},
		{"TALESPEAK_WORLDS_PER_ACCOUNT", "1", func(l limits) bool { return l.worldsPerAccount == 1 }},
		{"TALESPEAK_WORLDS_PER_ACCOUNT", "1000", func(l limits) bool { return l.worldsPerAccount == 1000 }},
		{"TALESPEAK_MAX_WORLDS", "1", func(l limits) bool { return l.maxWorlds == 1 }},
		{"TALESPEAK_LOADED_WORLDS", "10000", func(l limits) bool { return l.loadedWorlds == 10000 }},
		{"TALESPEAK_WORLD_IDLE", "10s", func(l limits) bool { return l.worldIdle == 10*time.Second }},
		{"TALESPEAK_WORLD_IDLE", "24h", func(l limits) bool { return l.worldIdle == 24*time.Hour }},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			got, err := loadLimits(env(map[string]string{tt.key: tt.value}))
			if err != nil {
				t.Fatal(err)
			}
			if !tt.check(got) {
				t.Errorf("limits = %+v", got)
			}
		})
	}
}

func TestLoadLimitsRefuses(t *testing.T) {
	tests := []struct{ key, value string }{
		{"TALESPEAK_LUA_TIME", "100"}, // no unit
		{"TALESPEAK_LUA_CLIENT_RATE", "0s"},
		{"TALESPEAK_LUA_CLIENT_BURST", "2m"},
		{"TALESPEAK_LUA_TIME", "9ms"},
		{"TALESPEAK_LUA_TIME", "11s"},
		{"TALESPEAK_LUA_TIME", "-1s"},
		{"TALESPEAK_LUA_TIME", "fast"},
		{"TALESPEAK_LUA_TIME", " 100ms"},
		{"TALESPEAK_LUA_CALL_MEMORY", "1023KiB"},
		{"TALESPEAK_LUA_CALL_MEMORY", "2GiB"},
		{"TALESPEAK_LUA_CALL_MEMORY", "32MB"},
		{"TALESPEAK_LUA_CALL_MEMORY", "32mib"},
		{"TALESPEAK_LUA_CALL_MEMORY", "1.5MiB"},
		{"TALESPEAK_LUA_CALL_MEMORY", "-1MiB"},
		{"TALESPEAK_LUA_CALL_MEMORY", "MiB"},
		{"TALESPEAK_LUA_STATE_MEMORY", "5GiB"},
		{"TALESPEAK_LUA_PLAYER_MEMORY", "63KiB"},
		{"TALESPEAK_LUA_PLAYER_MEMORY", "2GiB"},
		{"TALESPEAK_LUA_STATE_MEMORY", "99999999999999999999"},
		{"TALESPEAK_PACK_IMAGE_MAX", "1023"},
		{"TALESPEAK_PACK_IMAGE_MAX", "65MiB"},
		{"TALESPEAK_PACK_IMAGES_TOTAL", "1023B"},
		{"TALESPEAK_PACK_IMAGES_TOTAL", "4097MiB"},
		{"TALESPEAK_MAX_CONNS", "0"},
		{"TALESPEAK_MAX_CONNS", "1000001"},
		{"TALESPEAK_MAX_CONNS", "1e3"},
		{"TALESPEAK_MAX_CONNS", "10.0"},
		{"TALESPEAK_MAX_CONNS", "-5"},
		{"TALESPEAK_MAX_CONNS", "many"},
		{"TALESPEAK_MAX_CONNS_PER_CLIENT", "0"},
		{"TALESPEAK_CONNECT_RATE", "0"},
		{"TALESPEAK_CONNECT_RATE", "0.009"},
		{"TALESPEAK_CONNECT_RATE", "1000001"},
		{"TALESPEAK_CONNECT_RATE", "NaN"},
		{"TALESPEAK_CONNECT_RATE", "Inf"},
		{"TALESPEAK_CONNECT_RATE", "-Inf"},
		{"TALESPEAK_CONNECT_RATE", "1/s"},
		{"TALESPEAK_CONNECT_BURST", "0"},
		{"TALESPEAK_MESSAGE_RATE", "0.09"},
		{"TALESPEAK_MESSAGE_RATE", "1001"},
		{"TALESPEAK_MESSAGE_RATE", "nan"},
		{"TALESPEAK_MESSAGE_BURST", "10001"},
		{"TALESPEAK_IDLE_TIMEOUT", "44s"},
		{"TALESPEAK_IDLE_TIMEOUT", "25h"},
		{"TALESPEAK_IDLE_TIMEOUT", "30"},
		{"TALESPEAK_WORLDS_PER_ACCOUNT", "0"},
		{"TALESPEAK_WORLDS_PER_ACCOUNT", "1001"},
		{"TALESPEAK_MAX_WORLDS", "0"},
		{"TALESPEAK_LOADED_WORLDS", "0"},
		{"TALESPEAK_LOADED_WORLDS", "10001"},
		{"TALESPEAK_WORLD_IDLE", "9s"},
		{"TALESPEAK_WORLD_IDLE", "25h"},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			_, err := loadLimits(env(map[string]string{tt.key: tt.value}))
			if err == nil {
				t.Fatal("want an error")
			}
			// The message names the variable and quotes the value.
			if !strings.Contains(err.Error(), tt.key+"="+strconv.Quote(tt.value)) {
				t.Errorf("error %q does not name %s=%q", err, tt.key, tt.value)
			}
		})
	}
}

// Every bad variable is reported at once, one per line, so an admin fixes
// them in one go.
func TestLoadLimitsReportsEveryProblem(t *testing.T) {
	_, err := loadLimits(env(map[string]string{
		"TALESPEAK_LUA_TIME":        "1h",
		"TALESPEAK_LUA_CALL_MEMORY": "lots",
		"TALESPEAK_MAX_CONNS":       "0",
		"TALESPEAK_CONNECT_RATE":    "NaN",
		"TALESPEAK_IDLE_TIMEOUT":    "1s",
	}))
	if err == nil {
		t.Fatal("want an error")
	}
	lines := strings.Split(err.Error(), "\n")
	if len(lines) != 5 {
		t.Errorf("%d problems, want 5:\n%s", len(lines), err)
	}
	for _, key := range []string{"TALESPEAK_LUA_TIME", "TALESPEAK_LUA_CALL_MEMORY", "TALESPEAK_MAX_CONNS", "TALESPEAK_CONNECT_RATE", "TALESPEAK_IDLE_TIMEOUT"} {
		if !strings.Contains(err.Error(), key+"=") {
			t.Errorf("error does not name %s:\n%s", key, err)
		}
	}
	// The message says what is allowed, with the default as an example.
	if !strings.Contains(err.Error(), "from 1MiB to 1GiB, like 32MiB") || !strings.Contains(err.Error(), "from 10ms to 10s, like 100ms") {
		t.Errorf("error does not give the range and default:\n%s", err)
	}
}

func TestLoadLimitsCrossChecks(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
		want string // "" for no error
	}{
		{"total below image max", map[string]string{"TALESPEAK_PACK_IMAGE_MAX": "2MiB", "TALESPEAK_PACK_IMAGES_TOTAL": "1MiB"}, "TALESPEAK_PACK_IMAGES_TOTAL (1MiB) is smaller than TALESPEAK_PACK_IMAGE_MAX (2MiB)"},
		{"total equal to image max", map[string]string{"TALESPEAK_PACK_IMAGE_MAX": "2MiB", "TALESPEAK_PACK_IMAGES_TOTAL": "2MiB"}, ""},
		{"total below the default image max", map[string]string{"TALESPEAK_PACK_IMAGES_TOTAL": "1MiB"}, "smaller than"},
		{"per client above max", map[string]string{"TALESPEAK_MAX_CONNS": "10", "TALESPEAK_MAX_CONNS_PER_CLIENT": "11"}, "TALESPEAK_MAX_CONNS_PER_CLIENT (11) is larger than TALESPEAK_MAX_CONNS (10)"},
		{"per client equal to max", map[string]string{"TALESPEAK_MAX_CONNS": "10", "TALESPEAK_MAX_CONNS_PER_CLIENT": "10"}, ""},
		// Unset, the per-client limit follows a lower server maximum.
		{"max below the default per client", map[string]string{"TALESPEAK_MAX_CONNS": "50"}, ""},
		{"per client set above max", map[string]string{"TALESPEAK_MAX_CONNS": "50", "TALESPEAK_MAX_CONNS_PER_CLIENT": "100"}, "larger than TALESPEAK_MAX_CONNS (50)"},
		{"player memory above world", map[string]string{"TALESPEAK_LUA_STATE_MEMORY": "8MiB", "TALESPEAK_LUA_PLAYER_MEMORY": "9MiB"}, "TALESPEAK_LUA_STATE_MEMORY (8MiB) is smaller than TALESPEAK_LUA_PLAYER_MEMORY (9MiB)"},
		{"player memory equal to world", map[string]string{"TALESPEAK_LUA_STATE_MEMORY": "8MiB", "TALESPEAK_LUA_PLAYER_MEMORY": "8MiB"}, ""},
		{"client memory above world", map[string]string{"TALESPEAK_LUA_STATE_MEMORY": "8MiB", "TALESPEAK_LUA_CLIENT_MEMORY": "9MiB"}, "TALESPEAK_LUA_CLIENT_MEMORY (9MiB) is larger than TALESPEAK_LUA_STATE_MEMORY (8MiB)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadLimits(env(tt.vars))
			if tt.want == "" {
				if err != nil {
					t.Errorf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// docs/admin-limits.md lists exactly the variables loadLimits reads, with
// the defaults it uses.
func TestAdminLimitsDocMatches(t *testing.T) {
	doc, err := os.ReadFile("../../docs/admin-limits.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `(TALESPEAK_[A-Z_]+)` \\| `([^`]+)` \\|")
	documented := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(doc), -1) {
		documented[m[1]] = m[2]
	}
	var read []string
	if _, err := loadLimits(func(key string) string { read = append(read, key); return "" }); err != nil {
		t.Fatal(err)
	}
	sort.Strings(read)
	read = slices.Compact(read) // a variable may be read twice
	var docKeys []string
	for k := range documented {
		docKeys = append(docKeys, k)
	}
	sort.Strings(docKeys)
	if !reflect.DeepEqual(read, docKeys) {
		t.Fatalf("loadLimits reads %v\ndocs/admin-limits.md lists %v", read, docKeys)
	}
	// Each documented default, set explicitly, gives the built-in limits.
	for key, def := range documented {
		got, err := loadLimits(env(map[string]string{key: def}))
		if err != nil {
			t.Errorf("%s=%s (documented default): %v", key, def, err)
			continue
		}
		if !reflect.DeepEqual(got, defaults()) {
			t.Errorf("%s=%s (documented default) gives %+v, but the built-in default is %+v", key, def, got, defaults())
		}
	}
}

func TestParseBytes(t *testing.T) {
	tests := []struct {
		in   string
		want int64 // -1 for an error
	}{
		{"0", 0},
		{"1", 1},
		{"1B", 1},
		{"0B", 0},
		{"1KiB", 1 << 10},
		{"32MiB", 32 << 20},
		{"4GiB", 4 << 30},
		{"4294967296GiB", 1 << 62},
		{"4294967297GiB", -1},
		{"4611686018427387904", 1 << 62},
		{"4611686018427387905", -1},
		{"9223372036854775807", -1},
		{"", -1},
		{"B", -1},
		{"KiB", -1},
		{"-1", -1},
		{"-1KiB", -1},
		{"1.5MiB", -1},
		{"1e6", -1},
		{"0x10", -1},
		{"1KB", -1},
		{"1kib", -1},
		{"1 KiB B", -1},
		{"1MiBMiB", -1},
		{"1TiB", -1},
		{"ten", -1},
		{"5 MiB", -1},
		{" 7 ", -1},
		{"+5", -1},
	}
	for _, tt := range tests {
		got, err := parseBytes(tt.in)
		if tt.want < 0 {
			if err == nil {
				t.Errorf("parseBytes(%q) = %d, want an error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("parseBytes(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := map[int64]string{
		0:           "0B",
		1:           "1B",
		1023:        "1023B",
		1024:        "1KiB",
		1536:        "1536B",
		2 << 20:     "2MiB",
		3 << 19:     "1536KiB",
		1 << 30:     "1GiB",
		4 << 30:     "4GiB",
		1<<30 + 1:   "1073741825B",
		1 << 62:     "4294967296GiB",
		math.MaxInt: "9223372036854775807B",
	}
	for n, want := range tests {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

// FuzzParseBytes: parseBytes never panics, never returns a negative size,
// and what formatBytes writes reads back the same.
func FuzzParseBytes(f *testing.F) {
	for _, s := range []string{"0", "32MiB", "1KiB", "4GiB", " 5 B", "-1", "4294967297GiB", "1.5MiB", "+3MiB"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := parseBytes(s)
		if err != nil {
			return
		}
		if n < 0 || n > 1<<62 {
			t.Fatalf("parseBytes(%q) = %d", s, n)
		}
		back, err := parseBytes(formatBytes(n))
		if err != nil || back != n {
			t.Fatalf("parseBytes(formatBytes(%d) = %q) = %d, %v", n, formatBytes(n), back, err)
		}
	})
}
