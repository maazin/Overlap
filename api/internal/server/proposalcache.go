package server

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/maazin/Overlap/api/internal/proposal"
)

// proposalCache bounds how often an event's proposal is actually recomputed.
//
// Recomputing means refreshing every connected member's calendar live, which
// is the most expensive thing this API does and the only one that makes
// outbound requests on a stranger's say-so. GET /api/groups/{slug}/proposal
// needs no token, so a per-caller rate limit still lets a hundred addresses
// each trigger a full fan-out. Keying the guard on the event instead caps the
// outbound work no matter how many callers ask.
//
// Two mechanisms, because either alone leaves a hole. The TTL stops a caller
// looping on one event. The single-flight group stops a hundred callers
// arriving together from all missing the cache at once and fanning out in
// parallel, which is exactly the burst worth preventing.
//
// Freshness is preserved where it matters: PRD section 10 requires that stale
// calendar data never silently produce a wrong proposal, and a cooldown
// measured in seconds is far inside the window in which a calendar could
// meaningfully change. Creating a group event computes its proposal directly
// and does not read this cache.
type proposalCache struct {
	ttl    time.Duration
	group  singleflight.Group
	now    func() time.Time
	mu     sync.Mutex
	values map[string]cachedProposal
}

type cachedProposal struct {
	result proposal.Result
	at     time.Time
}

// maxCachedProposals bounds the map. Keys come from URL slugs, so an unbounded
// map here would be a memory exhaustion vector dressed up as a cache.
const maxCachedProposals = 2000

func newProposalCache(ttl time.Duration) *proposalCache {
	return &proposalCache{
		ttl:    ttl,
		now:    time.Now,
		values: make(map[string]cachedProposal),
	}
}

// Do returns a recent proposal for key, calling compute only when there is no
// fresh one and no identical computation already running.
//
// A ttl of zero disables caching entirely, which keeps the behaviour
// configurable down to "always recompute" without a second code path.
func (c *proposalCache) Do(key string, compute func() (proposal.Result, error)) (proposal.Result, error) {
	if c == nil || c.ttl <= 0 {
		return compute()
	}

	if r, ok := c.lookup(key); ok {
		return r, nil
	}

	v, err, _ := c.group.Do(key, func() (any, error) {
		// Checked again inside the group: the caller that waited on an
		// in-flight computation arrives here with the answer already stored.
		if r, ok := c.lookup(key); ok {
			return r, nil
		}
		r, err := compute()
		if err != nil {
			// Failures are not cached. A member whose refresh failed is
			// excluded from that one proposal, and pinning that exclusion for
			// the whole TTL would turn a transient network error into a
			// lasting wrong answer.
			return proposal.Result{}, err
		}
		c.store(key, r)
		return r, nil
	})
	if err != nil {
		return proposal.Result{}, err
	}
	return v.(proposal.Result), nil
}

func (c *proposalCache) lookup(key string) (proposal.Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	v, ok := c.values[key]
	if !ok || c.now().Sub(v.at) >= c.ttl {
		return proposal.Result{}, false
	}
	return v.result, true
}

func (c *proposalCache) store(key string, r proposal.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.values) >= maxCachedProposals {
		c.evictStaleLocked()
		// Still full means every entry is fresh, so there is nothing safe to
		// drop. Skipping the write costs one recomputation and keeps the bound.
		if len(c.values) >= maxCachedProposals {
			return
		}
	}
	c.values[key] = cachedProposal{result: r, at: c.now()}
}

func (c *proposalCache) evictStaleLocked() {
	now := c.now()
	for key, v := range c.values {
		if now.Sub(v.at) >= c.ttl {
			delete(c.values, key)
		}
	}
}
