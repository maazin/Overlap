package slots

import (
	"errors"
	"testing"
	"time"
)

var (
	ny, _   = time.LoadLocation("America/New_York")
	lhi, _  = time.LoadLocation("Australia/Lord_Howe")
	syd, _  = time.LoadLocation("Australia/Sydney")
	kolk, _ = time.LoadLocation("Asia/Kolkata")
)

// utc renders instants for comparison. Every expectation in this file is
// written as a UTC wall clock, because that is the only representation with no
// ambiguity to argue about.
func utc(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.UTC().Format("2006-01-02 15:04")
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d slots, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("slot %d = %s, want %s\n got: %v\nwant: %v", i, got[i], want[i], got, want)
		}
	}
}

func mustExpand(t *testing.T, w Window) Expansion {
	t.Helper()
	exp, err := Expand(w)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	return exp
}

// --- the DoD case ------------------------------------------------------------

// TestSpringForwardWindowKeepsLocalBand is the headline requirement: a window
// spanning the US spring-forward transition produces the right count and the
// right absolute instants.
//
// 9am stays 9am on both sides of the transition, so the underlying UTC instant
// moves by an hour. Hand-computed: EST is UTC-5, EDT is UTC-4, and the change
// lands on 2026-03-08.
func TestSpringForwardWindowKeepsLocalBand(t *testing.T) {
	exp := mustExpand(t, Window{
		Start:       Date{2026, time.March, 6},
		End:         Date{2026, time.March, 9},
		DayStart:    TimeOfDay{9, 0},
		DayEnd:      TimeOfDay{11, 0},
		SlotMinutes: 60,
		Loc:         ny,
	})

	equal(t, utc(exp.Starts), []string{
		// Friday and Saturday: EST, 9am local = 14:00 UTC.
		"2026-03-06 14:00", "2026-03-06 15:00",
		"2026-03-07 14:00", "2026-03-07 15:00",
		// Sunday onward: EDT, 9am local = 13:00 UTC.
		"2026-03-08 13:00", "2026-03-08 14:00",
		"2026-03-09 13:00", "2026-03-09 14:00",
	})

	if len(exp.Skipped) != 0 || len(exp.Ambiguous) != 0 {
		t.Fatalf("a 9-11am band never touches the 2am transition: skipped=%v ambiguous=%v",
			exp.Skipped, exp.Ambiguous)
	}
}

// TestNonexistentLocalTimesAreSkipped drives the band straight through the
// spring-forward gap.
//
// On 2026-03-08 the clock jumps 02:00 EST -> 03:00 EDT, so 02:00 and 02:30
// never occur. The property worth staring at is the UTC column: once the two
// impossible wall clocks are dropped, the remaining instants are an unbroken
// 30-minute sequence. No real time is lost and none is served twice, which is
// exactly what "handled deliberately" has to mean.
func TestNonexistentLocalTimesAreSkipped(t *testing.T) {
	exp := mustExpand(t, Window{
		Start:       Date{2026, time.March, 8},
		End:         Date{2026, time.March, 8},
		DayStart:    TimeOfDay{0, 0},
		DayEnd:      TimeOfDay{6, 0},
		SlotMinutes: 30,
		Loc:         ny,
	})

	equal(t, utc(exp.Starts), []string{
		"2026-03-08 05:00", // 00:00 EST
		"2026-03-08 05:30", // 00:30 EST
		"2026-03-08 06:00", // 01:00 EST
		"2026-03-08 06:30", // 01:30 EST
		// 02:00 and 02:30 EST do not exist.
		"2026-03-08 07:00", // 03:00 EDT
		"2026-03-08 07:30", // 03:30 EDT
		"2026-03-08 08:00", // 04:00 EDT
		"2026-03-08 08:30", // 04:30 EDT
		"2026-03-08 09:00", // 05:00 EDT
		"2026-03-08 09:30", // 05:30 EDT
	})

	if len(exp.Skipped) != 2 {
		t.Fatalf("Skipped = %v, want the two impossible wall clocks", exp.Skipped)
	}
	for i, want := range []TimeOfDay{{2, 0}, {2, 30}} {
		if exp.Skipped[i].Local != want {
			t.Fatalf("Skipped[%d] = %s, want %s", i, exp.Skipped[i].Local, want)
		}
		if exp.Skipped[i].Kind != KindNonexistent {
			t.Fatalf("Skipped[%d] kind = %v, want KindNonexistent", i, exp.Skipped[i].Kind)
		}
	}
}

