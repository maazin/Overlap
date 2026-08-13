package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/maazin/Overlap/api/internal/dbgen"
	"github.com/maazin/Overlap/api/internal/icsparse"
)

// Calendar sources a participant can connect.
const (
	CalendarNone   = "none"
	CalendarGoogle = "google"
	CalendarICS    = "ics"
)

// BusyBlock is a span during which a participant is committed.
//
// There is no title field, and there will not be one. Overlap reads free/busy
// and never event details; leaving nowhere to put a title is a cheaper
// guarantee than remembering not to store one.
type BusyBlock struct {
	Start     time.Time
	End       time.Time
	Source    string
	FetchedAt time.Time
}

// SaveBusyBlocks replaces a participant's busy blocks and records where they
// came from.
//
// Replacement rather than merge, for the same reason responses are replaced: a
// refetch must be able to remove a meeting that was cancelled, and a merge
// makes cancellation impossible to express.
func (s *Store) SaveBusyBlocks(
	ctx context.Context, participantID string, blocks []icsparse.Interval, source, url string,
) error {
	pid, err := parseUUID(participantID)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	q := s.q.WithTx(tx)

	if err := q.DeleteBusyBlocks(ctx, pid); err != nil {
		return fmt.Errorf("clear busy blocks: %w", err)
	}

	if len(blocks) > 0 {
		starts := make([]pgtype.Timestamptz, len(blocks))
		ends := make([]pgtype.Timestamptz, len(blocks))
		for i, b := range blocks {
			starts[i] = pgtype.Timestamptz{Time: b.Start, Valid: true}
			ends[i] = pgtype.Timestamptz{Time: b.End, Valid: true}
		}
		if err := q.InsertBusyBlocks(ctx, dbgen.InsertBusyBlocksParams{
			ParticipantID: pid,
			Column2:       starts,
			Column3:       ends,
			Source:        source,
		}); err != nil {
			return fmt.Errorf("insert busy blocks: %w", err)
		}
	}

	if err := q.SetCalendarSource(ctx, dbgen.SetCalendarSourceParams{
		ID:             pid,
		CalendarSource: source,
		CalendarUrl:    pgtype.Text{String: url, Valid: url != ""},
	}); err != nil {
		return fmt.Errorf("set calendar source: %w", err)
	}

	return tx.Commit(ctx)
}

// BusyBlocks returns a participant's stored commitments.
func (s *Store) BusyBlocks(ctx context.Context, participantID string) ([]BusyBlock, error) {
	pid, err := parseUUID(participantID)
	if err != nil {
		return nil, err
	}

	rows, err := s.q.ListBusyBlocks(ctx, pid)
	if err != nil {
		return nil, fmt.Errorf("list busy blocks: %w", err)
	}

	out := make([]BusyBlock, len(rows))
	for i, r := range rows {
		out[i] = BusyBlock{
			Start:     r.StartTs.Time.UTC(),
			End:       r.EndTs.Time.UTC(),
			Source:    r.Source,
			FetchedAt: r.FetchedAt.Time.UTC(),
		}
	}
	return out, nil
}
