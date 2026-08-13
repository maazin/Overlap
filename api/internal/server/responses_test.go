package server

import (
	"testing"
	"time"

	"github.com/maazin/Overlap/api/internal/solver"
	"github.com/maazin/Overlap/api/internal/store"
)

var nyLoc, _ = time.LoadLocation("America/New_York")

// dayNY builds the 09:00-13:00 New York slots for 2026-03-10, hourly. March 10
// is on EDT, so 09:00 local is 13:00Z.
func dayNY() []time.Time {
	out := make([]time.Time, 0, 5)
	for h := 13; h <= 17; h++ {
		out = append(out, time.Date(2026, time.March, 10, h, 0, 0, 0, time.UTC))
	}
	return out
}

func byStart(rs []store.Response) map[time.Time]store.Response {
	m := make(map[time.Time]store.Response, len(rs))
	for _, r := range rs {
		m[r.SlotStart.UTC()] = r
	}
	return m
}

// TestTierVocabularyRoundTrips pins the two halves of the tier vocabulary
// together. parseTier lives in this package and Tier.String lives in solver, so
// nothing but a test stops them drifting apart.
func TestTierVocabularyRoundTrips(t *testing.T) {
	for _, tier := range []solver.Tier{
		solver.TierNo, solver.TierIfNeeded, solver.TierOK, solver.TierPreferred,
	} {
		got, err := parseTier(tier.String())
		if err != nil {
			t.Fatalf("parseTier(%q): %v", tier.String(), err)
		}
		if got != tier {
			t.Fatalf("round trip of %v gave %v", tier, got)
		}
	}
	if _, err := parseTier("sure_why_not"); err == nil {
		t.Fatal("want an error for an unknown tier")
	}
}

// TestCoarseOnlyIsAValidResponse is the "bailing early still counts" property.
// Someone who only completes the coarse pass has given usable signal, and the
// server must store it rather than demanding the fine pass.
func TestCoarseOnlyIsAValidResponse(t *testing.T) {
	starts := dayNY()

	got, err := buildResponses(starts, nyLoc, putResponsesRequest{
		Coarse: []coarseSelection{
			{Date: "2026-03-10", Block: "morning", Tier: "ok"},
		},
	})
	if err != nil {
		t.Fatalf("buildResponses: %v", err)
	}

	// 09:00, 10:00 and 11:00 are the morning; 12:00 and 13:00 are not.
	if len(got) != 3 {
		t.Fatalf("stored %d responses, want 3: %+v", len(got), got)
	}
	for _, r := range got {
		if r.Tier != solver.TierOK {
			t.Errorf("%s tier = %v, want ok", r.SlotStart.Format(time.RFC3339), r.Tier)
		}
		if r.Source != store.SourceCoarse {
			t.Errorf("%s source = %q, want %q", r.SlotStart.Format(time.RFC3339), r.Source, store.SourceCoarse)
		}
	}
}

// TestFineOverridesCoarse is the inheritance rule: a coarse tap sets the whole
// block, and a fine tap then overrides exactly one slot without disturbing its
// neighbours.
func TestFineOverridesCoarse(t *testing.T) {
	starts := dayNY()
	narrowed := starts[1] // 10:00 local

	got, err := buildResponses(starts, nyLoc, putResponsesRequest{
		Coarse: []coarseSelection{
			{Date: "2026-03-10", Block: "morning", Tier: "ok"},
		},
		Slots: []slotSelection{
			{SlotStart: narrowed, Tier: "no"},
		},
	})
	if err != nil {
		t.Fatalf("buildResponses: %v", err)
	}

	m := byStart(got)
	if len(m) != 3 {
		t.Fatalf("want 3 responses, got %d: %+v", len(m), got)
	}

	if r := m[narrowed]; r.Tier != solver.TierNo || r.Source != store.SourceManual {
		t.Errorf("narrowed slot = %v/%q, want no/manual", r.Tier, r.Source)
	}
	for _, other := range []time.Time{starts[0], starts[2]} {
		if r := m[other]; r.Tier != solver.TierOK || r.Source != store.SourceCoarse {
			t.Errorf("%s = %v/%q, want ok/coarse", other.Format(time.RFC3339), r.Tier, r.Source)
		}
	}
}

// TestFineCanUpgradeOutsideCoarse covers the other direction: a slot nobody
// selected coarsely can still be rated directly, and it arrives as manual.
func TestFineCanUpgradeOutsideCoarse(t *testing.T) {
	starts := dayNY()
	afternoon := starts[4] // 13:00 local, outside any coarse selection

	got, err := buildResponses(starts, nyLoc, putResponsesRequest{
		Slots: []slotSelection{{SlotStart: afternoon, Tier: "preferred"}},
	})
	if err != nil {
		t.Fatalf("buildResponses: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want a single response, got %+v", got)
	}
	if got[0].Tier != solver.TierPreferred || got[0].Source != store.SourceManual {
		t.Fatalf("got %v/%q, want preferred/manual", got[0].Tier, got[0].Source)
	}
}