// TestNaiveExpansionWouldHaveCollided pins the specific bug this package
// exists to prevent. Go resolves the nonexistent 02:30 *backward* onto 01:30,
// so trusting time.Date yields two slots sharing one instant.
func TestNaiveExpansionWouldHaveCollided(t *testing.T) {
	real := time.Date(2026, time.March, 8, 1, 30, 0, 0, ny)
	impossible := time.Date(2026, time.March, 8, 2, 30, 0, 0, ny)

	if !real.Equal(impossible) {
		t.Fatalf("premise changed: 01:30 and 02:30 no longer collide (%s vs %s)", real, impossible)
	}

	exp := mustExpand(t, Window{
		Start: Date{2026, time.March, 8}, End: Date{2026, time.March, 8},
		DayStart: TimeOfDay{1, 0}, DayEnd: TimeOfDay{3, 0},
		SlotMinutes: 30, Loc: ny,
	})

	seen := map[int64]bool{}
	for _, s := range exp.Starts {
		if seen[s.UnixNano()] {
			t.Fatalf("duplicate instant %s in %v", s, utc(exp.Starts))
		}
		seen[s.UnixNano()] = true
	}
}

// TestFallBackAmbiguityResolvesToFirstOccurrence drives the band through the
// repeated hour.
//
// On 2026-11-01 the clock rewinds 02:00 EDT -> 01:00 EST, so 01:00 and 01:30
// each happen twice. Both resolve to the first occurrence, which leaves a
// deliberate 90-minute gap in the UTC sequence: the second pass through that
// hour is real time with no slot on it. That is the cost of keeping one wall
// clock equal to one slot, and it is a choice rather than an accident.
func TestFallBackAmbiguityResolvesToFirstOccurrence(t *testing.T) {
	exp := mustExpand(t, Window{
		Start:       Date{2026, time.November, 1},
		End:         Date{2026, time.November, 1},
		DayStart:    TimeOfDay{0, 0},
		DayEnd:      TimeOfDay{4, 0},
		SlotMinutes: 30,
		Loc:         ny,
	})

	equal(t, utc(exp.Starts), []string{
		"2026-11-01 04:00", // 00:00 EDT
		"2026-11-01 04:30", // 00:30 EDT
		"2026-11-01 05:00", // 01:00 EDT, first pass
		"2026-11-01 05:30", // 01:30 EDT, first pass
		// 06:00 and 06:30 UTC are the second pass; deliberately unscheduled.
		"2026-11-01 07:00", // 02:00 EST
		"2026-11-01 07:30", // 02:30 EST
		"2026-11-01 08:00", // 03:00 EST
		"2026-11-01 08:30", // 03:30 EST
	})

	if len(exp.Ambiguous) != 2 {
		t.Fatalf("Ambiguous = %v, want the two repeated wall clocks", exp.Ambiguous)
	}
	for i, want := range []TimeOfDay{{1, 0}, {1, 30}} {
		if exp.Ambiguous[i].Local != want {
			t.Fatalf("Ambiguous[%d] = %s, want %s", i, exp.Ambiguous[i].Local, want)
		}
	}
	// The recorded instant must be the earlier of the two, not whichever one
	// time.Date happened to hand back.
	firstPass := exp.Ambiguous[0].Chosen
	if got := firstPass.UTC().Format("15:04"); got != "05:00" {
		t.Fatalf("chose %s UTC for the ambiguous 01:00, want the 05:00 first pass", got)
	}
	if _, off := firstPass.Zone(); off != -4*3600 {
		t.Fatalf("offset = %d, want EDT (-4h) for the first pass", off/3600)
	}
	if len(exp.Skipped) != 0 {
		t.Fatalf("nothing is impossible on a fall-back day, got %v", exp.Skipped)
	}
}

