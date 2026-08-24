// Package proposal derives a scheduling suggestion from stored free/busy data,
// before anyone has stated a preference.
//
// A proposal answers a narrower question than the solver does. The solver
// ranks slots by how much people want them, using tiers someone actually
// entered. A proposal has no such data to work from -- it exists for the
// moment before anyone has responded -- so it can only ask whether a slot is
// free for everyone whose calendar is connected. That is why this is a
// separate, smaller package rather than another call into solver.Rank: the
// two answer different questions and conflating them would let calendar
// inference quietly stand in for a stated preference, which is exactly the
// confident wrongness the product exists to avoid.
//
// The package is pure. Which calendars are stale, and how to refresh them, are
// the caller's job.
package proposal

import (
	"sort"
	"time"

	"github.com/maazin/Overlap/api/internal/icsparse"
)

// Member is one person whose calendar may or may not be connected.
type Member struct {
	ID   string
	Name string
	// Busy is nil when this member has no connected calendar. A nil slice and
	// an empty one mean different things: nil means "we don't know", empty
	// means "we checked and they are free the whole window".
	Busy []icsparse.Interval
}

// Result is what a proposal computation found.
type Result struct {
	// Slot is the suggested time. Zero when nothing qualified.
	Slot time.Time
	// Found reports whether Slot is meaningful. A proposal with nothing
	// connected, or with no slot free for everyone connected, is not an error;
	// it is the honest "there is nothing to suggest yet" and the caller falls
	// back to an ordinary poll.
	Found bool
	// Considered lists the members whose calendars were used to reach the
	// verdict. Members with no connected calendar are not in this list, and
	// are not what the proposal claims to speak for.
	Considered []string
}

// Best returns the earliest slot that is free for every member whose
// calendar is connected.
//
// "Free for everyone connected" rather than "the most free slot" is the
// deliberate choice: a proposal is offered as something confirmable without
// asking anyone, per the PRD's rule that a proposal is a suggestion and never
// an automatic booking. A slot that merely maximises attendance is a poll
// result, and that is what the ordinary solver is for.
func Best(candidates []time.Time, duration time.Duration, members []Member) Result {
	connected := make([]Member, 0, len(members))
	for _, m := range members {
		if m.Busy != nil {
			connected = append(connected, m)
		}
	}
	if len(connected) == 0 {
		return Result{}
	}

	names := make([]string, len(connected))
	for i, m := range connected {
		names[i] = m.Name
	}
	sort.Strings(names)

	sorted := append([]time.Time(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })

	for _, slot := range sorted {
		if freeForAll(slot, duration, connected) {
			return Result{Slot: slot, Found: true, Considered: names}
		}
	}
	return Result{Considered: names}
}

// freeForAll checks the whole meeting span against each connected member's
// busy intervals, not just its start instant. A slot that starts clear but
// runs into a meeting ten minutes later is not free, and a point check would
// have said it was.
func freeForAll(slot time.Time, duration time.Duration, members []Member) bool {
	span := icsparse.Interval{Start: slot, End: slot.Add(duration)}
	for _, m := range members {
		for _, b := range m.Busy {
			if span.Overlaps(b) {
				return false
			}
		}
	}
	return true
}
