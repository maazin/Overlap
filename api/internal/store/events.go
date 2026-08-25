package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/maazin/Overlap/api/internal/dbgen"
	"github.com/maazin/Overlap/api/internal/slots"
	"github.com/maazin/Overlap/api/internal/tz"
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
	DecidedAt        *time.Time

	// GroupID is set when this event was created from a group. Nil for the
	// bare one-off link, which must always keep working standalone.
	GroupID *string

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

	// Organizer, when non-nil, is joined to the event in the same transaction
	// and marked is_organizer. Leaving it nil creates an event nobody has
	// joined yet, which is legitimate: the link is what matters, not the
	// account.
	Organizer *NewParticipant

	// GroupID links this event to a group at creation. Empty for the ordinary
	// one-off flow.
	GroupID string
}

// CreatedEvent is what CreateEvent returns. OrganizerToken is empty unless an
// organizer was supplied, and is the only time that token is ever readable.
type CreatedEvent struct {
	Event          Event
	Organizer      *Participant
	OrganizerToken string
}

// CreateEvent inserts an event, retrying on slug collision.
//
// The retry is driven by the database's unique index rather than a pre-flight
// "does this slug exist" query. A check-then-insert has a race between the two
// statements; letting the insert fail and trying again cannot.
//
// The event and its organizer are written in one transaction, so a failure
// partway cannot leave an event whose organizer does not exist and which
// therefore nobody can ever decide.
func (s *Store) CreateEvent(ctx context.Context, in NewEvent) (CreatedEvent, error) {
	var lastErr error

	for range slugAttempts {
		slug, err := newSlug()
		if err != nil {
			return CreatedEvent{}, fmt.Errorf("generate slug: %w", err)
		}

		out, err := s.createEventTx(ctx, slug, in)
		if err == nil {
			return out, nil
		}
		if isUniqueViolation(err, "events_slug_key") {
			lastErr = err
			continue
		}
		return CreatedEvent{}, err
	}

	return CreatedEvent{}, fmt.Errorf("could not allocate a free slug in %d attempts: %w", slugAttempts, lastErr)
}

func (s *Store) createEventTx(ctx context.Context, slug string, in NewEvent) (CreatedEvent, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreatedEvent{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	q := s.q.WithTx(tx)

	var groupID pgtype.UUID
	if in.GroupID != "" {
		id, err := parseUUID(in.GroupID)
		if err != nil {
			return CreatedEvent{}, err
		}
		groupID = id
	}

	row, err := q.CreateEventInGroup(ctx, dbgen.CreateEventInGroupParams{
		Slug:        slug,
		Title:       in.Title,
		OrganizerTz: in.OrganizerTZ,
		WindowStart: encodeDate(in.WindowStart),
		WindowEnd:   encodeDate(in.WindowEnd),
		DayStart:    encodeTimeOfDay(in.DayStart),
		DayEnd:      encodeTimeOfDay(in.DayEnd),
		SlotMinutes: int32(in.SlotMinutes),
		GroupID:     groupID,
	})
	if err != nil {
		// Returned unwrapped so CreateEvent can recognise a slug collision.
		return CreatedEvent{}, err
	}

	ev, err := decodeEvent(row)
	if err != nil {
		return CreatedEvent{}, err
	}
	out := CreatedEvent{Event: ev}

	if in.Organizer != nil {
		raw, digest, err := newToken()
		if err != nil {
			return CreatedEvent{}, err
		}

		prow, err := q.CreateParticipant(ctx, dbgen.CreateParticipantParams{
			EventID:     row.ID,
			TokenHash:   digest,
			DisplayName: in.Organizer.DisplayName,
			Tz:          orDefault(in.Organizer.TZ, in.OrganizerTZ),
			// An organizer who is not required would let the event be settled
			// without them, which is never what someone scheduling a meeting
			// means.
			Role:        RoleRequired,
			IsOrganizer: true,
		})
		if err != nil {
			return CreatedEvent{}, fmt.Errorf("create organizer: %w", err)
		}

		p := decodeParticipant(prow)
		out.Organizer = &p
		out.OrganizerToken = raw
	}

	if err := tx.Commit(ctx); err != nil {
		return CreatedEvent{}, fmt.Errorf("commit: %w", err)
	}
	return out, nil
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
	if row.DecidedAt.Valid {
		t := row.DecidedAt.Time
		e.DecidedAt = &t
	}
	if row.GroupID.Valid {
		id := formatUUID(row.GroupID)
		e.GroupID = &id
	}
	return e, nil
}

// purgeBatch bounds one delete statement. Large enough that an ordinary sweep
// finishes in a single pass, small enough that the first sweep over a table
// nobody has ever swept does not hold one transaction open over everything.
const purgeBatch = 500

// PurgeExpired deletes events whose links have expired and reports how many
// went. It loops until a pass comes back short, so a backlog drains in one
// call without any single statement being unbounded.
//
// Cascades do the rest: participants, responses and busy blocks belong to an
// event and go with it. Groups deliberately do not, since a group outliving
// the event that created it is the entire point of graduation.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	var total int64
	for {
		n, err := s.q.DeleteExpiredEvents(ctx, purgeBatch)
		if err != nil {
			// Whatever was already deleted stays deleted; reporting the count
			// alongside the error keeps the log honest about that.
			return total, fmt.Errorf("delete expired events: %w", err)
		}
		total += n
		if n < purgeBatch {
			return total, nil
		}
		// Cooperative: a long backlog should not monopolise a connection or
		// ignore a shutdown signal.
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
	}
}
