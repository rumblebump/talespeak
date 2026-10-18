package account

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// clock is a fake clock for the limiters.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)} }

func TestLimiterBurstAndRefill(t *testing.T) {
	c := newClock()
	l := newLimiter(3, 10*time.Second, c.now)
	steps := []struct {
		advance time.Duration
		key     string
		want    bool
	}{
		{0, "a", true}, {0, "a", true}, {0, "a", true},
		{0, "a", false}, // burst used up
		{0, "b", true},  // keys are counted apart
		{9 * time.Second, "a", false},
		{time.Second, "a", true}, // one more after per
		{0, "a", false},
		{time.Hour, "a", true}, // refilled to burst, no more
		{0, "a", true}, {0, "a", true},
		{0, "a", false},
	}
	for i, s := range steps {
		c.add(s.advance)
		if got := l.allow(s.key); got != s.want {
			t.Errorf("step %d (%s after %v): allow = %v, want %v", i, s.key, s.advance, got, s.want)
		}
	}
}

// A clock that goes backwards never adds tokens; the time in between does
// not count. (time.Now's monotonic reading never goes back.)
func TestLimiterClockBackwards(t *testing.T) {
	c := newClock()
	l := newLimiter(1, time.Second, c.now)
	if !l.allow("a") {
		t.Fatal("first allow refused")
	}
	c.add(-time.Hour)
	if l.allow("a") {
		t.Error("a clock going back added a token")
	}
	// Back at one second after the first token: one second's refill.
	c.add(time.Hour + time.Second)
	if !l.allow("a") || l.allow("a") {
		t.Error("want one token one second after the first, then none")
	}
}

// A full map drops buckets that have refilled first, so a key that is
// still limited stays limited; it never grows past maxBuckets.
func TestLimiterPruneKeepsLimitedKeys(t *testing.T) {
	c := newClock()
	l := newLimiter(2, time.Second, c.now)
	for i := range maxBuckets - 1 {
		l.allow(fmt.Sprint("k", i))
	}
	c.add(time.Second) // every k bucket is full again
	if !l.allow("hot") || !l.allow("hot") || l.allow("hot") {
		t.Fatal("hot: want two tokens, then none")
	}
	if len(l.m) != maxBuckets {
		t.Fatalf("buckets = %d, want %d", len(l.m), maxBuckets)
	}
	if !l.allow("new") {
		t.Fatal("a new key was refused")
	}
	if len(l.m) != 2 {
		t.Errorf("buckets after prune = %d, want hot and new", len(l.m))
	}
	if l.allow("hot") {
		t.Error("prune dropped a key that was still limited")
	}
}

// When no bucket has refilled, prune drops any to make room: the map stays
// at maxBuckets.
func TestLimiterPruneWhenNoneRefilled(t *testing.T) {
	c := newClock()
	l := newLimiter(1, time.Hour, c.now)
	for i := range maxBuckets + 50 {
		if !l.allow(fmt.Sprint("k", i)) {
			t.Fatalf("new key %d refused", i)
		}
		if len(l.m) > maxBuckets {
			t.Fatalf("buckets = %d after %d keys, want at most %d", len(l.m), i+1, maxBuckets)
		}
	}
}

func TestLimiterConcurrent(t *testing.T) {
	c := newClock()
	l := newLimiter(100, time.Hour, c.now)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				ok := l.allow("shared")
				l.allow(fmt.Sprint(g, "-", i))
				if ok {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if allowed != 100 {
		t.Errorf("allowed %d of 400 on one key with burst 100", allowed)
	}
}

// With a full map and no bucket refilled, a new key gets one try, not a
// fresh burst, and the drained keys stay: the fullest bucket makes room.
func TestLimiterFullMapNewKeyGetsOneTry(t *testing.T) {
	c := newClock()
	l := newLimiter(5, time.Hour, c.now)
	for i := range maxBuckets - 1 {
		for range 5 {
			l.allow(fmt.Sprint("k", i)) // drained
		}
	}
	l.allow("fullest") // four tokens left: the fullest bucket
	if len(l.m) != maxBuckets {
		t.Fatalf("buckets = %d", len(l.m))
	}
	if !l.allow("new") {
		t.Fatal("a new key got no try at all")
	}
	if l.allow("new") {
		t.Error("a new key in a full map got a fresh burst")
	}
	if _, ok := l.m["fullest"]; ok {
		t.Error("prune did not drop the fullest bucket")
	}
	if l.allow("k0") {
		t.Error("a drained key was pushed out and got a fresh burst")
	}
	if len(l.m) > maxBuckets {
		t.Errorf("buckets = %d, over %d", len(l.m), maxBuckets)
	}
}

// ok looks without taking; take takes also below zero, so a failed try
// counted after the fact still costs.
func TestLimiterOkTake(t *testing.T) {
	c := newClock()
	l := newLimiter(2, time.Second, c.now)
	for range 3 {
		if !l.ok("a") {
			t.Fatal("ok took a token")
		}
	}
	l.take("a")
	l.take("a")
	if l.ok("a") {
		t.Error("ok after two takes of a burst of two")
	}
	l.take("a") // below zero
	c.add(time.Second)
	if l.ok("a") {
		t.Error("one second after going below zero: want still none")
	}
	c.add(time.Second)
	if !l.ok("a") || !l.allow("a") || l.allow("a") {
		t.Error("two seconds after: want exactly one token")
	}
}