// TestEmptySubmissionIsValid is the "nothing works for me" case. It stores no
// rows, and the caller marks the person as having responded, which is how the
// solver later tells a wall of no from silence.
func TestEmptySubmissionIsValid(t *testing.T) {
	got, err := buildResponses(dayNY(), nyLoc, putResponsesRequest{})
	if err != nil {
		t.Fatalf("an empty submission must be accepted: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no rows, got %+v", got)
	}
}

// TestSlotOutsideEventIsRejected stops a client writing availability against an
// instant the event does not contain, which would never be scored and would
// misrepresent how complete the answer is.
func TestSlotOutsideEventIsRejected(t *testing.T) {
	starts := dayNY()
	bogus := starts[0].Add(7 * time.Minute)

	_, err := buildResponses(starts, nyLoc, putResponsesRequest{
		Slots: []slotSelection{{SlotStart: bogus, Tier: "ok"}},
	})
	if err == nil {
		t.Fatal("want an error for a slot that is not part of the event")
	}
}

// TestSlotMatchIsByInstantNotWallClock: a client may legitimately send the same
// moment expressed in its own offset, and that must match.
func TestSlotMatchIsByInstantNotWallClock(t *testing.T) {
	starts := dayNY()
	sameMoment := starts[0].In(nyLoc) // identical instant, different rendering

	got, err := buildResponses(starts, nyLoc, putResponsesRequest{
		Slots: []slotSelection{{SlotStart: sameMoment, Tier: "ok"}},
	})
	if err != nil {
		t.Fatalf("an equivalent instant must be accepted: %v", err)
	}
	if len(got) != 1 || !got[0].SlotStart.Equal(starts[0]) {
		t.Fatalf("got %+v, want the canonical slot instant", got)
	}
}

func TestMalformedCoarseIsRejected(t *testing.T) {
	starts := dayNY()

	for _, tc := range []struct {
		name string
		sel  coarseSelection
	}{
		{"bad block", coarseSelection{Date: "2026-03-10", Block: "brunch", Tier: "ok"}},
		{"bad date", coarseSelection{Date: "10/03/2026", Block: "morning", Tier: "ok"}},
		{"bad tier", coarseSelection{Date: "2026-03-10", Block: "morning", Tier: "great"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildResponses(starts, nyLoc, putResponsesRequest{
				Coarse: []coarseSelection{tc.sel},
			}); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// TestResponsesAreOrdered keeps stored rows deterministic, which matters for
// diffing a resubmission and for readable test failures.
func TestResponsesAreOrdered(t *testing.T) {
	starts := dayNY()

	got, err := buildResponses(starts, nyLoc, putResponsesRequest{
		Slots: []slotSelection{
			{SlotStart: starts[3], Tier: "ok"},
			{SlotStart: starts[0], Tier: "ok"},
			{SlotStart: starts[2], Tier: "ok"},
		},
	})
	if err != nil {
		t.Fatalf("buildResponses: %v", err)
	}
	for i := 1; i < len(got); i++ {
		if !got[i-1].SlotStart.Before(got[i].SlotStart) {
			t.Fatalf("responses are not sorted by slot start: %+v", got)
		}
	}
}

// TestSubmittingDoesNotWipeCalendarRows is the interaction between the two
// write paths. A response replaces the whole set so that a slot can be
// withdrawn, which means without care it also deletes everything the calendar
// contributed -- and times somebody is genuinely booked would come back as
// available.
func TestSubmittingDoesNotWipeCalendarRows(t *testing.T) {
	starts := dayNY()

	prior := []store.Response{
		{SlotStart: starts[3], Tier: solver.TierNo, Source: store.SourceCalendar},
		{SlotStart: starts[4], Tier: solver.TierNo, Source: store.SourceCalendar},
	}
	stated := []store.Response{
		{SlotStart: starts[0], Tier: solver.TierOK, Source: store.SourceCoarse},
		// The person says this one works despite the calendar.
		{SlotStart: starts[3], Tier: solver.TierPreferred, Source: store.SourceManual},
	}

	got := mergeCalendarRows(stated, prior)
	m := byStart(got)

	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(got), got)
	}
	// Stated beats inferred.
	if r := m[starts[3]]; r.Tier != solver.TierPreferred || r.Source != store.SourceManual {
		t.Errorf("stated slot = %v/%q, want preferred/manual", r.Tier, r.Source)
	}
	// Untouched calendar row survives.
	if r := m[starts[4]]; r.Tier != solver.TierNo || r.Source != store.SourceCalendar {
		t.Errorf("untouched calendar slot = %v/%q, want no/calendar", r.Tier, r.Source)
	}
	for i := 1; i < len(got); i++ {
		if !got[i-1].SlotStart.Before(got[i].SlotStart) {
			t.Fatalf("merged rows are not ordered: %+v", got)
		}
	}
}