// TestFallBackDayHasNoDuplicates is the counterpart to the collision test: the
// repeated hour must not produce two slots that render identically.
func TestFallBackDayHasNoDuplicates(t *testing.T) {
	exp := mustExpand(t, Window{
		Start: Date{2026, time.November, 1}, End: Date{2026, time.November, 1},
		DayStart: TimeOfDay{0, 0}, DayEnd: TimeOfDay{6, 0},
		SlotMinutes: 15, Loc: ny,
	})

	seen := map[int64]bool{}
	for _, s := range exp.Starts {
		if seen[s.UnixNano()] {
			t.Fatalf("duplicate instant %s", s)
		}
		seen[s.UnixNano()] = true
	}
	if !sortedAscending(exp.Starts) {
		t.Fatal("slots must come out in ascending order")
	}
}

// --- transitions that are not the US one -------------------------------------

// TestHalfHourTransition exercises Lord Howe Island, whose DST shift is thirty
// minutes rather than an hour. A probe that only ever looked an hour away would
// miss this entirely and silently mislabel ambiguity as normal.
func TestHalfHourTransition(t *testing.T) {
	// 2026-10-04: +10:30 -> +11:00, so 02:00-02:29 local never happens.
	spring := mustExpand(t, Window{
		Start: Date{2026, time.October, 4}, End: Date{2026, time.October, 4},
		DayStart: TimeOfDay{1, 0}, DayEnd: TimeOfDay{4, 0},
		SlotMinutes: 15, Loc: lhi,
	})
	if len(spring.Skipped) == 0 {
		t.Fatalf("expected a skipped half hour on the Lord Howe spring transition, got %v",
			utc(spring.Starts))
	}
	for _, a := range spring.Skipped {
		if a.Local.Hour != 2 || a.Local.Minute >= 30 {
			t.Fatalf("unexpected skipped wall clock %s", a.Local)
		}
	}

	// 2026-04-05: +11:00 -> +10:30, so 01:30-01:59 local happens twice.
	fall := mustExpand(t, Window{
		Start: Date{2026, time.April, 5}, End: Date{2026, time.April, 5},
		DayStart: TimeOfDay{1, 0}, DayEnd: TimeOfDay{4, 0},
		SlotMinutes: 15, Loc: lhi,
	})
	if len(fall.Ambiguous) == 0 {
		t.Fatalf("expected an ambiguous half hour on the Lord Howe autumn transition, got %v",
			utc(fall.Starts))
	}
}

// TestSouthernHemisphereTransitionsRunTheOtherWay guards against tests that
// only ever pass because they were written against the US calendar.
func TestSouthernHemisphereTransitionsRunTheOtherWay(t *testing.T) {
	// Sydney springs forward in October and falls back in April.
	spring := mustExpand(t, Window{
		Start: Date{2026, time.October, 4}, End: Date{2026, time.October, 4},
		DayStart: TimeOfDay{1, 0}, DayEnd: TimeOfDay{4, 0},
		SlotMinutes: 30, Loc: syd,
	})
	if len(spring.Skipped) != 2 {
		t.Fatalf("Sydney spring forward should skip 02:00 and 02:30, got %v", spring.Skipped)
	}

	fall := mustExpand(t, Window{
		Start: Date{2026, time.April, 5}, End: Date{2026, time.April, 5},
		DayStart: TimeOfDay{1, 0}, DayEnd: TimeOfDay{4, 0},
		SlotMinutes: 30, Loc: syd,
	})
	if len(fall.Ambiguous) != 2 {
		t.Fatalf("Sydney fall back should repeat 02:00 and 02:30, got %v", fall.Ambiguous)
	}
}

