// Package slots expands an organizer's local scheduling window into absolute
// instants.
//
// This is the other half of the product that has to be exactly right, and like
// the solver it is pure: no database, no clock reads, no globals. Time enters
// only through the values passed in.
//
// The whole difficulty is that a local wall clock is not a moment. On the day a
// zone springs forward some wall clocks never happen, and on the day it falls
// back some happen twice. Go's time.Date silently resolves both rather than
// reporting them, so a generator that trusts it produces wrong instants without
// any error to notice. Expand detects both cases and handles them deliberately.
package slots

import (
	"errors"
	"fmt"
	"time"
)

// Date is a calendar date with no zone and no time of day, matching a Postgres
// `date` column. It exists because time.Time cannot represent "a date" without
// inviting the reader to assume a zone that is not there.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

// next returns the following calendar date. It goes through time.Date in UTC
// rather than adding 24 hours to a zoned value, because on a transition day
// 24 hours is not one day and the date would skip or repeat.
func (d Date) next() Date {
	t := time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	return Date{t.Year(), t.Month(), t.Day()}
}

func (d Date) after(o Date) bool {
	if d.Year != o.Year {
		return d.Year > o.Year
	}
	if d.Month != o.Month {
		return d.Month > o.Month
	}
	return d.Day > o.Day
}

// TimeOfDay is a wall-clock time of day, matching a Postgres `time` column.
// Minutes is the offset from midnight, so 24:00 is representable as an end
// bound the way Postgres allows.
type TimeOfDay struct {
	Hour   int
	Minute int
}

func (t TimeOfDay) minutes() int   { return t.Hour*60 + t.Minute }
func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute) }

// Window is the request: a run of local dates, a daily band, and a slot size,
// all expressed in Loc.
type Window struct {
	Start, End  Date
	DayStart    TimeOfDay
	DayEnd      TimeOfDay
	SlotMinutes int
	Loc         *time.Location
}

// Kind classifies how a wall clock mapped onto the timeline.
type Kind uint8

const (
	// KindNormal: the wall clock happened exactly once.
	KindNormal Kind = iota
	// KindNonexistent: the wall clock was skipped by a forward transition.
	KindNonexistent
	// KindAmbiguous: the wall clock happened twice under different offsets.
	KindAmbiguous
)

// Anomaly records a wall clock that did not map cleanly, so callers can explain
// themselves instead of silently producing a surprising schedule.
type Anomaly struct {
	Date  Date
	Local TimeOfDay
	Kind  Kind
	// Chosen is the instant used. Zero when the slot was dropped.
	Chosen time.Time
}

// Expansion is the result of expanding a window.
type Expansion struct {
	// Starts holds every slot's absolute instant, ascending, deduplicated.
	Starts []time.Time
	// Skipped lists wall clocks dropped because they never occurred.
	Skipped []Anomaly
	// Ambiguous lists wall clocks that occurred twice and were resolved to the
	// first occurrence.
	Ambiguous []Anomaly
}

var (
	ErrNoLocation   = errors.New("slots: window has no location")
	ErrBadSlotSize  = errors.New("slots: slot minutes must be positive")
	ErrBadDayBand   = errors.New("slots: day end must be after day start")
	ErrBadDateRange = errors.New("slots: window end precedes window start")
	ErrTooManySlots = errors.New("slots: window would generate too many slots")
)

// MaxSlots bounds what one event may expand to. A window is organizer-supplied
// input, and a year of five-minute slots is a memory-exhaustion vector rather
// than a scheduling request.
const MaxSlots = 5000

// Validate reports whether the window is usable. Expand calls it, but handlers
// can call it first to reject a request before doing anything else.
func (w Window) Validate() error {
	switch {
	case w.Loc == nil:
		return ErrNoLocation
	case w.SlotMinutes <= 0:
		return ErrBadSlotSize
	case w.DayEnd.minutes() <= w.DayStart.minutes():
		return ErrBadDayBand
	case w.Start.after(w.End):
		return ErrBadDateRange
	}
	return nil
}

