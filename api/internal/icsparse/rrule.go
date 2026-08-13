package icsparse

import (
	"strconv"
	"strings"
	"time"
)

// rule is the subset of RRULE that matters for free/busy.
//
// Recurring events are most of what fills a real calendar -- the weekly
// standup, the daily check-in -- so a parser that ignored RRULE would report
// almost everyone as free almost always. That failure would be silent, which is
// the worst kind for this product.
type rule struct {
	freq     string // DAILY, WEEKLY, MONTHLY, YEARLY
	interval int
	count    int       // 0 when absent
	until    time.Time // zero when absent
	byDay    []time.Weekday
}

func parseRule(v string) rule {
	r := rule{interval: 1}

	for _, part := range strings.Split(v, ";") {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(part[:eq]))
		val := strings.TrimSpace(part[eq+1:])

		switch key {
		case "FREQ":
			r.freq = strings.ToUpper(val)
		case "INTERVAL":
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				r.interval = n
			}
		case "COUNT":
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				r.count = n
			}
		case "UNTIL":
			if t, _, ok := parseTime(property{value: val}); ok {
				r.until = t
			}
		case "BYDAY":
			for _, d := range strings.Split(val, ",") {
				if wd, ok := parseWeekday(d); ok {
					r.byDay = append(r.byDay, wd)
				}
			}
		}
	}
	return r
}

// parseWeekday reads MO/TU/... and tolerates the ordinal prefix in forms like
// "2TH" (the second Thursday) by ignoring it. Treating that as a plain Thursday
// over-reports busy time rather than under-reporting it, which is the safe
// direction: the cost is a slot wrongly avoided, not a meeting double-booked.
func parseWeekday(s string) (time.Weekday, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.TrimLeft(s, "+-0123456789")

	switch s {
	case "SU":
		return time.Sunday, true
	case "MO":
		return time.Monday, true
	case "TU":
		return time.Tuesday, true
	case "WE":
		return time.Wednesday, true
	case "TH":
		return time.Thursday, true
	case "FR":
		return time.Friday, true
	case "SA":
		return time.Saturday, true
	}
	return 0, false
}

// expand walks a recurrence and returns the occurrences overlapping the window.
//
// It steps through occurrences rather than computing them, because the step has
// to happen in the event's own timezone: a weekly 9am meeting stays at 9am
// local across a DST transition, so adding seven fixed 24-hour days would drift
// it by an hour for half the year. AddDate works on the calendar, which is what
// the rule actually means.
func expand(base Interval, r rule, exdates map[int64]bool, from, to time.Time) []Interval {
	if r.freq == "" {
		if base.Overlaps(Interval{Start: from, End: to}) {
			return []Interval{base}
		}
		return nil
	}

	dur := base.End.Sub(base.Start)
	loc := base.Start.Location()

	var out []Interval
	emitted := 0

	emit := func(start time.Time) bool {
		if exdates[start.UnixNano()] {
			return true // excluded, but it still counts toward COUNT
		}
		iv := Interval{Start: start, End: start.Add(dur)}
		if iv.Overlaps(Interval{Start: from, End: to}) {
			out = append(out, iv)
		}
		return true
	}

	cur := base.Start
	for step := 0; step < maxOccurrences; step++ {
		if !r.until.IsZero() && cur.After(r.until) {
			break
		}
		// Everything from here on starts after the window, so there is nothing
		// left to find.
		if cur.After(to) {
			break
		}

		if len(r.byDay) > 0 && (r.freq == "WEEKLY" || r.freq == "DAILY") {
			// Within the week containing cur, emit each named weekday.
			weekStart := cur.AddDate(0, 0, -int(cur.Weekday()))
			for _, wd := range r.byDay {
				d := weekStart.AddDate(0, 0, int(wd))
				occ := time.Date(d.Year(), d.Month(), d.Day(),
					base.Start.Hour(), base.Start.Minute(), base.Start.Second(), 0, loc)

				if occ.Before(base.Start) {
					continue // before the series began
				}
				if !r.until.IsZero() && occ.After(r.until) {
					continue
				}
				if r.count > 0 && emitted >= r.count {
					break
				}
				emitted++
				emit(occ)
			}
		} else {
			if r.count > 0 && emitted >= r.count {
				break
			}
			emitted++
			emit(cur)
		}

		if r.count > 0 && emitted >= r.count {
			break
		}

		next, ok := advance(cur, r)
		if !ok || !next.After(cur) {
			break
		}
		cur = next
	}

	return out
}

// advance moves to the next period. Calendar arithmetic, not fixed durations,
// so months and DST behave the way a person expects.
func advance(t time.Time, r rule) (time.Time, bool) {
	switch r.freq {
	case "DAILY":
		return t.AddDate(0, 0, r.interval), true
	case "WEEKLY":
		return t.AddDate(0, 0, 7*r.interval), true
	case "MONTHLY":
		return t.AddDate(0, r.interval, 0), true
	case "YEARLY":
		return t.AddDate(r.interval, 0, 0), true
	}
	return t, false
}
