package solver

import (
	"fmt"
	"testing"
	"time"
)

// benchFixture builds a realistically-sized poll: a two-week window of
// half-hour slots across a 12-hour day, with most people answered and a few
// still outstanding. That mid-poll state is exactly when the dominance
// analysis matters most and runs on every page load.
func benchFixture(days, people, nonResponders int) ([]time.Time, []Participant, Responses) {
	var slots []time.Time
	base := time.Date(2026, 6, 1, 9, 0, 0, 0, ny)
	for d := range days {
		for i := range 24 { // 24 half-hour slots = 09:00 to 21:00
			slots = append(slots, base.AddDate(0, 0, d).Add(time.Duration(i)*30*time.Minute))
		}
	}

	ps := make([]Participant, people)
	for i := range ps {
		role := RoleOptional
		if i < people/2 {
			role = RoleRequired
		}
		ps[i] = Participant{ID: fmt.Sprintf("p%d", i), Name: fmt.Sprintf("P%d", i), Role: role, Loc: ny}
	}

	rs := make(Responses, people)
	tiers := []Tier{TierPreferred, TierOK, TierIfNeeded, TierNo}
	for i, p := range ps {
		if i >= people-nonResponders {
			continue // still outstanding
		}
		bySlot := make(map[time.Time]Tier, len(slots))
		for j, s := range slots {
			bySlot[s] = tiers[(i+j)%len(tiers)]
		}
		rs[p.ID] = bySlot
	}
	return slots, ps, rs
}

func BenchmarkAnalyzeMidPoll(b *testing.B) {
	slots, ps, rs := benchFixture(14, 12, 6)
	cfg := DefaultConfig()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		Analyze(slots, ps, rs, cfg)
	}
}

func BenchmarkAnalyzeSmallPoll(b *testing.B) {
	slots, ps, rs := benchFixture(5, 6, 2)
	cfg := DefaultConfig()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		Analyze(slots, ps, rs, cfg)
	}
}

func BenchmarkRank(b *testing.B) {
	slots, ps, rs := benchFixture(14, 12, 6)
	cfg := DefaultConfig()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		Rank(slots, ps, rs, cfg)
	}
}
