// Package icsparse turns an iCalendar feed into busy intervals.
//
// It reads only what free/busy needs: when someone is committed, and never what
// for. SUMMARY, DESCRIPTION, LOCATION and ATTENDEE are not extracted, not
// returned and not logged. That is a stated privacy commitment, so it is
// enforced here by simply never putting those values anywhere they could
// escape, rather than by remembering not to print them.
//
// The input is a file from a stranger, so every function has to survive
// nonsense: truncated files, unknown properties, impossible dates, timezones
// that do not exist, and an END that never arrives.
//
// The package is pure. The expansion window is a parameter, not a clock read.
package icsparse

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maazin/Overlap/api/internal/tz"
)

// Interval is a half-open span during which someone is committed.
type Interval struct {
	Start time.Time
	End   time.Time
}

func (i Interval) valid() bool { return i.End.After(i.Start) }

// Overlaps reports whether two intervals share any instant. Half-open, so an
// event ending exactly when another starts is not an overlap.
func (i Interval) Overlaps(o Interval) bool {
	return i.Start.Before(o.End) && o.Start.Before(i.End)
}

// maxOccurrences bounds how far a recurrence rule is expanded, in case a feed
// carries a rule with no COUNT and no UNTIL. The window bound does most of the
// work; this stops a pathological rule spinning regardless.
const maxOccurrences = 2000

// Parse extracts busy intervals overlapping [from, to).
//
// Recurring events are expanded within the window. Anything the parser cannot
// make sense of is skipped rather than failing the whole feed: one malformed
// event in a year of calendar should cost that event, not the import.
func Parse(body string, from, to time.Time) []Interval {
	var out []Interval

	for _, block := range vevents(unfold(body)) {
		out = append(out, block.intervals(from, to)...)
	}
	return Merge(out)
}

// Merge sorts intervals and coalesces every overlap or abutment.
//
// Calendars are full of double-booked and back-to-back entries, and without
// this a single hour could be counted several times over. Abutting spans are
// joined too: 9-10 and 10-11 is one commitment from 9 to 11 as far as finding a
// free slot is concerned.
func Merge(in []Interval) []Interval {
	if len(in) == 0 {
		return nil
	}

	sort.Slice(in, func(a, b int) bool {
		if in[a].Start.Equal(in[b].Start) {
			return in[a].End.Before(in[b].End)
		}
		return in[a].Start.Before(in[b].Start)
	})

	out := []Interval{in[0]}
	for _, cur := range in[1:] {
		last := &out[len(out)-1]
		if cur.Start.After(last.End) {
			out = append(out, cur)
			continue
		}
		if cur.End.After(last.End) {
			last.End = cur.End
		}
	}
	return out
}

// --- lexing -------------------------------------------------------------------

// unfold reverses RFC 5545 line folding and normalises line endings.
//
// A continuation line begins with a space or tab and belongs to the line
// before it. Folding can land mid-word, so the join carries no separator.
func unfold(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && len(out) > 0 {
			out[len(out)-1] += line[1:]
			continue
		}
		out = append(out, line)
	}
	return out
}

// property is one content line: a name, its parameters and its value.
type property struct {
	name   string
	params map[string]string
	value  string
}

func parseProperty(line string) (property, bool) {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return property{}, false
	}

	head, value := line[:colon], line[colon+1:]
	parts := strings.Split(head, ";")

	p := property{
		name:   strings.ToUpper(strings.TrimSpace(parts[0])),
		params: make(map[string]string, len(parts)-1),
		value:  value,
	}
	if p.name == "" {
		return property{}, false
	}

	for _, raw := range parts[1:] {
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(raw[:eq]))
		val := strings.Trim(strings.TrimSpace(raw[eq+1:]), `"`)
		p.params[key] = val
	}
	return p, true
}

// vevent is the subset of an event this package cares about.
type vevent struct {
	props []property
}

// vevents pulls out every VEVENT block.
//
// A BEGIN with no matching END simply yields nothing at the end of the file
// rather than being treated as an error, which is what makes a truncated
// download degrade instead of failing.
func vevents(lines []string) []vevent {
	var out []vevent
	var cur *vevent

	for _, line := range lines {
		p, ok := parseProperty(line)
		if !ok {
			continue
		}

		switch {
		case p.name == "BEGIN" && strings.EqualFold(p.value, "VEVENT"):
			cur = &vevent{}
		case p.name == "END" && strings.EqualFold(p.value, "VEVENT"):
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
		case cur != nil:
			cur.props = append(cur.props, p)
		}
	}
	return out
}

func (e vevent) first(name string) (property, bool) {
	for _, p := range e.props {
		if p.name == name {
			return p, true
		}
	}
	return property{}, false
}

func (e vevent) all(name string) []property {
	var out []property
	for _, p := range e.props {
		if p.name == name {
			out = append(out, p)
		}
	}
	return out
}

// --- interpretation -----------------------------------------------------------