// TestZoneWithoutDSTIsUneventful covers the common case, including a zone whose
// offset is not a whole number of hours.
func TestZoneWithoutDSTIsUneventful(t *testing.T) {
	exp := mustExpand(t, Window{
		Start: Date{2026, time.March, 8}, End: Date{2026, time.March, 8},
		DayStart: TimeOfDay{9, 0}, DayEnd: TimeOfDay{11, 0},
		SlotMinutes: 60, Loc: kolk, // UTC+5:30 year round
	})

	equal(t, utc(exp.Starts), []string{
		"2026-03-08 03:30", // 09:00 IST
		"2026-03-08 04:30", // 10:00 IST
	})
	if len(exp.Skipped) != 0 || len(exp.Ambiguous) != 0 {
		t.Fatal("a zone without DST can have no anomalies")
	}
}

// --- band and layout ---------------------------------------------------------

// TestSlotsMustFitInsideTheBand: 45 minute slots step from 9:00, so they run
// 9:00, 9:45 ... 15:45. The next start would be 16:30, which overhangs a 17:00
// band, so the day ends with 30 minutes of unusable remainder. Note that 16:15
// is never a candidate — it is not on the grid.
func TestSlotsMustFitInsideTheBand(t *testing.T) {
	exp := mustExpand(t, Window{
		Start: Date{2026, time.June, 1}, End: Date{2026, time.June, 1},
		DayStart: TimeOfDay{9, 0}, DayEnd: TimeOfDay{17, 0},
		SlotMinutes: 45, Loc: ny,
	})

	if n := len(exp.Starts); n != 10 {
		t.Fatalf("got %d slots, want 10 (9:00 through 15:45)", n)
	}
	last := exp.Starts[len(exp.Starts)-1].In(ny)
	if last.Hour() != 15 || last.Minute() != 45 {
		t.Fatalf("last slot starts %02d:%02d local, want 15:45", last.Hour(), last.Minute())
	}
	// The slot must end no later than the band does.
	if end := last.Add(45 * time.Minute); end.Hour() > 17 || (end.Hour() == 17 && end.Minute() > 0) {
		t.Fatalf("last slot ends %02d:%02d, overhanging the 17:00 band", end.Hour(), end.Minute())
	}
}

func TestSingleDayWindowIsInclusive(t *testing.T) {
	exp := mustExpand(t, Window{
		Start: Date{2026, time.June, 1}, End: Date{2026, time.June, 1},
		DayStart: TimeOfDay{9, 0}, DayEnd: TimeOfDay{10, 0},
		SlotMinutes: 60, Loc: ny,
	})
	if len(exp.Starts) != 1 {
		t.Fatalf("got %d slots, want 1", len(exp.Starts))
	}
}

// TestWindowCrossesMonthAndYearBoundaries guards the date-increment helper,
// which is the sort of thing that works for eleven months of the year.
func TestWindowCrossesMonthAndYearBoundaries(t *testing.T) {
	exp := mustExpand(t, Window{
		Start: Date{2026, time.December, 30}, End: Date{2027, time.January, 2},
		DayStart: TimeOfDay{9, 0}, DayEnd: TimeOfDay{10, 0},
		SlotMinutes: 60, Loc: ny,
	})

	equal(t, utc(exp.Starts), []string{
		"2026-12-30 14:00",
		"2026-12-31 14:00",
		"2027-01-01 14:00",
		"2027-01-02 14:00",
	})
}

