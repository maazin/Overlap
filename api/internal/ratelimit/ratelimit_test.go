package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// clock is a hand-wound replacement for time.Now so these tests assert on
// refill behaviour without sleeping for it.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestLimiter(burst int, perMinute float64, maxKeys int) (*Limiter, *clock) {
	c := &clock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	l := New(burst, perMinute, maxKeys)
	l.now = c.now
	return l, c
}

func TestAllowsBurstThenRefuses(t *testing.T) {
	l, _ := newTestLimiter(3, 60, 100)

	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}

	ok, wait := l.Allow("a")
	if ok {
		t.Fatal("a fourth request got through a burst of three")
	}
	if wait <= 0 {
		t.Error("a refusal must say how long to wait, so Retry-After can mean something")
	}
}

func TestRefillsOverTime(t *testing.T) {
	l, c := newTestLimiter(2, 60, 100) // one token per second

	l.Allow("a")
	l.Allow("a")
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("bucket should be empty")
	}

	c.advance(1500 * time.Millisecond)
	if ok, _ := l.Allow("a"); !ok {
		t.Error("a token should have refilled after a second and a half")
	}
}

func TestRefillStopsAtBurst(t *testing.T) {
	l, c := newTestLimiter(2, 60, 100)

	l.Allow("a")
	// An hour of quiet must not bank an hour of requests. Without the cap, a
	// key that goes idle would return holding a budget large enough to do
	// exactly the damage the limiter exists to bound.
	c.advance(time.Hour)

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first request after idling was refused")
	}
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("second request after idling was refused")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Error("idling banked more than the burst")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := newTestLimiter(1, 60, 100)

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first key refused")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("one key exhausting its bucket must not affect another")
	}
}

func TestSweepDropsIdleKeysOnly(t *testing.T) {
	l, c := newTestLimiter(2, 60, 100)

	l.Allow("idle")
	l.Allow("busy")
	l.Allow("busy") // drained

	// One second at one token per second refills "idle" from 1 to its full 2,
	// and "busy" only from 0 to 1. Any longer and both are full, which is the
	// correct outcome but tests nothing about the distinction.
	c.advance(time.Second)

	l.Sweep()

	// "idle" is back to full and carries nothing a fresh bucket would not.
	// "busy" is still down a token, so dropping it would hand back budget it
	// has not earned yet.
	if l.Len() != 1 {
		t.Errorf("Len after sweep = %d, want 1 (the drained key)", l.Len())
	}
}

// TestAtCapacityFailsOpen pins the deliberate choice. A per-key limiter cannot
// stop an attacker who rotates keys, so refusing new keys once the map is full
// would add a way to lock out real users without closing the hole.
func TestAtCapacityFailsOpen(t *testing.T) {
	l, _ := newTestLimiter(1, 60, 2)

	l.Allow("a")
	l.Allow("b")
	if l.Len() != 2 {
		t.Fatalf("Len = %d, want the map at its cap of 2", l.Len())
	}

	if ok, _ := l.Allow("c"); !ok {
		t.Error("a new key arriving at capacity was refused; it should pass untracked")
	}
	if l.Len() > 2 {
		t.Errorf("Len = %d, cap must hold at 2", l.Len())
	}
}

func TestConcurrentAllowIsRaceFree(t *testing.T) {
	l, _ := newTestLimiter(100, 6000, 1000)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Allow("shared")
			l.Allow(string(rune('a' + i%26)))
			l.Sweep()
		}()
	}
	wg.Wait()
}
