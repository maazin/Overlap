package server

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maazin/Overlap/api/internal/proposal"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestCache(ttl time.Duration) (*proposalCache, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)}
	pc := newProposalCache(ttl)
	pc.now = c.now
	return pc, c
}

func found(slot time.Time) proposal.Result {
	return proposal.Result{Slot: slot, Found: true, Considered: []string{"Ana"}}
}

// TestCooldownCollapsesRepeatCalls is the whole point. Each miss fans out to
// every connected member's calendar, so a caller looping on this endpoint
// without a cooldown is an outbound request amplifier.
func TestCooldownCollapsesRepeatCalls(t *testing.T) {
	pc, _ := newTestCache(30 * time.Second)
	var computed atomic.Int32

	compute := func() (proposal.Result, error) {
		computed.Add(1)
		return found(time.Date(2026, 5, 2, 14, 0, 0, 0, time.UTC)), nil
	}

	for range 50 {
		if _, err := pc.Do("event-1", compute); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}

	if got := computed.Load(); got != 1 {
		t.Errorf("computed %d times across 50 calls, want 1", got)
	}
}

func TestCooldownExpires(t *testing.T) {
	pc, clock := newTestCache(30 * time.Second)
	var computed atomic.Int32

	compute := func() (proposal.Result, error) {
		computed.Add(1)
		return found(time.Date(2026, 5, 2, 14, 0, 0, 0, time.UTC)), nil
	}

	pc.Do("event-1", compute)
	clock.advance(31 * time.Second)
	pc.Do("event-1", compute)

	if got := computed.Load(); got != 2 {
		t.Errorf("computed %d times, want 2: the cooldown must expire", got)
	}
}

func TestCooldownIsPerEvent(t *testing.T) {
	pc, _ := newTestCache(30 * time.Second)
	var computed atomic.Int32

	compute := func() (proposal.Result, error) {
		computed.Add(1)
		return found(time.Date(2026, 5, 2, 14, 0, 0, 0, time.UTC)), nil
	}

	pc.Do("event-1", compute)
	pc.Do("event-2", compute)

	if got := computed.Load(); got != 2 {
		t.Errorf("computed %d times, want 2: one event's answer is not another's", got)
	}
}

// TestConcurrentCallsFanOutOnce is the case a TTL alone does not cover. A
// hundred callers arriving together all miss an empty cache, and without
// single-flight they all fan out in parallel, which is precisely the burst
// worth preventing.
func TestConcurrentCallsFanOutOnce(t *testing.T) {
	pc, _ := newTestCache(30 * time.Second)
	var computed atomic.Int32

	release := make(chan struct{})
	compute := func() (proposal.Result, error) {
		computed.Add(1)
		<-release // hold the first computation open so the rest pile up
		return found(time.Date(2026, 5, 2, 14, 0, 0, 0, time.UTC)), nil
	}

	var wg sync.WaitGroup
	results := make([]proposal.Result, 100)
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := pc.Do("event-1", compute)
			if err != nil {
				t.Errorf("Do: %v", err)
				return
			}
			results[i] = r
		}()
	}

	// Give the goroutines time to arrive at the same key before unblocking.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := computed.Load(); got != 1 {
		t.Errorf("computed %d times for 100 concurrent callers, want 1", got)
	}
	for i, r := range results {
		if !r.Found {
			t.Fatalf("caller %d got no result", i)
		}
	}
}

// TestFailuresAreNotCached matters for correctness rather than load. A member
// whose calendar refresh failed is excluded from that one proposal, and
// pinning that exclusion for the whole cooldown would turn a transient network
// error into a lasting wrong answer.
func TestFailuresAreNotCached(t *testing.T) {
	pc, _ := newTestCache(30 * time.Second)
	var computed atomic.Int32

	boom := errors.New("calendar host unreachable")
	compute := func() (proposal.Result, error) {
		if computed.Add(1) == 1 {
			return proposal.Result{}, boom
		}
		return found(time.Date(2026, 5, 2, 14, 0, 0, 0, time.UTC)), nil
	}

	if _, err := pc.Do("event-1", compute); !errors.Is(err, boom) {
		t.Fatalf("first call error = %v, want the compute error", err)
	}

	r, err := pc.Do("event-1", compute)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !r.Found {
		t.Error("a retry after a failure should recompute rather than serve the failure")
	}
}

// TestZeroCooldownAlwaysRecomputes keeps the configuration honest: setting
// PROPOSAL_COOLDOWN to 0 must genuinely disable the cache.
func TestZeroCooldownAlwaysRecomputes(t *testing.T) {
	pc, _ := newTestCache(0)
	var computed atomic.Int32

	compute := func() (proposal.Result, error) {
		computed.Add(1)
		return found(time.Date(2026, 5, 2, 14, 0, 0, 0, time.UTC)), nil
	}

	pc.Do("event-1", compute)
	pc.Do("event-1", compute)

	if got := computed.Load(); got != 2 {
		t.Errorf("computed %d times with the cooldown disabled, want 2", got)
	}
}

func TestCacheIsBounded(t *testing.T) {
	pc, _ := newTestCache(time.Hour) // long enough that nothing goes stale
	compute := func() (proposal.Result, error) {
		return found(time.Date(2026, 5, 2, 14, 0, 0, 0, time.UTC)), nil
	}

	for i := range maxCachedProposals + 500 {
		pc.Do("event-"+strconv.Itoa(i), compute)
	}

	pc.mu.Lock()
	n := len(pc.values)
	pc.mu.Unlock()

	if n > maxCachedProposals {
		t.Errorf("cache holds %d entries, cap is %d", n, maxCachedProposals)
	}
}
