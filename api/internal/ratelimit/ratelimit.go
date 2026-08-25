// Package ratelimit is a per-key token bucket with bounded memory.
//
// It is in-process on purpose. A limiter backed by Postgres would put a write
// on the hot path of every request, against the same small database it is
// meant to protect, so under the flood it exists to handle it becomes part of
// the load. Losing counters on deploy costs nothing here: this enforces no
// quota anyone paid for, and an abuser sending thousands of requests a minute
// is stopped inside the first window either way.
//
// What it cannot do is stop an attacker who rotates source addresses. That is
// a property of per-IP limiting rather than of this implementation, and it is
// why the expensive fan-out path is additionally guarded by a cooldown keyed
// on the resource instead of on the caller.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter hands out tokens per key, refilling continuously.
//
// The zero value is not usable; call New.
type Limiter struct {
	// burst is the bucket depth: how many requests can arrive at once before
	// the rate starts to bite.
	burst float64
	// refill is tokens added per second.
	refill float64
	// maxKeys caps how many buckets are tracked at once. Untrusted input keys
	// a map, so leaving it unbounded would make the limiter its own denial of
	// service.
	maxKeys int

	// now is injectable so tests can advance time without sleeping.
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New builds a limiter allowing burst requests immediately and perMinute
// sustained.
func New(burst int, perMinute float64, maxKeys int) *Limiter {
	return &Limiter{
		burst:   float64(burst),
		refill:  perMinute / 60,
		maxKeys: maxKeys,
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
}

// Allow reports whether the key may proceed, spending a token if so.
//
// It also returns how long the caller should wait before trying again, which
// is zero when the request was allowed. Handlers turn that into Retry-After,
// so a client that respects it stops guessing.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		// At capacity, allow rather than refuse. Being at capacity means
		// thousands of distinct keys are active, which is a distributed
		// attack, and a per-key limiter was never going to stop one. Refusing
		// here would convert an attack this cannot prevent into an outage for
		// everyone whose key happens to arrive after the map filled up.
		if len(l.buckets) >= l.maxKeys {
			l.evictLocked(now)
			if len(l.buckets) >= l.maxKeys {
				return true, 0
			}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	// Lazy refill. Storing the last-seen time instead of running a ticker per
	// key is what makes an idle bucket free to keep and trivial to drop: a
	// bucket that has refilled to full carries no information a new one would
	// not have.
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.refill)
		b.last = now
	}

	if b.tokens < 1 {
		// Time until one whole token exists.
		wait := time.Duration((1 - b.tokens) / l.refill * float64(time.Second))
		return false, wait
	}

	b.tokens--
	return true, 0
}

// Sweep drops buckets that have refilled to full, which is every key that has
// been quiet for burst/refill seconds. Callers run it on a ticker; Allow also
// calls it when the map hits its cap.
//
// It returns how many were dropped, which is worth logging only when it is
// large enough to say something about traffic.
func (l *Limiter) Sweep() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.evictLocked(l.now())
}

func (l *Limiter) evictLocked(now time.Time) int {
	dropped := 0
	for key, b := range l.buckets {
		elapsed := now.Sub(b.last).Seconds()
		if b.tokens+elapsed*l.refill >= l.burst {
			delete(l.buckets, key)
			dropped++
		}
	}
	return dropped
}

// Len reports how many keys are currently tracked. Exists for tests and for
// the sweep log line.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
