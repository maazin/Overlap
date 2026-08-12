package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/maazinshaikh/overlap/api/internal/dbgen"
	"github.com/maazinshaikh/overlap/api/internal/solver"
)

// Roles a participant can hold. Only a required participant can veto a slot.
const (
	RoleOptional = "optional"
	RoleRequired = "required"
)

// Sources a response tier can come from.
const (
	SourceManual   = "manual"   // the person set this slot themselves
	SourceCoarse   = "coarse"   // inherited from a day-part they marked
	SourceCalendar = "calendar" // inferred from imported free/busy
)

// tokenBytes is the size of a participant token before encoding. 256 bits of
// CSPRNG output is far past anything guessable, which is what lets the database
// store a plain SHA-256 digest instead of a password KDF.
const tokenBytes = 32

// Participant is one person invited to an event.
type Participant struct {
	ID             string
	DisplayName    string
	TZ             string
	Role           string
	IsOrganizer    bool
	CalendarSource string

	// RespondedAt is nil until this person submits. Nil means unknown, which
	// is not the same as having said no to everything, and the solver relies on
	// the difference.
	RespondedAt *time.Time
	CreatedAt   time.Time
}

// Responded reports whether this person has submitted anything.
func (p Participant) Responded() bool { return p.RespondedAt != nil }

// NewParticipant is the input to a join.
type NewParticipant struct {
	DisplayName string
	TZ          string
	Role        string
	IsOrganizer bool
}

// Response is one person's feeling about one slot.
type Response struct {
	SlotStart time.Time
	Tier      solver.Tier
	Source    string
}

// newToken mints a participant token, returning the value handed to the client
// and the digest stored in the database. The raw token is never persisted, so a
// leaked backup does not let anyone impersonate a participant.
func newToken() (raw string, digest []byte, err error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw), nil
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// JoinEvent creates a participant and returns the token the client must keep.
//
// The token is returned exactly once. There is no endpoint that reads it back,
// because the server does not have it.
func (s *Store) JoinEvent(ctx context.Context, eventID string, in NewParticipant) (Participant, string, error) {
	eid, err := parseUUID(eventID)
	if err != nil {
		return Participant{}, "", err
	}

	raw, digest, err := newToken()
	if err != nil {
		return Participant{}, "", err
	}

	row, err := s.q.CreateParticipant(ctx, dbgen.CreateParticipantParams{
		EventID:     eid,
		TokenHash:   digest,
		DisplayName: in.DisplayName,
		Tz:          in.TZ,
		Role:        orDefault(in.Role, RoleOptional),
		IsOrganizer: in.IsOrganizer,
	})
	if err != nil {
		return Participant{}, "", fmt.Errorf("create participant: %w", err)
	}
	return decodeParticipant(row), raw, nil
}

