package icsparse

import (
	"strings"
	"testing"
	"time"
)

var ny = func() *time.Location {
	l, _ := time.LoadLocation("America/New_York")
	return l
}()

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// cal wraps VEVENT bodies in a calendar, with CRLF endings the way a real feed
// arrives.
func cal(events ...string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\n")
	for _, e := range events {
		b.WriteString("BEGIN:VEVENT\r\n")
		for _, line := range strings.Split(strings.TrimSpace(e), "\n") {
			b.WriteString(strings.TrimSpace(line) + "\r\n")
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

var window = struct{ from, to time.Time }{
	from: utc(2026, time.June, 1, 0, 0),
	to:   utc(2026, time.July, 1, 0, 0),
}

func parse(t *testing.T, body string) []Interval {
	t.Helper()
	return Parse(body, window.from, window.to)
}

func TestSimpleTimedEvent(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20260610T140000Z
		DTEND:20260610T150000Z
	`))

	if len(got) != 1 {
		t.Fatalf("got %d intervals, want 1: %+v", len(got), got)
	}
	if !got[0].Start.Equal(utc(2026, time.June, 10, 14, 0)) {
		t.Errorf("start = %s", got[0].Start)
	}
	if !got[0].End.Equal(utc(2026, time.June, 10, 15, 0)) {
		t.Errorf("end = %s", got[0].End)
	}
}

// TestTZIDIsHonoured: a feed from a person in New York states local times with
// a TZID, and reading them as UTC would move every commitment by hours.
func TestTZIDIsHonoured(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART;TZID=America/New_York:20260610T090000
		DTEND;TZID=America/New_York:20260610T100000
	`))

	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	// 09:00 EDT is 13:00 UTC.
	if !got[0].Start.Equal(utc(2026, time.June, 10, 13, 0)) {
		t.Fatalf("start = %s, want 13:00Z", got[0].Start.UTC())
	}
}

func TestUnknownTZIDFallsBackRatherThanDropping(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART;TZID=Mars/Olympus:20260610T090000
		DTEND;TZID=Mars/Olympus:20260610T100000
	`))

	// Being an hour out is better than silently reporting somebody as free.
	if len(got) != 1 {
		t.Fatalf("an unresolvable zone must not drop the event: %+v", got)
	}
}

// TestAllDayEvent covers the DATE-valued form, which has no time at all.
func TestAllDayEvent(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART;VALUE=DATE:20260610
	`))

	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if d := got[0].End.Sub(got[0].Start); d != 24*time.Hour {
		t.Fatalf("duration = %s, want 24h", d)
	}
}

func TestDurationProperty(t *testing.T) {
	for _, tc := range []struct {
		dur  string
		want time.Duration
	}{
		{"PT1H", time.Hour},
		{"PT30M", 30 * time.Minute},
		{"PT1H30M", 90 * time.Minute},
		{"P1D", 24 * time.Hour},
		{"P1W", 7 * 24 * time.Hour},
	} {
		t.Run(tc.dur, func(t *testing.T) {
			got := parse(t, cal(`
				UID:1
				DTSTART:20260610T140000Z
				DURATION:`+tc.dur+`
			`))
			if len(got) != 1 {
				t.Fatalf("got %+v", got)
			}
			if d := got[0].End.Sub(got[0].Start); d != tc.want {
				t.Fatalf("duration = %s, want %s", d, tc.want)
			}
		})
	}
}

// TestCancelledAndTransparentAreNotBusy: a declined meeting and a birthday
// reminder are both on the calendar and neither makes anyone unavailable.
func TestCancelledAndTransparentAreNotBusy(t *testing.T) {
	got := parse(t, cal(
		`UID:1
		 DTSTART:20260610T140000Z
		 DTEND:20260610T150000Z
		 STATUS:CANCELLED`,
		`UID:2
		 DTSTART:20260610T160000Z
		 DTEND:20260610T170000Z
		 TRANSP:TRANSPARENT`,
	))

	if len(got) != 0 {
		t.Fatalf("neither event is a commitment: %+v", got)
	}
}

