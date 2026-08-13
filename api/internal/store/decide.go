package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/maazin/Overlap/api/internal/dbgen"
)

// Decide locks an event onto a slot.
//
// The slot is not validated here. Whether an instant is one of the event's own
// slots is a property of the window expansion, which lives in the pure slots
// package, and re-deriving it inside a database call would put the same rule in
// two places. The handler checks it against the expansion before calling.
func (s *Store) Decide(ctx context.Context, eventID string, slotStart time.Time) (Event, error) {
	eid, err := parseUUID(eventID)
	if err != nil {
		return Event{}, err
	}

	row, err := s.q.DecideEvent(ctx, dbgen.DecideEventParams{
		ID:               eid,
		DecidedSlotStart: pgtype.Timestamptz{Time: slotStart, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("decide event: %w", err)
	}
	return decodeEvent(row)
}

// Reopen returns a decided event to open, for when a locked time falls through.
func (s *Store) Reopen(ctx context.Context, eventID string) (Event, error) {
	eid, err := parseUUID(eventID)
	if err != nil {
		return Event{}, err
	}

	row, err := s.q.ReopenEvent(ctx, eid)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("reopen event: %w", err)
	}
	return decodeEvent(row)
}