// ParticipantByToken resolves a token within one event.
//
// The event is part of the lookup rather than a check afterwards, so a token
// minted for one event simply does not exist in another. That makes cross-event
// reuse a miss instead of a comparison someone could forget to write.
func (s *Store) ParticipantByToken(ctx context.Context, eventID, token string) (Participant, error) {
	eid, err := parseUUID(eventID)
	if err != nil {
		return Participant{}, err
	}

	row, err := s.q.GetParticipantByToken(ctx, dbgen.GetParticipantByTokenParams{
		EventID:   eid,
		TokenHash: hashToken(token),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Participant{}, ErrNotFound
	}
	if err != nil {
		return Participant{}, fmt.Errorf("get participant: %w", err)
	}

	// The unique index already made this an equality lookup, so this is belt
	// and braces rather than the real defence; it costs nothing and keeps the
	// comparison constant-time if the lookup is ever loosened.
	if subtle.ConstantTimeCompare(row.TokenHash, hashToken(token)) != 1 {
		return Participant{}, ErrNotFound
	}

	return decodeParticipant(row), nil
}

// Participants lists everyone on an event, oldest first.
func (s *Store) Participants(ctx context.Context, eventID string) ([]Participant, error) {
	eid, err := parseUUID(eventID)
	if err != nil {
		return nil, err
	}

	rows, err := s.q.ListParticipants(ctx, eid)
	if err != nil {
		return nil, fmt.Errorf("list participants: %w", err)
	}

	out := make([]Participant, len(rows))
	for i, r := range rows {
		out[i] = decodeParticipant(r)
	}
	return out, nil
}

// SaveResponses replaces a participant's entire response set and marks them as
// having answered.
//
// Replacement rather than merge is deliberate: the client always submits the
// complete picture, and a merge would make it impossible to withdraw a slot
// someone had previously marked. All three statements share a transaction, so a
// failure halfway cannot leave someone marked as responded with half a
// response, which would read as a deliberate wall of "no" to the solver.
func (s *Store) SaveResponses(ctx context.Context, participantID string, rs []Response) error {
	pid, err := parseUUID(participantID)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	// Rollback after a successful commit is a no-op, so this is safe to defer
	// unconditionally and removes any path that leaks the transaction.
	defer tx.Rollback(ctx)

	q := s.q.WithTx(tx)

	if err := q.DeleteResponsesForParticipant(ctx, pid); err != nil {
		return fmt.Errorf("clear responses: %w", err)
	}

	if len(rs) > 0 {
		starts := make([]pgtype.Timestamptz, len(rs))
		tiers := make([]int16, len(rs))
		sources := make([]string, len(rs))
		for i, r := range rs {
			starts[i] = pgtype.Timestamptz{Time: r.SlotStart, Valid: true}
			tiers[i] = int16(r.Tier)
			sources[i] = orDefault(r.Source, SourceManual)
		}

		if err := q.InsertResponses(ctx, dbgen.InsertResponsesParams{
			ParticipantID: pid,
			Column2:       starts,
			Column3:       tiers,
			Column4:       sources,
		}); err != nil {
			return fmt.Errorf("insert responses: %w", err)
		}
	}

	if err := q.MarkParticipantResponded(ctx, pid); err != nil {
		return fmt.Errorf("mark responded: %w", err)
	}

	return tx.Commit(ctx)
}

// ResponsesForParticipant returns one person's stored tiers.
func (s *Store) ResponsesForParticipant(ctx context.Context, participantID string) ([]Response, error) {
	pid, err := parseUUID(participantID)
	if err != nil {
		return nil, err
	}

	rows, err := s.q.ListResponsesForParticipant(ctx, pid)
	if err != nil {
		return nil, fmt.Errorf("list responses: %w", err)
	}

	out := make([]Response, len(rows))
	for i, r := range rows {
		out[i] = Response{
			SlotStart: r.SlotStart.Time.UTC(),
			Tier:      solver.Tier(r.Tier),
			Source:    r.Source,
		}
	}
	return out, nil
}

// ResponsesForEvent returns every stored tier on an event, keyed by
// participant ID.
func (s *Store) ResponsesForEvent(ctx context.Context, eventID string) (map[string][]Response, error) {
	eid, err := parseUUID(eventID)
	if err != nil {
		return nil, err
	}

	rows, err := s.q.ListResponsesForEvent(ctx, eid)
	if err != nil {
		return nil, fmt.Errorf("list event responses: %w", err)
	}

	out := make(map[string][]Response)
	for _, r := range rows {
		pid := formatUUID(r.ParticipantID)
		out[pid] = append(out[pid], Response{
			SlotStart: r.SlotStart.Time.UTC(),
			Tier:      solver.Tier(r.Tier),
			Source:    r.Source,
		})
	}
	return out, nil
}

// --- conversion --------------------------------------------------------------

func decodeParticipant(row dbgen.Participant) Participant {
	p := Participant{
		ID:             formatUUID(row.ID),
		DisplayName:    row.DisplayName,
		TZ:             row.Tz,
		Role:           row.Role,
		IsOrganizer:    row.IsOrganizer,
		CalendarSource: row.CalendarSource,
		CreatedAt:      row.CreatedAt.Time.UTC(),
	}
	if row.RespondedAt.Valid {
		t := row.RespondedAt.Time.UTC()
		p.RespondedAt = &t
	}
	return p
}

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: malformed id %q", ErrNotFound, s)
	}
	return u, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