// Expand turns the window into absolute instants.
//
// Slots are laid out on the local wall clock, one band per calendar date, which
// is what an organizer means by "9 to 5 all week": every day starts at 9am
// locally regardless of whether the offset moved in between. The consequence is
// that consecutive days are not always exactly 24 hours apart in absolute
// terms, and that is correct rather than a bug.
//
// Only slots that fit entirely inside the daily band are emitted. Slot starts
// sit on a grid stepping from DayStart, so a 45 minute slot in a 9:00-17:00
// band runs 9:00, 9:45 ... 15:45 and stops: the next start, 16:30, would
// overhang the band. A remainder at the end of the day is expected.
func Expand(w Window) (Expansion, error) {
	if err := w.Validate(); err != nil {
		return Expansion{}, err
	}

	var (
		exp  Expansion
		seen = make(map[int64]struct{})
	)

	last := w.DayEnd.minutes() - w.SlotMinutes
	for d := w.Start; !d.after(w.End); d = d.next() {
		for m := w.DayStart.minutes(); m <= last; m += w.SlotMinutes {
			local := TimeOfDay{Hour: m / 60, Minute: m % 60}

			t, kind := resolve(d, local, w.Loc)
			switch kind {
			case KindNonexistent:
				// The clock never read this. Dropping it is the only honest
				// option: there is no instant to meet at. Emitting Go's
				// normalized value would also collide with a real slot, since
				// it normalizes backward into the hour before the transition.
				exp.Skipped = append(exp.Skipped, Anomaly{d, local, kind, time.Time{}})
				continue
			case KindAmbiguous:
				exp.Ambiguous = append(exp.Ambiguous, Anomaly{d, local, kind, t})
			}

			// Distinct wall clocks can still land on one instant at a
			// transition, and a duplicate slot would be counted twice by the
			// solver.
			if _, dup := seen[t.UnixNano()]; dup {
				continue
			}
			seen[t.UnixNano()] = struct{}{}
			exp.Starts = append(exp.Starts, t)

			if len(exp.Starts) > MaxSlots {
				return Expansion{}, ErrTooManySlots
			}
		}
	}

	return exp, nil
}

// probeDeltas are the offset jumps searched for a second instant sharing a wall
// clock. Every transition in the IANA database moves the offset by 30 minutes,
// one hour or two, so probing those in both directions finds any real
// ambiguity without scanning the timeline.
var probeDeltas = [...]time.Duration{
	time.Hour, -time.Hour,
	30 * time.Minute, -30 * time.Minute,
	2 * time.Hour, -2 * time.Hour,
}

// resolve maps a local wall clock to an absolute instant and reports how it
// mapped.
//
// Detection leans on the fact that time.Date always returns *some* instant:
// reading the wall clock back off that instant and comparing it to what was
// asked for is what distinguishes a normalized answer from a real one.
func resolve(d Date, tod TimeOfDay, loc *time.Location) (time.Time, Kind) {
	t := time.Date(d.Year, d.Month, d.Day, tod.Hour, tod.Minute, 0, 0, loc)

	// Go resolves a nonexistent local time by shifting it, so the round trip
	// disagrees with the request. On US spring-forward it shifts backward:
	// 02:30 comes back as 01:30.
	if !matches(t, d, tod) {
		return t, KindNonexistent
	}

	// A second instant with the same wall clock means the clock read this twice.
	for _, delta := range probeDeltas {
		u := t.Add(delta)
		if !matches(u, d, tod) {
			continue
		}
		// Resolve to the first occurrence. The choice matters less than it
		// being deterministic, since the same event must expand identically
		// every time it is scored.
		if u.Before(t) {
			t = u
		}
		return t, KindAmbiguous
	}

	return t, KindNormal
}

// matches reports whether t, read in its own location, shows exactly the
// requested wall clock.
func matches(t time.Time, d Date, tod TimeOfDay) bool {
	y, mo, day := t.Date()
	h, mi, s := t.Clock()
	return y == d.Year && mo == d.Month && day == d.Day &&
		h == tod.Hour && mi == tod.Minute && s == 0
}
