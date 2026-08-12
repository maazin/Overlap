package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/maazinshaikh/overlap/api/internal/dbgen"
	"github.com/maazinshaikh/overlap/api/internal/slots"
	"github.com/maazinshaikh/overlap/api/internal/tz"
)

// ErrNotFound is returned when a lookup matches nothing.
var ErrNotFound = errors.New("store: not found")

// Event is the domain view of a row in events. The window is held as the
// organizer's local dates and times, exactly as entered; absolute instants are
// derived on demand rather than stored, so changing the window can never leave
// stale slots behind.
type Event struct {
	ID          string
	Slug        string
	Title       string
	OrganizerTZ string

	WindowStart slots.Date
	WindowEnd   slots.Date
	DayStart    slots.TimeOfDay
	DayEnd      slots.TimeOfDay
	SlotMinutes int

	Status           string
	DecidedSlotStart *time.Time

	CreatedAt time.Time
	ExpiresAt time.Time
}

// Window builds the expansion input for this event, resolving its zone.
func (e Event) Window() (slots.Window, error) {
	loc, err := tz.Load(e.OrganizerTZ)
	if err != nil {
		return slots.Window{}, err
	}
	return slots.Window{
		Start:       e.WindowStart,
		End:         e.WindowEnd,
		DayStart:    e.DayStart,
		DayEnd:      e.DayEnd,
		SlotMinutes: e.SlotMinutes,
		Loc:         loc,
	}, nil
}

// Slots expands the event's window into absolute instants.
func (e Event) Slots() (slots.Expansion, error) {
	w, err := e.Window()
	if err != nil {
		return slots.Expansion{}, err
	}
	return slots.Expand(w)
}

// NewEvent is the input to CreateEvent. The slug is assigned by the store, not
// the caller.
type NewEvent struct {
	Title       string
	OrganizerTZ string
	WindowStart slots.Date
	WindowEnd   slots.Date
	DayStart    slots.TimeOfDay
	DayEnd      slots.TimeOfDay
	SlotMinutes int
}

// CreateEvent inserts an event, retrying on slug collision.
//
// The retry is driven by the database's unique index rather than a pre-flight
// "does this slug exist" query. A check-then-insert has a race between the two
// statements; letting the insert fail and trying again cannot.
func (s *Store) CreateEvent(ctx context.Context, in NewEvent) (Event, error) {
	var lastErr error

	for range slugAttempts {
		slug, err := newSlug()
		if err != nil {
			return Event{}, fmt.Errorf("generate slug: %w", err)
		}

		row, err := s.q.CreateEvent(ctx, dbgen.CreateEventParams{
			Slug:        slug,
			Title:       in.Title,
			OrganizerTz: in.OrganizerTZ,
			WindowStart: encodeDate(in.WindowStart),
			WindowEnd:   encodeDate(in.WindowEnd),
			DayStart:    encodeTimeOfDay(in.DayStart),
			DayEnd:      encodeTimeOfDay(in.DayEnd),
			SlotMinutes: int32(in.SlotMinutes),
		})
		if err == nil {
			return decodeEvent(row)
		}

		if isUniqueViolation(err, "events_slug_key") {
			lastErr = err
			continue
		}
		return Event{}, fmt.Errorf("insert event: %w", err)
	}

	return Event{}, fmt.Errorf("could not allocate a free slug in %d attempts: %w", slugAttempts, lastErr)
}

// EventBySlug looks up one event.
func (s *Store) EventBySlug(ctx context.Context, slug string) (Event, error) {
	row, err := s.q.GetEventBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	return decodeEvent(row)
}

// --- pgtype conversion -------------------------------------------------------
//
// These live here rather than in the handlers so that pgtype stays an
// implementation detail of the store. A handler that had to know about
// pgtype.Time would also have to know it counts microseconds since midnight,
// which is exactly the kind of leak that produces off-by-1000 bugs.

func encodeDate(d slots.Date) pgtype.Date {
	return pgtype.Date{
		Time:  time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC),
		Valid: true,
	}
}

func decodeDate(d pgtype.Date) slots.Date {
	// pgx hands back a date as midnight UTC; only the calendar fields mean
	// anything, and reading them in any other zone would shift the day.
	t := d.Time.UTC()
	return slots.Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

const microsPerMinute = 60 * 1_000_000

func encodeTimeOfDay(t slots.TimeOfDay) pgtype.Time {
	return pgtype.Time{
		Microseconds: int64(t.Hour*60+t.Minute) * microsPerMinute,
		Valid:        true,
	}
}

func decodeTimeOfDay(t pgtype.Time) slots.TimeOfDay {
	mins := int(t.Microseconds / microsPerMinute)
	return slots.TimeOfDay{Hour: mins / 60, Minute: mins % 60}
}

// formatUUID renders pgtype.UUID's raw bytes in the canonical 8-4-4-4-12 form.
func formatUUID(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	h := hex.EncodeToString(u.Bytes[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func decodeEvent(row dbgen.Event) (Event, error) {
	e := Event{
		ID:          formatUUID(row.ID),
		Slug:        row.Slug,
		Title:       row.Title,
		OrganizerTZ: row.OrganizerTz,
		WindowStart: decodeDate(row.WindowStart),
		WindowEnd:   decodeDate(row.WindowEnd),
		DayStart:    decodeTimeOfDay(row.DayStart),
		DayEnd:      decodeTimeOfDay(row.DayEnd),
		SlotMinutes: int(row.SlotMinutes),
		Status:      row.Status,
		CreatedAt:   row.CreatedAt.Time,
		ExpiresAt:   row.ExpiresAt.Time,
	}
	if row.DecidedSlotStart.Valid {
		t := row.DecidedSlotStart.Time
		e.DecidedSlotStart = &t
	}
	return e, nil
}
