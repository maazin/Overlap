package ics

import (
	"strings"
	"testing"
	"time"
)

func sample() (Event, time.Time) {
	ny, _ := time.LoadLocation("America/New_York")
	return Event{
			UID:         "abcd1234@overlap.app",
			Summary:     "Team sync",
			Description: "Ana, Ben, Cara",
			Start:       time.Date(2026, 11, 5, 15, 0, 0, 0, ny), // 20:00 UTC
			End:         time.Date(2026, 11, 5, 15, 45, 0, 0, ny),
			URL:         "https://overlap.app/e/abcd1234",
		},
		time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
}

func lines(s string) []string {
	// Trailing CRLF leaves an empty final element; drop it.
	out := strings.Split(s, "\r\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// TestEveryLineEndsWithCRLF is the single most common reason an otherwise valid
// file is rejected. A bare LF is not a line break in RFC 5545.
func TestEveryLineEndsWithCRLF(t *testing.T) {
	got := Render(sample())

	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatal("output contains a line feed that is not part of a CRLF pair")
	}
	if !strings.HasSuffix(got, "\r\n") {
		t.Fatal("the final line must be terminated")
	}
}

func TestStructureAndRequiredProperties(t *testing.T) {
	got := lines(Render(sample()))

	if got[0] != "BEGIN:VCALENDAR" {
		t.Fatalf("first line = %q", got[0])
	}
	if got[len(got)-1] != "END:VCALENDAR" {
		t.Fatalf("last line = %q", got[len(got)-1])
	}

	// VERSION and PRODID are mandatory on the calendar; UID and DTSTAMP are
	// mandatory on the event. Omitting any of them is what makes a client
	// silently drop the import.
	for _, want := range []string{
		"VERSION:2.0",
		"BEGIN:VEVENT",
		"UID:abcd1234@overlap.app",
		"DTSTAMP:20261101T120000Z",
		"DTSTART:20261105T200000Z",
		"DTEND:20261105T204500Z",
		"SUMMARY:Team sync",
		"END:VEVENT",
	} {
		if !slicesContain(got, want) {
			t.Errorf("missing line %q\n--- got ---\n%s", want, strings.Join(got, "\n"))
		}
	}
	if !hasPrefixIn(got, "PRODID:") {
		t.Error("PRODID is required")
	}
}

// TestTimesAreUTC pins the conversion. 15:00 in New York on 5 November is
// 20:00 UTC because the clocks have already gone back; getting this wrong ships
// a meeting an hour out for everyone who imports it.
func TestTimesAreUTC(t *testing.T) {
	got := Render(sample())

	if !strings.Contains(got, "DTSTART:20261105T200000Z") {
		t.Error("DTSTART must be the UTC instant with a Z suffix")
	}
	if strings.Contains(got, "DTSTART;TZID") {
		t.Error("no TZID form should be emitted; everything is absolute")
	}
}

func TestTextEscaping(t *testing.T) {
	ev, now := sample()
	ev.Summary = `Lunch, drinks; then "planning" \ wrap-up`
	ev.Description = "Line one\nLine two"

	got := Render(ev, now)

	if !strings.Contains(got, `Lunch\, drinks\; then "planning" \\ wrap-up`) {
		t.Errorf("commas, semicolons and backslashes must be escaped:\n%s", got)
	}
	if !strings.Contains(got, `Line one\nLine two`) {
		t.Errorf("newlines must become the two-character sequence backslash-n:\n%s", got)
	}
}

// TestFoldingRespectsOctetLimit checks both halves of the rule: no content line
// may exceed 75 octets, and every continuation must begin with a space.
func TestFoldingRespectsOctetLimit(t *testing.T) {
	ev, now := sample()
	ev.Summary = strings.Repeat("long meeting title ", 12)

	got := lines(Render(ev, now))

	sawContinuation := false
	for i, l := range got {
		if len(l) > maxOctets {
			t.Errorf("line %d is %d octets, over the %d limit: %q", i, len(l), maxOctets, l)
		}
		if strings.HasPrefix(l, " ") {
			sawContinuation = true
		}
	}
	if !sawContinuation {
		t.Fatal("a long summary should have produced a folded continuation line")
	}
}

// TestFoldingDoesNotSplitMultibyteRunes is the bug that only shows up for
// people whose names are not ASCII: folding on bytes cuts a UTF-8 sequence in
// half and the client renders a replacement character.
func TestFoldingDoesNotSplitMultibyteRunes(t *testing.T) {
	ev, now := sample()
	ev.Summary = strings.Repeat("é", 90)

	got := Render(ev, now)

	unfolded := strings.ReplaceAll(got, "\r\n ", "")
	if !strings.Contains(unfolded, strings.Repeat("é", 90)) {
		t.Fatal("unfolding did not recover the original text; a rune was split")
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatal("output contains a replacement character")
	}
}

// TestUnfoldingRecoversTheOriginal is the round trip a client performs: strip
// every CRLF-space pair and the original property value must come back.
func TestUnfoldingRecoversTheOriginal(t *testing.T) {
	ev, now := sample()
	ev.Summary = strings.Repeat("abcdefghij", 20)

	unfolded := strings.ReplaceAll(Render(ev, now), "\r\n ", "")
	if !strings.Contains(unfolded, "SUMMARY:"+ev.Summary) {
		t.Fatal("folded summary did not survive unfolding")
	}
}

func TestOptionalFieldsAreOmitted(t *testing.T) {
	ev, now := sample()
	ev.Description = ""
	ev.URL = ""

	got := Render(ev, now)
	if strings.Contains(got, "DESCRIPTION:") {
		t.Error("an empty description should not be emitted")
	}
	if strings.Contains(got, "URL:") {
		t.Error("an empty URL should not be emitted")
	}
}

func TestFilename(t *testing.T) {
	if got := Filename("abcd1234"); got != "overlap-abcd1234.ics" {
		t.Fatalf("Filename = %q", got)
	}
}

func slicesContain(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func hasPrefixIn(hay []string, prefix string) bool {
	for _, h := range hay {
		if strings.HasPrefix(h, prefix) {
			return true
		}
	}
	return false
}
