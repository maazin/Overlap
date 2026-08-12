// Package ics renders a decided event as an iCalendar object (RFC 5545).
//
// This is written by hand rather than pulled from a library because generating
// one VEVENT is a small, exactly-specified job, and the parts that actually
// break interoperability -- CRLF terminators, octet-based line folding, text
// escaping and UTC stamps -- are easier to test directly than to verify through
// somebody else's abstraction. Phase 6 parses arbitrary third-party feeds,
// which is the opposite situation, and should reach for a library.
//
// The package is pure: the current time is a parameter.
package ics

import (
	"fmt"
	"strings"
	"time"
)

// maxOctets is the RFC 5545 content line limit, excluding the CRLF. Longer
// lines must be folded onto continuation lines beginning with a single space.
const maxOctets = 75

// Event is a single decided meeting.
type Event struct {
	// UID must be globally unique and stable for the life of the meeting.
	// Re-issuing the same UID with a later DTSTAMP is what lets a calendar
	// client update an event in place rather than create a duplicate.
	UID string

	Summary     string
	Description string
	Start       time.Time
	End         time.Time

	// URL is the event's page, so an attendee can get back to it from their
	// calendar.
	URL string
}

// Render returns the .ics body. now supplies DTSTAMP, which RFC 5545 requires
// and which clients use to decide whether an update is newer than what they
// already hold.
func Render(ev Event, now time.Time) string {
	var b strings.Builder

	write := func(name, value string) {
		writeFolded(&b, name+":"+escapeText(value))
	}
	writeRaw := func(name, value string) {
		writeFolded(&b, name+":"+value)
	}

	writeRaw("BEGIN", "VCALENDAR")
	writeRaw("VERSION", "2.0")
	// PRODID is required and is meant to identify the software, not the user.
	writeRaw("PRODID", "-//Overlap//Overlap Scheduler//EN")
	writeRaw("CALSCALE", "GREGORIAN")
	// PUBLISH rather than REQUEST: this is a file someone downloads, not an
	// invitation sent on anyone's behalf. REQUEST would make some clients treat
	// it as an invite needing an RSVP to an organizer address we do not have.
	writeRaw("METHOD", "PUBLISH")

	writeRaw("BEGIN", "VEVENT")
	writeRaw("UID", ev.UID)
	writeRaw("DTSTAMP", stamp(now))
	writeRaw("DTSTART", stamp(ev.Start))
	writeRaw("DTEND", stamp(ev.End))
	write("SUMMARY", ev.Summary)
	if ev.Description != "" {
		write("DESCRIPTION", ev.Description)
	}
	if ev.URL != "" {
		writeRaw("URL", ev.URL)
	}
	writeRaw("STATUS", "CONFIRMED")
	writeRaw("TRANSP", "OPAQUE")
	writeRaw("END", "VEVENT")

	writeRaw("END", "VCALENDAR")

	return b.String()
}

// stamp formats an instant as a UTC timestamp.
//
// Everything is emitted in UTC with the trailing Z rather than as a local time
// plus a VTIMEZONE block. Both are legal; the UTC form cannot be misread, and
// it means the file does not have to carry a transcription of the timezone
// database that could disagree with the client's own.
func stamp(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// escapeText applies the RFC 5545 TEXT escaping rules. Backslash must be
// handled first, or the escapes inserted below would themselves be escaped.
func escapeText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\r\n", `\n`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\n`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	return s
}

// writeFolded appends one content line, folding it to the octet limit and
// terminating with CRLF.
//
// The limit counts octets, not characters, and a fold may not split a multi-byte
// UTF-8 sequence. Folding on runes rather than bytes is what keeps a name with
// an accent in it from arriving as mojibake.
func writeFolded(b *strings.Builder, line string) {
	const crlf = "\r\n"

	if len(line) <= maxOctets {
		b.WriteString(line)
		b.WriteString(crlf)
		return
	}

	used := 0
	first := true
	for _, r := range line {
		n := len(string(r))
		// Continuation lines start with a space that counts toward the limit.
		limit := maxOctets
		if !first {
			limit = maxOctets - 1
		}
		if used+n > limit {
			b.WriteString(crlf)
			b.WriteString(" ")
			used = 0
			first = false
		}
		b.WriteString(string(r))
		used += n
	}
	b.WriteString(crlf)
}

// Filename returns a safe attachment filename for a decided event.
func Filename(slug string) string {
	return fmt.Sprintf("overlap-%s.ics", slug)
}