// TestOverlappingBlocksAreMerged is the phase 6 case from the build plan.
// Double-booked calendars are normal, and counting the same hour twice would
// distort everything downstream.
func TestOverlappingBlocksAreMerged(t *testing.T) {
	got := parse(t, cal(
		`UID:1
		 DTSTART:20260610T140000Z
		 DTEND:20260610T160000Z`,
		`UID:2
		 DTSTART:20260610T150000Z
		 DTEND:20260610T170000Z`,
		// Abutting: 17:00-18:00 continues straight on from the above.
		`UID:3
		 DTSTART:20260610T170000Z
		 DTEND:20260610T180000Z`,
	))

	if len(got) != 1 {
		t.Fatalf("got %d intervals, want them merged into 1: %+v", len(got), got)
	}
	if !got[0].Start.Equal(utc(2026, time.June, 10, 14, 0)) ||
		!got[0].End.Equal(utc(2026, time.June, 10, 18, 0)) {
		t.Fatalf("merged span = %s..%s, want 14:00..18:00", got[0].Start, got[0].End)
	}
}

func TestDisjointBlocksAreNotMerged(t *testing.T) {
	got := parse(t, cal(
		`UID:1
		 DTSTART:20260610T140000Z
		 DTEND:20260610T150000Z`,
		`UID:2
		 DTSTART:20260610T160000Z
		 DTEND:20260610T170000Z`,
	))

	if len(got) != 2 {
		t.Fatalf("got %d, want 2 separate intervals: %+v", len(got), got)
	}
}

// TestMalformedInputIsSurvived is the property that matters most for a feed
// fetched from a stranger: nonsense costs the event, not the import.
func TestMalformedInputIsSurvived(t *testing.T) {
	for _, name := range []string{"empty", "garbage", "truncated", "no colon", "bad date"} {
		t.Run(name, func(t *testing.T) {
			var body string
			switch name {
			case "empty":
				body = ""
			case "garbage":
				body = "this is not a calendar at all\n\x00\xff binary"
			case "truncated":
				body = "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nDTSTART:20260610T140000Z\r\n"
			case "no colon":
				body = "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nDTSTART 20260610\r\nEND:VEVENT\r\n"
			case "bad date":
				body = cal(`UID:1
					DTSTART:not-a-date
					DTEND:also-not`)
			}

			// The requirement is simply that this returns rather than panics.
			got := Parse(body, window.from, window.to)
			if len(got) != 0 {
				t.Fatalf("malformed input produced intervals: %+v", got)
			}
		})
	}
}

// TestOneBadEventDoesNotKillTheFeed: a year of calendar should not be lost to a
// single unparseable entry.
func TestOneBadEventDoesNotKillTheFeed(t *testing.T) {
	got := parse(t, cal(
		`UID:1
		 DTSTART:garbage
		 DTEND:worse`,
		`UID:2
		 DTSTART:20260610T140000Z
		 DTEND:20260610T150000Z`,
	))

	if len(got) != 1 {
		t.Fatalf("the good event should survive: %+v", got)
	}
}

// TestFoldedLinesAreRejoined: real feeds fold long lines, and a TZID split
// across a fold would otherwise be unreadable.
func TestFoldedLinesAreRejoined(t *testing.T) {
	body := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\n" +
		"DTSTART;TZID=America/New_Y\r\n ork:20260610T090000\r\n" +
		"DTEND;TZID=America/New_York:20260610T100000\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"

	got := Parse(body, window.from, window.to)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if !got[0].Start.Equal(utc(2026, time.June, 10, 13, 0)) {
		t.Fatalf("start = %s, want the unfolded TZID to have been used", got[0].Start.UTC())
	}
}

func TestEventsOutsideTheWindowAreIgnored(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20250101T140000Z
		DTEND:20250101T150000Z
	`))

	if len(got) != 0 {
		t.Fatalf("an event a year outside the window should not appear: %+v", got)
	}
}

// --- recurrence ---------------------------------------------------------------

// TestWeeklyRecurrenceIsExpanded is why RRULE cannot be skipped: the weekly
// standup is most of what makes someone busy, and ignoring it would report them
// as free every week but the first.
func TestWeeklyRecurrenceIsExpanded(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20260603T140000Z
		DTEND:20260603T150000Z
		RRULE:FREQ=WEEKLY
	`))

	// June 3, 10, 17, 24 are the Wednesdays inside the window.
	if len(got) != 4 {
		t.Fatalf("got %d occurrences, want 4: %+v", len(got), got)
	}
	for i, iv := range got {
		if iv.Start.Weekday() != time.Wednesday {
			t.Errorf("[%d] %s is a %s", i, iv.Start, iv.Start.Weekday())
		}
	}
}

