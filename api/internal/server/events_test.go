package server

import (
	"strings"
	"testing"
	"time"

	"github.com/maazin/Overlap/api/internal/slots"
)

func validRequest() createEventRequest {
	return createEventRequest{
		Title:       "Team sync",
		Timezone:    "America/New_York",
		WindowStart: "2026-03-06",
		WindowEnd:   "2026-03-09",
		DayStart:    "09:00",
		DayEnd:      "17:00",
		SlotMinutes: 45,
	}
}

func TestCreateRequestDefaults(t *testing.T) {
	req := validRequest()
	req.DayStart, req.DayEnd, req.SlotMinutes = "", "", 0

	got, err := req.toNewEvent()
	if err != nil {
		t.Fatalf("toNewEvent: %v", err)
	}

	// These must match the column defaults in 00001_events.sql.
	if got.DayStart != (slots.TimeOfDay{Hour: 9}) {
		t.Fatalf("DayStart = %s, want 09:00", got.DayStart)
	}
	if got.DayEnd != (slots.TimeOfDay{Hour: 17}) {
		t.Fatalf("DayEnd = %s, want 17:00", got.DayEnd)
	}
	if got.SlotMinutes != 30 {
		t.Fatalf("SlotMinutes = %d, want 30", got.SlotMinutes)
	}
}

func TestCreateRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*createEventRequest)
		want   string
	}{
		{"empty title", func(r *createEventRequest) { r.Title = "   " }, "title is required"},
		{"long title", func(r *createEventRequest) { r.Title = strings.Repeat("x", 121) }, "at most 120"},
		{"unknown zone", func(r *createEventRequest) { r.Timezone = "Mars/Olympus" }, "IANA"},
		{"empty zone", func(r *createEventRequest) { r.Timezone = "" }, "IANA"},
		// "Local" resolves against the server's own zone, which would make the
		// result depend on where the API runs.
		{"local zone", func(r *createEventRequest) { r.Timezone = "Local" }, "IANA"},
		{"missing window start", func(r *createEventRequest) { r.WindowStart = "" }, "window_start"},
		{"impossible date", func(r *createEventRequest) { r.WindowStart = "2026-02-30" }, "window_start"},
		{"american date", func(r *createEventRequest) { r.WindowStart = "03/06/2026" }, "window_start"},
		{"bad day band", func(r *createEventRequest) { r.DayStart = "9am" }, "day_start"},
		{"hour out of range", func(r *createEventRequest) { r.DayEnd = "25:00" }, "day_end"},
		{"25 hour minute", func(r *createEventRequest) { r.DayEnd = "24:30" }, "day_end"},
		{"slot too small", func(r *createEventRequest) { r.SlotMinutes = 1 }, "slot_minutes"},
		{"slot too large", func(r *createEventRequest) { r.SlotMinutes = 481 }, "slot_minutes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			tc.mutate(&req)

			_, err := req.toNewEvent()
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestParseTimeOfDayAcceptsEndOfDay(t *testing.T) {
	got, err := parseTimeOfDayOr("24:00", slots.TimeOfDay{})
	if err != nil {
		t.Fatalf("24:00 must be a legal end bound: %v", err)
	}
	if got != (slots.TimeOfDay{Hour: 24}) {
		t.Fatalf("got %s, want 24:00", got)
	}
}

// TestDSTNotesDescribeBothAnomalies checks the organizer is actually told when
// their window hit a transition, rather than silently receiving a schedule that
// does not match what they asked for.
func TestDSTNotesDescribeBothAnomalies(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	spring, err := slots.Expand(slots.Window{
		Start:    slots.Date{Year: 2026, Month: time.March, Day: 8},
		End:      slots.Date{Year: 2026, Month: time.March, Day: 8},
		DayStart: slots.TimeOfDay{Hour: 1}, DayEnd: slots.TimeOfDay{Hour: 4},
		SlotMinutes: 30, Loc: ny,
	})
	if err != nil {
		t.Fatal(err)
	}
	notes := dstNotes(spring)
	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(notes))
	}
	for _, n := range notes {
		if n.Reason != "nonexistent" {
			t.Fatalf("reason = %q, want nonexistent", n.Reason)
		}
	}

	fall, err := slots.Expand(slots.Window{
		Start:    slots.Date{Year: 2026, Month: time.November, Day: 1},
		End:      slots.Date{Year: 2026, Month: time.November, Day: 1},
		DayStart: slots.TimeOfDay{Hour: 1}, DayEnd: slots.TimeOfDay{Hour: 3},
		SlotMinutes: 30, Loc: ny,
	})
	if err != nil {
		t.Fatal(err)
	}
	notes = dstNotes(fall)
	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(notes))
	}
	for _, n := range notes {
		if n.Reason != "ambiguous" {
			t.Fatalf("reason = %q, want ambiguous", n.Reason)
		}
	}
}

func TestNoDSTNotesWhenNothingHappened(t *testing.T) {
	if got := dstNotes(slots.Expansion{}); got != nil {
		t.Fatalf("got %v, want nil so the field is omitted from JSON", got)
	}
}
