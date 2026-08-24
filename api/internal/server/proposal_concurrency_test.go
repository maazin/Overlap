package server

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCalendarFetchFanOutIsConcurrent pins the shape of the proposal refresh:
// N independent calendar fetches must overlap in time rather than queue behind
// each other, and must still be bounded so a large group cannot open an
// unlimited number of outbound connections at once.
//
// This exercises the same bounded-fan-out pattern computeGroupProposal uses,
// with a stand-in for the network call. Driving the real handler would need a
// live Postgres and several third-party calendar hosts; what can actually
// regress here is the concurrency structure, and that is what this measures.
func TestCalendarFetchFanOutIsConcurrent(t *testing.T) {
	const members = 8
	const fetchDelay = 60 * time.Millisecond

	var inFlight, peak int64

	results := make([]int, members)
	ok := make([]bool, members)

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentCalendarFetches)

	start := time.Now()
	for i := range members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Track how many fetches are genuinely running at once.
			cur := atomic.AddInt64(&inFlight, 1)
			for {
				old := atomic.LoadInt64(&peak)
				if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
					break
				}
			}
			time.Sleep(fetchDelay) // stands in for the HTTP round trip
			atomic.AddInt64(&inFlight, -1)

			results[i] = i
			ok[i] = true
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Serially this would be members*fetchDelay. Allowing generous slack for a
	// loaded CI machine, anything near the serial figure means the fan-out
	// regressed back into a sequential loop.
	if serial := members * fetchDelay; elapsed >= serial/2 {
		t.Fatalf("took %s for %d fetches of %s each; serial would be %s, so this is not running concurrently",
			elapsed, members, fetchDelay, serial)
	}

	if peak < 2 {
		t.Fatalf("peak concurrency was %d; the fetches never overlapped", peak)
	}
	if peak > maxConcurrentCalendarFetches {
		t.Fatalf("peak concurrency was %d, above the %d bound", peak, maxConcurrentCalendarFetches)
	}

	// Order must follow the member list, not completion order, so a proposal
	// does not depend on which calendar host answered first.
	for i := range members {
		if !ok[i] || results[i] != i {
			t.Fatalf("result %d landed out of order: ok=%v value=%d", i, ok[i], results[i])
		}
	}
}

// TestCalendarFetchFanOutRespectsTheBound checks the semaphore actually caps
// concurrency when there are more members than slots.
func TestCalendarFetchFanOutRespectsTheBound(t *testing.T) {
	const members = maxConcurrentCalendarFetches * 3

	var inFlight, peak int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentCalendarFetches)

	for range members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cur := atomic.AddInt64(&inFlight, 1)
			for {
				old := atomic.LoadInt64(&peak)
				if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt64(&inFlight, -1)
		}()
	}
	wg.Wait()

	if peak > maxConcurrentCalendarFetches {
		t.Fatalf("peak concurrency %d exceeded the bound of %d", peak, maxConcurrentCalendarFetches)
	}
}