func TestCountLimitsARecurrence(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20260603T140000Z
		DTEND:20260603T150000Z
		RRULE:FREQ=WEEKLY;COUNT=2
	`))

	if len(got) != 2 {
		t.Fatalf("got %d, want COUNT=2 respected: %+v", len(got), got)
	}
}

func TestUntilLimitsARecurrence(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20260603T140000Z
		DTEND:20260603T150000Z
		RRULE:FREQ=WEEKLY;UNTIL=20260611T000000Z
	`))

	if len(got) != 2 {
		t.Fatalf("got %d, want the 3rd and later cut off by UNTIL: %+v", len(got), got)
	}
}

func TestIntervalSkipsPeriods(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20260603T140000Z
		DTEND:20260603T150000Z
		RRULE:FREQ=WEEKLY;INTERVAL=2
	`))

	// June 3, 17 -- fortnightly.
	if len(got) != 2 {
		t.Fatalf("got %d, want fortnightly: %+v", len(got), got)
	}
}

func TestDailyRecurrence(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20260601T140000Z
		DTEND:20260601T143000Z
		RRULE:FREQ=DAILY;COUNT=5
	`))

	if len(got) != 5 {
		t.Fatalf("got %d, want 5 daily occurrences: %+v", len(got), got)
	}
}

// TestByDayEmitsEachNamedWeekday covers the standard "Mon/Wed/Fri" shape.
func TestByDayEmitsEachNamedWeekday(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20260601T140000Z
		DTEND:20260601T150000Z
		RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR;COUNT=6
	`))

	if len(got) != 6 {
		t.Fatalf("got %d, want 6: %+v", len(got), got)
	}
	for _, iv := range got {
		switch iv.Start.Weekday() {
		case time.Monday, time.Wednesday, time.Friday:
		default:
			t.Errorf("%s is a %s, which is not in BYDAY", iv.Start, iv.Start.Weekday())
		}
	}
}

// TestExdateRemovesAnOccurrence: a cancelled instance of a recurring meeting is
// free time, and missing it would hide a slot that is genuinely available.
func TestExdateRemovesAnOccurrence(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20260603T140000Z
		DTEND:20260603T150000Z
		RRULE:FREQ=WEEKLY
		EXDATE:20260610T140000Z
	`))

	if len(got) != 3 {
		t.Fatalf("got %d, want the 10th excluded: %+v", len(got), got)
	}
	for _, iv := range got {
		if iv.Start.Equal(utc(2026, time.June, 10, 14, 0)) {
			t.Fatal("the excluded occurrence is still present")
		}
	}
}

// TestRecurrenceKeepsLocalTimeAcrossDST is the timezone trap in recurrence.
// A weekly 09:00 meeting in New York stays at 09:00 through the November
// transition, which means the UTC instant shifts by an hour. Stepping by a
// fixed 168 hours instead of by calendar weeks would drift it.
func TestRecurrenceKeepsLocalTimeAcrossDST(t *testing.T) {
	body := cal(`
		UID:1
		DTSTART;TZID=America/New_York:20261028T090000
		DTEND;TZID=America/New_York:20261028T100000
		RRULE:FREQ=WEEKLY;COUNT=3
	`)

	// 2026-11-01 is the US fall-back, so the second and third occurrences are
	// on the far side of it.
	got := Parse(body, utc(2026, time.October, 1, 0, 0), utc(2026, time.December, 1, 0, 0))
	if len(got) != 3 {
		t.Fatalf("got %d occurrences: %+v", len(got), got)
	}

	for i, iv := range got {
		local := iv.Start.In(ny)
		if local.Hour() != 9 {
			t.Errorf("[%d] %s is %02d:00 local, want 09:00", i, local, local.Hour())
		}
	}

	// And the UTC instants must differ across the transition: -4 before, -5
	// after. Identical offsets would mean the local time had drifted instead.
	if got[0].Start.UTC().Hour() != 13 {
		t.Errorf("before the transition 09:00 EDT is 13:00Z, got %s", got[0].Start.UTC())
	}
	if got[2].Start.UTC().Hour() != 14 {
		t.Errorf("after the transition 09:00 EST is 14:00Z, got %s", got[2].Start.UTC())
	}
}