// intervals turns one VEVENT into the busy spans it contributes.
func (e vevent) intervals(from, to time.Time) []Interval {
	// A cancelled event is not a commitment.
	if p, ok := e.first("STATUS"); ok && strings.EqualFold(strings.TrimSpace(p.value), "CANCELLED") {
		return nil
	}
	// TRANSPARENT is the calendar's own way of saying "this does not make me
	// busy" -- a birthday, a reminder, a tentative hold someone marked free.
	if p, ok := e.first("TRANSP"); ok && strings.EqualFold(strings.TrimSpace(p.value), "TRANSPARENT") {
		return nil
	}

	dtstart, ok := e.first("DTSTART")
	if !ok {
		return nil
	}
	start, allDay, ok := parseTime(dtstart)
	if !ok {
		return nil
	}

	dur, ok := e.duration(start, allDay)
	if !ok {
		return nil
	}

	base := Interval{Start: start, End: start.Add(dur)}
	if !base.valid() {
		return nil
	}

	rrule, hasRule := e.first("RRULE")
	if !hasRule {
		if base.Overlaps(Interval{Start: from, End: to}) {
			return []Interval{base}
		}
		return nil
	}

	return expand(base, parseRule(rrule.value), e.exdates(), from, to)
}

// duration works out how long an event lasts, from DTEND or DURATION.
//
// An all-day event with neither is one whole day; a timed event with neither is
// a point in time and contributes nothing, which is what the zero return means.
func (e vevent) duration(start time.Time, allDay bool) (time.Duration, bool) {
	if p, ok := e.first("DTEND"); ok {
		if end, _, ok := parseTime(p); ok && end.After(start) {
			return end.Sub(start), true
		}
	}
	if p, ok := e.first("DURATION"); ok {
		if d, ok := parseDuration(p.value); ok && d > 0 {
			return d, true
		}
	}
	if allDay {
		// A DATE-valued DTSTART with no end covers the whole day. Adding 24
		// hours rather than one calendar day is deliberate: the value is
		// already an instant, and on a DST boundary a calendar day would be
		// 23 or 25 hours of *local* time, which is not what "all day busy"
		// needs to mean for slot elimination.
		return 24 * time.Hour, true
	}
	return 0, false
}

// exdates collects excluded occurrences, keyed by instant.
func (e vevent) exdates() map[int64]bool {
	out := make(map[int64]bool)
	for _, p := range e.all("EXDATE") {
		for _, v := range strings.Split(p.value, ",") {
			if t, _, ok := parseTime(property{params: p.params, value: v}); ok {
				out[t.UnixNano()] = true
			}
		}
	}
	return out
}

// --- times --------------------------------------------------------------------

// parseTime reads a DATE-TIME or DATE value, honouring TZID.
//
// The three forms are UTC (trailing Z), a zoned local time (TZID parameter) and
// a floating local time (neither). Floating times are read as UTC: without a
// zone there is nothing better to do, and guessing the server's zone would make
// the result depend on where the process happens to run.
func parseTime(p property) (t time.Time, allDay bool, ok bool) {
	v := strings.TrimSpace(p.value)
	if v == "" {
		return time.Time{}, false, false
	}

	loc := time.UTC
	if tzid := p.params["TZID"]; tzid != "" {
		// tz.Load rather than time.LoadLocation directly: it memoises the
		// zoneinfo lookup, and a feed carries the same TZID on every one of
		// its events, so an unrecurring calendar with thousands of entries
		// would otherwise re-parse the same zone file thousands of times.
		if l, err := tz.Load(tzid); err == nil {
			loc = l
		}
		// An unknown TZID falls back to UTC rather than dropping the event.
		// Being an hour or two out on a busy block is better than silently
		// treating someone as free.
	}

	if strings.EqualFold(p.params["VALUE"], "DATE") || len(v) == 8 {
		d, err := time.ParseInLocation("20060102", v, loc)
		if err != nil {
			return time.Time{}, false, false
		}
		return d, true, true
	}

	if strings.HasSuffix(v, "Z") {
		d, err := time.ParseInLocation("20060102T150405Z", v, time.UTC)
		if err != nil {
			return time.Time{}, false, false
		}
		return d, false, true
	}

	d, err := time.ParseInLocation("20060102T150405", v, loc)
	if err != nil {
		return time.Time{}, false, false
	}
	return d, false, true
}

// parseDuration reads an RFC 5545 duration such as PT1H30M or P2D.
func parseDuration(v string) (time.Duration, bool) {
	v = strings.TrimSpace(strings.ToUpper(v))
	neg := strings.HasPrefix(v, "-")
	v = strings.TrimLeft(v, "+-")

	if !strings.HasPrefix(v, "P") {
		return 0, false
	}
	v = v[1:]

	var total time.Duration
	var num strings.Builder
	inTime := false
	any := false

	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			num.WriteRune(r)
		case r == 'T':
			inTime = true
		default:
			n, err := strconv.Atoi(num.String())
			num.Reset()
			if err != nil {
				return 0, false
			}
			unit, ok := durationUnit(r, inTime)
			if !ok {
				return 0, false
			}
			total += time.Duration(n) * unit
			any = true
		}
	}
	if !any {
		return 0, false
	}
	if neg {
		total = -total
	}
	return total, true
}

func durationUnit(r rune, inTime bool) (time.Duration, bool) {
	switch r {
	case 'W':
		return 7 * 24 * time.Hour, true
	case 'D':
		return 24 * time.Hour, true
	case 'H':
		return time.Hour, true
	case 'S':
		return time.Second, true
	case 'M':
		// M is months before the T and minutes after it. Months are not a
		// fixed length, and a duration is; a feed using them for an event
		// length is beyond what free/busy needs to guess at.
		if inTime {
			return time.Minute, true
		}
		return 0, false
	}
	return 0, false
}
