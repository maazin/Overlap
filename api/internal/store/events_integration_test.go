package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/maazinshaikh/overlap/api/internal/slots"
)

// testStore connects to the database named by TEST_DATABASE_URL, skipping when
// it is unset so that `go test ./...` stays runnable without infrastructure.
// `make test-integration` supplies it.
func testStore(t *testing.T) *Store {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; run `make test-integration`")
	}

	ctx := t.Context()
	st, err := New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func sampleEvent() NewEvent {
	return NewEvent{
		Title:       "Team sync",
		OrganizerTZ: "America/New_York",
		WindowStart: slots.Date{Year: 2026, Month: time.March, Day: 6},
		WindowEnd:   slots.Date{Year: 2026, Month: time.March, Day: 9},
		DayStart:    slots.TimeOfDay{Hour: 9},
		DayEnd:      slots.TimeOfDay{Hour: 17},
		SlotMinutes: 45,
	}
}

// TestCreateAndReadBack is the round trip that matters: Postgres `date` and
// `time` columns must come back as exactly the calendar values that went in.
// Getting this wrong by a timezone offset is the classic way a scheduling tool
// ends up a day out for half its users.
func TestCreateAndReadBack(t *testing.T) {
	st := testStore(t)
	in := sampleEvent()

	created, err := st.CreateEvent(t.Context(), in)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if created.Slug == "" {
		t.Fatal("created event has no slug")
	}
	if created.Status != "open" {
		t.Fatalf("Status = %q, want open", created.Status)
	}
	if created.DecidedSlotStart != nil {
		t.Fatal("a new event cannot already be decided")
	}

	got, err := st.EventBySlug(t.Context(), created.Slug)
	if err != nil {
		t.Fatalf("EventBySlug: %v", err)
	}

	if got.WindowStart != in.WindowStart {
		t.Fatalf("WindowStart = %s, want %s", got.WindowStart, in.WindowStart)
	}
	if got.WindowEnd != in.WindowEnd {
		t.Fatalf("WindowEnd = %s, want %s", got.WindowEnd, in.WindowEnd)
	}
	if got.DayStart != in.DayStart {
		t.Fatalf("DayStart = %s, want %s", got.DayStart, in.DayStart)
	}
	if got.DayEnd != in.DayEnd {
		t.Fatalf("DayEnd = %s, want %s", got.DayEnd, in.DayEnd)
	}
	if got.SlotMinutes != in.SlotMinutes {
		t.Fatalf("SlotMinutes = %d, want %d", got.SlotMinutes, in.SlotMinutes)
	}
	if got.OrganizerTZ != in.OrganizerTZ {
		t.Fatalf("OrganizerTZ = %q, want %q", got.OrganizerTZ, in.OrganizerTZ)
	}
	if got.ID == "" || len(got.ID) != 36 {
		t.Fatalf("ID = %q, want a formatted uuid", got.ID)
	}
	if got.ExpiresAt.Before(time.Now()) {
		t.Fatalf("ExpiresAt = %s, want a future default", got.ExpiresAt)
	}
}

// TestHalfHourBandSurvivesTheTimeColumn guards the pgtype.Time conversion,
// where the unit is microseconds and an off-by-1000 would be invisible until
// someone set a band that was not on the hour.
func TestHalfHourBandSurvivesTheTimeColumn(t *testing.T) {
	st := testStore(t)

	in := sampleEvent()
	in.DayStart = slots.TimeOfDay{Hour: 8, Minute: 30}
	in.DayEnd = slots.TimeOfDay{Hour: 17, Minute: 45}

	created, err := st.CreateEvent(t.Context(), in)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	got, err := st.EventBySlug(t.Context(), created.Slug)
	if err != nil {
		t.Fatalf("EventBySlug: %v", err)
	}

	if got.DayStart != in.DayStart || got.DayEnd != in.DayEnd {
		t.Fatalf("band round-tripped as %s-%s, want %s-%s",
			got.DayStart, got.DayEnd, in.DayStart, in.DayEnd)
	}
}

// TestStoredEventExpandsAcrossDST is the phase DoD end to end: an event that
// spans the US spring-forward transition, persisted and read back, produces the
// hand-computed absolute instants.
func TestStoredEventExpandsAcrossDST(t *testing.T) {
	st := testStore(t)

	in := sampleEvent()
	in.DayStart = slots.TimeOfDay{Hour: 9}
	in.DayEnd = slots.TimeOfDay{Hour: 11}
	in.SlotMinutes = 60

	created, err := st.CreateEvent(t.Context(), in)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	got, err := st.EventBySlug(t.Context(), created.Slug)
	if err != nil {
		t.Fatalf("EventBySlug: %v", err)
	}

	exp, err := got.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}

	want := []string{
		"2026-03-06 14:00", "2026-03-06 15:00", // EST, UTC-5
		"2026-03-07 14:00", "2026-03-07 15:00",
		"2026-03-08 13:00", "2026-03-08 14:00", // EDT, UTC-4
		"2026-03-09 13:00", "2026-03-09 14:00",
	}
	if len(exp.Starts) != len(want) {
		t.Fatalf("got %d slots, want %d", len(exp.Starts), len(want))
	}
	for i, s := range exp.Starts {
		if got := s.UTC().Format("2006-01-02 15:04"); got != want[i] {
			t.Fatalf("slot %d = %s, want %s", i, got, want[i])
		}
	}
}

func TestEventBySlugMissing(t *testing.T) {
	st := testStore(t)

	_, err := st.EventBySlug(t.Context(), "nosuchslug")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestSlugsAreUnique exercises the real unique index rather than trusting the
// random source.
func TestSlugsAreUnique(t *testing.T) {
	st := testStore(t)

	seen := make(map[string]bool, 50)
	for range 50 {
		ev, err := st.CreateEvent(t.Context(), sampleEvent())
		if err != nil {
			t.Fatalf("CreateEvent: %v", err)
		}
		if seen[ev.Slug] {
			t.Fatalf("duplicate slug %q", ev.Slug)
		}
		seen[ev.Slug] = true

		if len(ev.Slug) != slugLength {
			t.Fatalf("slug %q has length %d, want %d", ev.Slug, len(ev.Slug), slugLength)
		}
	}
}

// TestCheckConstraintsRejectBadRows confirms the database is the backstop, not
// just the handler. These inputs bypass HTTP validation entirely.
func TestCheckConstraintsRejectBadRows(t *testing.T) {
	st := testStore(t)

	for _, tc := range []struct {
		name   string
		mutate func(*NewEvent)
	}{
		{"inverted window", func(e *NewEvent) {
			e.WindowStart, e.WindowEnd = e.WindowEnd, e.WindowStart
		}},
		{"inverted day band", func(e *NewEvent) {
			e.DayStart, e.DayEnd = e.DayEnd, e.DayStart
		}},
		{"blank title", func(e *NewEvent) { e.Title = "   " }},
		{"absurd slot size", func(e *NewEvent) { e.SlotMinutes = 100000 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := sampleEvent()
			tc.mutate(&in)

			if _, err := st.CreateEvent(t.Context(), in); err == nil {
				t.Fatal("expected the database to reject this row")
			}
		})
	}
}

func TestContextCancellationIsHonoured(t *testing.T) {
	st := testStore(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := st.CreateEvent(ctx, sampleEvent()); err == nil {
		t.Fatal("a cancelled context must not produce a write")
	}
}
