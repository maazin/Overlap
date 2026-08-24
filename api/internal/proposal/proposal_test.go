package proposal

import (
	"testing"
	"time"

	"github.com/maazin/Overlap/api/internal/icsparse"
)

func at(hour int) time.Time {
	return time.Date(2026, 6, 10, hour, 0, 0, 0, time.UTC)
}

func slots(hours ...int) []time.Time {
	out := make([]time.Time, len(hours))
	for i, h := range hours {
		out[i] = at(h)
	}
	return out
}

func TestNoConnectedCalendarsYieldsNoProposal(t *testing.T) {
	got := Best(slots(9, 10, 11), time.Hour, []Member{
		{ID: "a", Name: "Ana", Busy: nil},
		{ID: "b", Name: "Ben", Busy: nil},
	})
	if got.Found {
		t.Fatal("nobody has a connected calendar; there is nothing to propose")
	}
	if len(got.Considered) != 0 {
		t.Fatalf("Considered = %v, want empty", got.Considered)
	}
}

// TestPicksTheEarliestFullyFreeSlot is the ordinary case: two connected
// members, one slot works for both.
func TestPicksTheEarliestFullyFreeSlot(t *testing.T) {
	got := Best(slots(9, 10, 11), time.Hour, []Member{
		{ID: "a", Name: "Ana", Busy: []icsparse.Interval{{Start: at(9), End: at(10)}}},
		{ID: "b", Name: "Ben", Busy: []icsparse.Interval{{Start: at(11), End: at(12)}}},
	})
	if !got.Found || !got.Slot.Equal(at(10)) {
		t.Fatalf("got %+v, want 10:00", got)
	}
}

// TestConflictingMemberIsExcludedFromTheWinner is the DoD case: a member with
// a conflict must not make the slot look clear.
func TestConflictingMemberIsExcludedFromTheWinner(t *testing.T) {
	got := Best(slots(9), time.Hour, []Member{
		{ID: "a", Name: "Ana", Busy: nil},
		{ID: "b", Name: "Ben", Busy: []icsparse.Interval{{Start: at(9), End: at(10)}}},
	})
	if got.Found {
		t.Fatal("Ben's conflict should rule out the only candidate slot")
	}
	if len(got.Considered) != 1 || got.Considered[0] != "Ben" {
		t.Fatalf("Considered = %v, want only Ben, the one connected member", got.Considered)
	}
}

// TestPartialOverlapCounts guards the bug this package was rewritten to avoid:
// a meeting that starts inside the candidate slot, not before it, still makes
// the slot unusable.
func TestPartialOverlapCounts(t *testing.T) {
	got := Best(slots(9), time.Hour, []Member{
		// Busy 09:30-10:30 doesn't contain the slot's start (09:00) but does
		// collide with its second half.
		{ID: "a", Name: "Ana", Busy: []icsparse.Interval{{Start: at(9).Add(30 * time.Minute), End: at(10).Add(30 * time.Minute)}}},
	})
	if got.Found {
		t.Fatal("a meeting overlapping the back half of the slot must still block it")
	}
}

func TestEverybodyFreeReturnsTheFirstCandidate(t *testing.T) {
	got := Best(slots(11, 9, 10), time.Hour, []Member{
		{ID: "a", Name: "Ana", Busy: []icsparse.Interval{}},
	})
	if !got.Found || !got.Slot.Equal(at(9)) {
		t.Fatalf("got %+v, want the earliest candidate (09:00) regardless of input order", got)
	}
}

func TestNoSlotWorksForEveryone(t *testing.T) {
	got := Best(slots(9, 10), time.Hour, []Member{
		{ID: "a", Name: "Ana", Busy: []icsparse.Interval{{Start: at(9), End: at(10)}}},
		{ID: "b", Name: "Ben", Busy: []icsparse.Interval{{Start: at(10), End: at(11)}}},
	})
	if got.Found {
		t.Fatalf("got %+v, want no proposal: every slot conflicts with somebody", got)
	}
}

func TestUnconnectedMemberDoesNotBlockOrCount(t *testing.T) {
	got := Best(slots(9), time.Hour, []Member{
		{ID: "a", Name: "Ana", Busy: []icsparse.Interval{}},
		{ID: "b", Name: "Ben", Busy: nil}, // not connected
	})
	if !got.Found || !got.Slot.Equal(at(9)) {
		t.Fatalf("got %+v, want 09:00, unaffected by Ben's unknown calendar", got)
	}
	if len(got.Considered) != 1 || got.Considered[0] != "Ana" {
		t.Fatalf("Considered = %v, want only Ana", got.Considered)
	}
}

func TestNoCandidateSlotsYieldsNoProposal(t *testing.T) {
	got := Best(nil, time.Hour, []Member{{ID: "a", Name: "Ana", Busy: []icsparse.Interval{}}})
	if got.Found {
		t.Fatal("an empty candidate list has nothing to propose")
	}
}
