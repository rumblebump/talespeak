package script

// The limits of state.player, state.world and state.game, and the bounds
// a server's settings for them must keep (cmd/talespeak/config.go).

// Default limits of state.player, state.world and state.game: the
// defaults of StateLimits, small on purpose so no pack can fill the
// database. Servers change them with TALESPEAK_STATE_* settings
// (docs/admin-limits.md).
const (
	MaxStateKeyBytes    = 64       // one key
	MaxStateValueBytes  = 16 << 10 // one value, encoded
	MaxStateDepth       = 8        // tables in tables
	MaxPlayerStateBytes = 256 << 10
	MaxPlayerStateKeys  = 500
	MaxWorldStateBytes  = 4 << 20
	MaxWorldStateKeys   = 5000
	MaxGameStateBytes   = 1 << 20
	MaxGameStateKeys    = 1000
	MaxStateWrites      = 50 // set and update calls of state.player, state.world and state.game in one hook

	// maxStateScan is how many table slots one set may look at, empty
	// ones included: gopher-lua skips empty array slots and removed keys
	// one by one, in Go, where the call's time limit cannot stop it.
	maxStateScan = 64 << 10
)

// StateLimits bound what scripts keep in state.player, state.world and
// state.game, so no pack can fill the database. A zero field is its
// default (DefaultStateLimits).
type StateLimits struct {
	KeyBytes    int   // one key
	ValueBytes  int   // one value, encoded
	Depth       int   // tables in tables
	PlayerBytes int64 // per character, keys and encoded values
	PlayerKeys  int
	WorldBytes  int64 // per world
	WorldKeys   int
	GameBytes   int64 // state.game, per world
	GameKeys    int
	Writes      int // set and update calls in one hook, its questions included
}

// DefaultStateLimits are the limits of a server that sets none.
var DefaultStateLimits = StateLimits{KeyBytes: MaxStateKeyBytes, ValueBytes: MaxStateValueBytes, Depth: MaxStateDepth,
	PlayerBytes: MaxPlayerStateBytes, PlayerKeys: MaxPlayerStateKeys, WorldBytes: MaxWorldStateBytes, WorldKeys: MaxWorldStateKeys,
	GameBytes: MaxGameStateBytes, GameKeys: MaxGameStateKeys, Writes: MaxStateWrites}

// MinStateLimits and MaxStateLimits are the lowest and highest limits a
// server may set. The shortest keys still hold a pack's setting names
// (pack.MaxSettingName); the highest keep the decoder's recursion and one
// value far below a world's memory budget.
var (
	MinStateLimits = StateLimits{KeyBytes: 32, ValueBytes: 1 << 10, Depth: 2, PlayerBytes: 4 << 10, PlayerKeys: 10,
		WorldBytes: 4 << 10, WorldKeys: 10, GameBytes: 4 << 10, GameKeys: 30, Writes: 1}
	MaxStateLimits = StateLimits{KeyBytes: 255, ValueBytes: 1 << 20, Depth: 32, PlayerBytes: 64 << 20, PlayerKeys: 100_000,
		WorldBytes: 1 << 30, WorldKeys: 1_000_000, GameBytes: 64 << 20, GameKeys: 100_000, Writes: 10_000}
)

// Refusals is what a Host may implement to hear of state writes a limit
// refused, for the server log: the first one of each hook only, so a
// script writing in a loop cannot flood the log.
type Refusals interface {
	StateRefused(msg string)
}

// refused tells the running hook's Host, once per hook, that a limit
// refused a write with msg.
func (r *Runtime) refused(msg string) {
	if r.cur == nil || r.cur.thread == nil || r.cur.thread.refusedTold {
		return
	}
	r.cur.thread.refusedTold = true
	if h, ok := r.cur.host.(Refusals); ok {
		h.StateRefused(msg)
	}
}

// OrDefault returns l with its zero fields set to the defaults.
func (l StateLimits) OrDefault() StateLimits {
	d := DefaultStateLimits
	pick := func(v *int, def int) {
		if *v <= 0 {
			*v = def
		}
	}
	pick64 := func(v *int64, def int64) {
		if *v <= 0 {
			*v = def
		}
	}
	pick(&l.KeyBytes, d.KeyBytes)
	pick(&l.ValueBytes, d.ValueBytes)
	pick(&l.Depth, d.Depth)
	pick64(&l.PlayerBytes, d.PlayerBytes)
	pick(&l.PlayerKeys, d.PlayerKeys)
	pick64(&l.WorldBytes, d.WorldBytes)
	pick(&l.WorldKeys, d.WorldKeys)
	pick64(&l.GameBytes, d.GameBytes)
	pick(&l.GameKeys, d.GameKeys)
	pick(&l.Writes, d.Writes)
	return l
}
