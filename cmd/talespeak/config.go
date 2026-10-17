package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/script"
	"github.com/rumblebump/talespeak/internal/ws"
)

// limits are the resource limits an admin can set with environment
// variables; docs/admin-limits.md lists them. Limits that are part of the
// pack format (dialog options, title lengths) are not configurable, so a
// pack runs the same on every server.
type limits struct {
	script script.Limits
	pack   pack.Limits

	luaClientRate, luaClientBurst time.Duration

	maxConns, maxConnsPerClient int
	connectRate                 float64
	connectBurst                int
	messageRate                 float64
	messageBurst                int
	idleTimeout                 time.Duration
}

// loadLimits reads the limits from getenv. An empty or unset variable keeps
// the default; a value that does not parse or is out of range is an error,
// and every such error is reported at once.
func loadLimits(getenv func(string) string) (limits, error) {
	l := limits{
		script:            script.DefaultLimits,
		luaClientRate:     game.DefaultClientRate,
		luaClientBurst:    game.DefaultClientBurst,
		pack:              pack.DefaultLimits,
		maxConns:          ws.DefaultMaxConns,
		maxConnsPerClient: ws.DefaultMaxConnsPerClient,
		connectRate:       ws.DefaultConnectRate,
		connectBurst:      ws.DefaultConnectBurst,
		messageRate:       ws.DefaultRate,
		messageBurst:      ws.DefaultBurst,
		idleTimeout:       ws.DefaultIdleTimeout,
	}
	var errs []error
	duration := func(key string, dst *time.Duration, lo, hi time.Duration) {
		v := getenv(key)
		if v == "" {
			return
		}
		d, err := time.ParseDuration(v)
		if err != nil || d < lo || d > hi {
			errs = append(errs, fmt.Errorf("%s=%q: want a duration from %v to %v, like %v", key, v, lo, hi, *dst))
			return
		}
		*dst = d
	}
	size := func(key string, dst *int64, lo, hi int64) {
		v := getenv(key)
		if v == "" {
			return
		}
		n, err := parseBytes(v)
		if err != nil || n < lo || n > hi {
			errs = append(errs, fmt.Errorf("%s=%q: want a size from %s to %s, like %s", key, v, formatBytes(lo), formatBytes(hi), formatBytes(*dst)))
			return
		}
		*dst = n
	}
	count := func(key string, dst *int, lo, hi int) {
		v := getenv(key)
		if v == "" {
			return
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			errs = append(errs, fmt.Errorf("%s=%q: want a whole number from %d to %d", key, v, lo, hi))
			return
		}
		*dst = n
	}
	rate := func(key string, dst *float64, lo, hi float64) {
		v := getenv(key)
		if v == "" {
			return
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || !(f >= lo && f <= hi) { // also refuses NaN
			errs = append(errs, fmt.Errorf("%s=%q: want a number from %g to %g per second", key, v, lo, hi))
			return
		}
		*dst = f
	}

	duration("TALESPEAK_LUA_TIME", &l.script.Time, 10*time.Millisecond, 10*time.Second)
	// Lua time per client address, per second and at once (internal/game).
	duration("TALESPEAK_LUA_CLIENT_RATE", &l.luaClientRate, time.Millisecond, 10*time.Second)
	duration("TALESPEAK_LUA_CLIENT_BURST", &l.luaClientBurst, 10*time.Millisecond, time.Minute)
	size("TALESPEAK_LUA_CALL_MEMORY", &l.script.CallMemory, 1<<20, 1<<30)
	size("TALESPEAK_LUA_STATE_MEMORY", &l.script.StateMemory, 1<<20, 4<<30)
	size("TALESPEAK_PACK_IMAGE_MAX", &l.pack.AssetBytes, 1<<10, 64<<20)
	size("TALESPEAK_PACK_IMAGES_TOTAL", &l.pack.AssetsBytes, 1<<10, 4<<30)
	count("TALESPEAK_MAX_CONNS", &l.maxConns, 1, 1_000_000)
	if getenv("TALESPEAK_MAX_CONNS_PER_CLIENT") == "" {
		l.maxConnsPerClient = min(l.maxConnsPerClient, l.maxConns) // a small server lowers both at once
	}
	count("TALESPEAK_MAX_CONNS_PER_CLIENT", &l.maxConnsPerClient, 1, 1_000_000)
	rate("TALESPEAK_CONNECT_RATE", &l.connectRate, 0.01, 1_000_000)
	count("TALESPEAK_CONNECT_BURST", &l.connectBurst, 1, 1_000_000)
	rate("TALESPEAK_MESSAGE_RATE", &l.messageRate, 0.1, 1000)
	count("TALESPEAK_MESSAGE_BURST", &l.messageBurst, 1, 10_000)
	// The client pings every 30 s, so less than 45 s would drop idle players.
	duration("TALESPEAK_IDLE_TIMEOUT", &l.idleTimeout, 45*time.Second, 24*time.Hour)

	if l.pack.AssetsBytes < l.pack.AssetBytes {
		errs = append(errs, fmt.Errorf("TALESPEAK_PACK_IMAGES_TOTAL (%s) is smaller than TALESPEAK_PACK_IMAGE_MAX (%s)", formatBytes(l.pack.AssetsBytes), formatBytes(l.pack.AssetBytes)))
	}
	if l.maxConnsPerClient > l.maxConns {
		errs = append(errs, fmt.Errorf("TALESPEAK_MAX_CONNS_PER_CLIENT (%d) is larger than TALESPEAK_MAX_CONNS (%d)", l.maxConnsPerClient, l.maxConns))
	}
	return l, errors.Join(errs...)
}

var byteUnits = []struct {
	suffix string
	n      int64
}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}}

// parseBytes reads a size: a whole number of bytes, or one with a KiB,
// MiB or GiB suffix, like "32MiB".
func parseBytes(s string) (int64, error) {
	unit := int64(1)
	for _, u := range byteUnits {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			s, unit = num, u.n
			break
		}
	}
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, errors.New("not a size")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > (1<<62)/unit {
		return 0, errors.New("not a size")
	}
	return n * unit, nil
}

// formatBytes writes n with the largest unit that divides it.
func formatBytes(n int64) string {
	for _, u := range byteUnits {
		if n >= u.n && n%u.n == 0 {
			return strconv.FormatInt(n/u.n, 10) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}