// TestRunawayRuleIsBounded guards against a feed with no COUNT and no UNTIL.
// TestOldUncountedDailySeriesStillReachesTheWindow is the bug an uncounted,
// unbounded RRULE ran into: the stepping loop is capped at maxOccurrences so
// it cannot spin forever, but that cap is a step count, not a date. A daily
// standup set up in 2010 is over 5,000 days before a 2026 window -- more than
// maxOccurrences steps away -- so walking one day at a time from the series'
// own start burns the whole budget in 2010-2015 and never reaches the window,
// silently reporting the person free during a meeting they attend every day.
func TestOldUncountedDailySeriesStillReachesTheWindow(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20100601T140000Z
		DTEND:20100601T150000Z
		RRULE:FREQ=DAILY
	`))

	// June 1-30 2026 are all in the window; the daily series must produce one
	// occurrence per day, same as if it had started inside the window.
	if len(got) != 30 {
		t.Fatalf("got %d occurrences, want 30 -- one per day in June; the series lost the window entirely: %+v",
			len(got), got)
	}
}

// TestOldUncountedWeeklySeriesStillReachesTheWindow is the same failure mode
// for a weekly series, which is the more common real-world shape (a
// recurring 1:1 or team meeting set up years ago).
func TestOldUncountedWeeklySeriesStillReachesTheWindow(t *testing.T) {
	// 2000 weekly steps is about 38 years; 1985 to 2026 is over 40, past that
	// budget, so this is not just a correctness check but a genuine regression
	// test for the fast-forward -- it fails without it.
	got := parse(t, cal(`
		UID:1
		DTSTART:19850605T140000Z
		DTEND:19850605T150000Z
		RRULE:FREQ=WEEKLY
	`))

	// June 3, 10, 17, 24 2026 are the Wednesdays in the window, matching the
	// series' original weekday.
	if len(got) != 4 {
		t.Fatalf("got %d occurrences, want 4 Wednesdays; a 41-year-old weekly series lost the window: %+v",
			len(got), got)
	}
}

// TestFastForwardDoesNotSkipAnExdateNearTheWindow checks the safety margin:
// jumping most of the way there must not overshoot past an excluded
// occurrence close to the window, which the unchanged correction loop is
// relied on to still catch.
func TestFastForwardDoesNotSkipAnExdateNearTheWindow(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20100601T140000Z
		DTEND:20100601T150000Z
		RRULE:FREQ=DAILY
		EXDATE:20260601T140000Z
	`))

	for _, iv := range got {
		if iv.Start.Equal(utc(2026, time.June, 1, 14, 0)) {
			t.Fatal("June 1 was excluded and must not appear, even after fast-forwarding from 2010")
		}
	}
	if len(got) != 29 {
		t.Fatalf("got %d occurrences, want 29 (30 days minus the 1 excluded): %+v", len(got), got)
	}
}

// TestFastForwardRespectsUntilBeforeTheWindow: a series whose UNTIL falls
// before the window ended before the window opened, and fast-forwarding past
// that boundary must still report nothing rather than resurrecting occurrences
// the rule itself terminated.
func TestFastForwardRespectsUntilBeforeTheWindow(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20100601T140000Z
		DTEND:20100601T150000Z
		RRULE:FREQ=DAILY;UNTIL=20120101T000000Z
	`))

	if len(got) != 0 {
		t.Fatalf("the series ended in 2012, years before the window; got %+v", got)
	}
}

// TestFastForwardOnlyAppliesWithoutCount documents why COUNT needs no special
// handling: a bounded series always exits within `count` steps regardless of
// how far cur has to travel, so it was never subject to the maxOccurrences
// trap and the fast-forward path is skipped for it entirely. A COUNT small
// enough to finish long before a distant window must correctly find nothing.
func TestFastForwardOnlyAppliesWithoutCount(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20100601T140000Z
		DTEND:20100601T150000Z
		RRULE:FREQ=DAILY;COUNT=5
	`))

	if len(got) != 0 {
		t.Fatalf("5 occurrences starting in 2010 all finished in 2010, nowhere near the window: %+v", got)
	}
}

func TestRunawayRuleIsBounded(t *testing.T) {
	got := parse(t, cal(`
		UID:1
		DTSTART:20260601T140000Z
		DTEND:20260601T141000Z
		RRULE:FREQ=DAILY
	`))

	// Bounded by the one-month window, not by the rule.
	if len(got) == 0 || len(got) > 31 {
		t.Fatalf("got %d occurrences, want them bounded by the window", len(got))
	}
}

func TestMergeIsStable(t *testing.T) {
	if got := Merge(nil); got != nil {
		t.Fatalf("Merge(nil) = %+v", got)
	}
	in := []Interval{
		{utc(2026, time.June, 1, 12, 0), utc(2026, time.June, 1, 13, 0)},
		{utc(2026, time.June, 1, 9, 0), utc(2026, time.June, 1, 10, 0)},
	}
	got := Merge(in)
	if len(got) != 2 || !got[0].Start.Before(got[1].Start) {
		t.Fatalf("Merge did not sort: %+v", got)
	}
}