// TestLeapDay: 2028 is a leap year, so the window must not skip 29 February.
func TestLeapDay(t *testing.T) {
	exp := mustExpand(t, Window{
		Start: Date{2028, time.February, 28}, End: Date{2028, time.March, 1},
		DayStart: TimeOfDay{12, 0}, DayEnd: TimeOfDay{13, 0},
		SlotMinutes: 60, Loc: time.UTC,
	})

	equal(t, utc(exp.Starts), []string{
		"2028-02-28 12:00",
		"2028-02-29 12:00",
		"2028-03-01 12:00",
	})
}

// TestEndOfDayBandIsAllowed: 24:00 is a legal end bound, matching Postgres.
func TestEndOfDayBandIsAllowed(t *testing.T) {
	exp := mustExpand(t, Window{
		Start: Date{2026, time.June, 1}, End: Date{2026, time.June, 1},
		DayStart: TimeOfDay{22, 0}, DayEnd: TimeOfDay{24, 0},
		SlotMinutes: 60, Loc: time.UTC,
	})

	equal(t, utc(exp.Starts), []string{
		"2026-06-01 22:00",
		"2026-06-01 23:00",
	})
}

// --- validation --------------------------------------------------------------

func TestValidation(t *testing.T) {
	base := Window{
		Start: Date{2026, time.June, 1}, End: Date{2026, time.June, 2},
		DayStart: TimeOfDay{9, 0}, DayEnd: TimeOfDay{17, 0},
		SlotMinutes: 30, Loc: ny,
	}

	for _, tc := range []struct {
		name   string
		mutate func(*Window)
		want   error
	}{
		{"no location", func(w *Window) { w.Loc = nil }, ErrNoLocation},
		{"zero slot size", func(w *Window) { w.SlotMinutes = 0 }, ErrBadSlotSize},
		{"negative slot size", func(w *Window) { w.SlotMinutes = -30 }, ErrBadSlotSize},
		{"inverted day band", func(w *Window) { w.DayStart, w.DayEnd = w.DayEnd, w.DayStart }, ErrBadDayBand},
		{"empty day band", func(w *Window) { w.DayEnd = w.DayStart }, ErrBadDayBand},
		{"inverted dates", func(w *Window) { w.Start, w.End = w.End, w.Start }, ErrBadDateRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := base
			tc.mutate(&w)
			if _, err := Expand(w); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestOversizedWindowIsRefused: the window is organizer-supplied input, so an
// absurd one has to be rejected rather than allocated.
func TestOversizedWindowIsRefused(t *testing.T) {
	_, err := Expand(Window{
		Start: Date{2026, time.January, 1}, End: Date{2026, time.December, 31},
		DayStart: TimeOfDay{0, 0}, DayEnd: TimeOfDay{24, 0},
		SlotMinutes: 5, Loc: ny,
	})
	if !errors.Is(err, ErrTooManySlots) {
		t.Fatalf("err = %v, want ErrTooManySlots", err)
	}
}

// TestSlotSizeNotDividingTheBand: 50-minute slots in a 9-to-17 band simply stop
// when the next one would overhang, leaving a remainder.
func TestSlotSizeNotDividingTheBand(t *testing.T) {
	exp := mustExpand(t, Window{
		Start: Date{2026, time.June, 1}, End: Date{2026, time.June, 1},
		DayStart: TimeOfDay{9, 0}, DayEnd: TimeOfDay{17, 0},
		SlotMinutes: 50, Loc: time.UTC,
	})

	if n := len(exp.Starts); n != 9 {
		t.Fatalf("got %d slots, want 9", n)
	}
	last := exp.Starts[len(exp.Starts)-1].UTC()
	if last.Hour() != 15 || last.Minute() != 40 {
		t.Fatalf("last slot %02d:%02d, want 15:40", last.Hour(), last.Minute())
	}
}

// --- helpers -----------------------------------------------------------------

func sortedAscending(ts []time.Time) bool {
	for i := 1; i < len(ts); i++ {
		if !ts[i].After(ts[i-1]) {
			return false
		}
	}
	return true
}
