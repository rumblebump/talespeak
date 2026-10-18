package account

import (
	"math"
	"sync"
	"time"
)

// maxBuckets bounds a limiter's map; past it, buckets that have refilled
// are dropped.
const maxBuckets = 10000

// limiter is a token bucket per key (a client address, an account name):
// burst at once, then one more every per.
type limiter struct {
	per   time.Duration
	burst float64
	now   func() time.Time

	mu sync.Mutex
	m  map[string]*tokens
}

type tokens struct {
	n    float64
	last time.Time
}

func newLimiter(burst int, per time.Duration, now func() time.Time) *limiter {
	return &limiter{per: per, burst: float64(burst), now: now, m: map[string]*tokens{}}
}

// allow takes a token for key, or reports false when there is none.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.bucket(key)
	if t.n < 1 {
		return false
	}
	t.n--
	return true
}

// ok reports whether key has a token, without taking it.
func (l *limiter) ok(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bucket(key).n >= 1
}

// take takes a token for key, also when that leaves it below zero.
func (l *limiter) take(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bucket(key).n--
}

// bucket returns key's bucket, refilled up to now. The caller holds mu.
func (l *limiter) bucket(key string) *tokens {
	now := l.now()
	t := l.m[key]
	if t == nil {
		t = &tokens{n: l.burst, last: now}
		if len(l.m) >= maxBuckets && !l.prune(now) {
			t.n = 1 // a full map: a new key gets one try, never a fresh burst
		}
		l.m[key] = t
	}
	t.n = min(l.burst, t.n+float64(now.Sub(t.last))/float64(l.per))
	t.last = now
	return t
}

// prune makes room for one more bucket by dropping those that have
// refilled. When none has, it drops the one closest to full and reports
// false: a client cannot push an empty bucket (its own, or the account it
// guesses at) out of the map with many new keys. The caller holds mu.
func (l *limiter) prune(now time.Time) bool {
	fullest, most := "", math.Inf(-1)
	for k, t := range l.m {
		n := t.n + float64(now.Sub(t.last))/float64(l.per)
		if n >= l.burst {
			delete(l.m, k)
		} else if n > most {
			fullest, most = k, n
		}
	}
	if len(l.m) < maxBuckets {
		return true
	}
	delete(l.m, fullest)
	return false
}
