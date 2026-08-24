package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/maazin/Overlap/api/internal/dbgen"
	"github.com/maazin/Overlap/api/internal/icsparse"
	"github.com/maazin/Overlap/api/internal/solver"
)

// Group is a persistent set of people who schedule with each other more than
// once. Unlike an event, a group has no window and no slots of its own; it
// exists only to carry membership, roles and calendar connections forward from
// one event to the next.
type Group struct {
	ID          string
	Slug        string
	Name        string
	CreatedFrom *string
	CreatedAt   time.Time
}

// GroupMember is one person's standing membership in a group.
//
// Claimed reports whether a device has ever bound a token to this row. An
// unclaimed member exists because graduation names everyone from the event it
// grew out of before any of them has necessarily opened the group link.
type GroupMember struct {
	ID             string
	DisplayName    string
	TZ             string
	DefaultRole    string
	CalendarSource string
	CalendarURL    string
	Claimed        bool
	JoinedAt       time.Time
}

// NewGroup is the input to CreateGroup.
type NewGroup struct {
	Name string
	// CreatedFrom is the event this group graduated from, or empty for a
	// group created some other way.
	CreatedFrom string
	// Members seeds the group's roster. Graduation passes every participant
	// of the resolved event; nothing else currently calls this with members.
	Members []NewGroupMember
}

// NewGroupMember is one seat to create, unclaimed, when a group is made.
type NewGroupMember struct {
	DisplayName string
	TZ          string
	DefaultRole string
}

// CreatedGroup is what CreateGroup returns.
type CreatedGroup struct {
	Group   Group
	Members []GroupMember
}

// CreateGroup inserts a group and its initial roster in one transaction, and
// retries on slug collision the same way CreateEvent does.
func (s *Store) CreateGroup(ctx context.Context, in NewGroup) (CreatedGroup, error) {
	var lastErr error

	for range slugAttempts {
		slug, err := newSlug()
		if err != nil {
			return CreatedGroup{}, fmt.Errorf("generate slug: %w", err)
		}

		out, err := s.createGroupTx(ctx, slug, in)
		if err == nil {
			return out, nil
		}
		if isUniqueViolation(err, "groups_slug_key") {
			lastErr = err
			continue
		}
		return CreatedGroup{}, err
	}

	return CreatedGroup{}, fmt.Errorf("could not allocate a free slug in %d attempts: %w", slugAttempts, lastErr)
}

func (s *Store) createGroupTx(ctx context.Context, slug string, in NewGroup) (CreatedGroup, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreatedGroup{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	q := s.q.WithTx(tx)

	var createdFrom pgtype.UUID
	if in.CreatedFrom != "" {
		id, err := parseUUID(in.CreatedFrom)
		if err != nil {
			return CreatedGroup{}, err
		}
		createdFrom = id
	}

	row, err := q.CreateGroup(ctx, dbgen.CreateGroupParams{
		Slug:        slug,
		Name:        in.Name,
		CreatedFrom: createdFrom,
	})
	if err != nil {
		return CreatedGroup{}, err
	}

	out := CreatedGroup{Group: decodeGroup(row)}
	for _, m := range in.Members {
		mrow, err := q.CreateGroupMember(ctx, dbgen.CreateGroupMemberParams{
			GroupID:     row.ID,
			TokenHash:   nil,
			DisplayName: m.DisplayName,
			Tz:          m.TZ,
			DefaultRole: orDefault(m.DefaultRole, RoleOptional),
		})
		if err != nil {
			return CreatedGroup{}, fmt.Errorf("seed member %q: %w", m.DisplayName, err)
		}
		out.Members = append(out.Members, decodeGroupMember(mrow))
	}

	if err := tx.Commit(ctx); err != nil {
		return CreatedGroup{}, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

// GroupBySlug loads a group by its join-link slug.
func (s *Store) GroupBySlug(ctx context.Context, slug string) (Group, error) {
	row, err := s.q.GetGroupBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, fmt.Errorf("get group: %w", err)
	}
	return decodeGroup(row), nil
}

// GroupMembers lists a group's roster, oldest first.
func (s *Store) GroupMembers(ctx context.Context, groupID string) ([]GroupMember, error) {
	gid, err := parseUUID(groupID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListGroupMembers(ctx, gid)
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	out := make([]GroupMember, len(rows))
	for i, r := range rows {
		out[i] = decodeGroupMember(r)
	}
	return out, nil
}

// JoinGroup creates a brand new member row and mints its token. This is a
// genuinely new person following the group link, as distinct from claiming a
// seat graduation already created -- see ClaimGroupMember for that.
func (s *Store) JoinGroup(ctx context.Context, groupID string, in NewGroupMember) (GroupMember, string, error) {
	gid, err := parseUUID(groupID)
	if err != nil {
		return GroupMember{}, "", err
	}

	raw, digest, err := newToken()
	if err != nil {
		return GroupMember{}, "", err
	}

	row, err := s.q.CreateGroupMember(ctx, dbgen.CreateGroupMemberParams{
		GroupID:     gid,
		TokenHash:   digest,
		DisplayName: in.DisplayName,
		Tz:          in.TZ,
		DefaultRole: orDefault(in.DefaultRole, RoleOptional),
	})
	if err != nil {
		return GroupMember{}, "", fmt.Errorf("join group: %w", err)
	}
	return decodeGroupMember(row), raw, nil
}

// ClaimGroupMember binds a fresh token to an existing member row, claimed or
// not.
//
// No verification beyond picking the right row from the roster: that is the
// trust model the PRD states explicitly for groups, the same one When2Meet
// already operates at, because there is nothing behind this door worth a
// password. Re-claiming an already-claimed row (recovery on a new device, or
// after clearing storage) works the same way as a first claim.
func (s *Store) ClaimGroupMember(ctx context.Context, groupID, memberID string) (GroupMember, string, error) {
	gid, err := parseUUID(groupID)
	if err != nil {
		return GroupMember{}, "", err
	}
	mid, err := parseUUID(memberID)
	if err != nil {
		return GroupMember{}, "", err
	}

	// Scoped to the group as well as the id, so a member id from one group
	// cannot be claimed through another group's link.
	if _, err := s.q.GetGroupMemberByID(ctx, dbgen.GetGroupMemberByIDParams{ID: mid, GroupID: gid}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GroupMember{}, "", ErrNotFound
		}
		return GroupMember{}, "", fmt.Errorf("get group member: %w", err)
	}

	raw, digest, err := newToken()
	if err != nil {
		return GroupMember{}, "", err
	}

	row, err := s.q.ClaimGroupMember(ctx, dbgen.ClaimGroupMemberParams{ID: mid, TokenHash: digest})
	if err != nil {
		return GroupMember{}, "", fmt.Errorf("claim group member: %w", err)
	}
	return decodeGroupMember(row), raw, nil
}

// GroupMemberByToken resolves a caller's group token, scoped to the group the
// same way a participant token is scoped to its event.
func (s *Store) GroupMemberByToken(ctx context.Context, groupID, token string) (GroupMember, error) {
	gid, err := parseUUID(groupID)
	if err != nil {
		return GroupMember{}, err
	}
	row, err := s.q.GetGroupMemberByToken(ctx, dbgen.GetGroupMemberByTokenParams{
		GroupID: gid, TokenHash: hashToken(token),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupMember{}, ErrNotFound
	}
	if err != nil {
		return GroupMember{}, fmt.Errorf("get group member: %w", err)
	}
	return decodeGroupMember(row), nil
}

// SaveGroupMemberBusyBlocks replaces one member's stored calendar, the same
// replace-and-record pattern SaveBusyBlocks uses for event participants.
func (s *Store) SaveGroupMemberBusyBlocks(
	ctx context.Context, memberID string, blocks []icsparse.Interval, url string,
) error {
	mid, err := parseUUID(memberID)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	q := s.q.WithTx(tx)

	if err := q.DeleteGroupMemberBusyBlocks(ctx, mid); err != nil {
		return fmt.Errorf("clear busy blocks: %w", err)
	}
	if len(blocks) > 0 {
		starts := make([]pgtype.Timestamptz, len(blocks))
		ends := make([]pgtype.Timestamptz, len(blocks))
		for i, b := range blocks {
			starts[i] = pgtype.Timestamptz{Time: b.Start, Valid: true}
			ends[i] = pgtype.Timestamptz{Time: b.End, Valid: true}
		}
		if err := q.InsertGroupMemberBusyBlocks(ctx, dbgen.InsertGroupMemberBusyBlocksParams{
			GroupMemberID: mid, Column2: starts, Column3: ends, Source: CalendarICS,
		}); err != nil {
			return fmt.Errorf("insert busy blocks: %w", err)
		}
	}
	if err := q.SetGroupMemberCalendar(ctx, dbgen.SetGroupMemberCalendarParams{
		ID: mid, CalendarSource: CalendarICS, CalendarUrl: pgtype.Text{String: url, Valid: url != ""},
	}); err != nil {
		return fmt.Errorf("set calendar source: %w", err)
	}

	return tx.Commit(ctx)
}

// GroupMemberBusyBlocks returns one member's stored commitments.
func (s *Store) GroupMemberBusyBlocks(ctx context.Context, memberID string) ([]BusyBlock, error) {
	mid, err := parseUUID(memberID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListGroupMemberBusyBlocks(ctx, mid)
	if err != nil {
		return nil, fmt.Errorf("list busy blocks: %w", err)
	}
	out := make([]BusyBlock, len(rows))
	for i, r := range rows {
		out[i] = BusyBlock{
			Start: r.StartTs.Time.UTC(), End: r.EndTs.Time.UTC(),
			Source: r.Source, FetchedAt: r.FetchedAt.Time.UTC(),
		}
	}
	return out, nil
}

// GroupEvents lists every event this group has created, newest first.
func (s *Store) GroupEvents(ctx context.Context, groupID string) ([]Event, error) {
	gid, err := parseUUID(groupID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListGroupEvents(ctx, pgtype.UUID{Bytes: gid.Bytes, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list group events: %w", err)
	}
	out := make([]Event, len(rows))
	for i, r := range rows {
		ev, err := decodeEvent(r)
		if err != nil {
			return nil, err
		}
		out[i] = ev
	}
	return out, nil
}

// GroupDecisionHistory returns the group's past decisions as solver input,
// oldest information carrying no special weight of its own -- decay is
// computed in the solver from DecidedAt, which is why that timestamp exists.
func (s *Store) GroupDecisionHistory(ctx context.Context, groupID string) ([]solver.Decision, error) {
	gid, err := parseUUID(groupID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListGroupDecisions(ctx, pgtype.UUID{Bytes: gid.Bytes, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list group decisions: %w", err)
	}
	out := make([]solver.Decision, len(rows))
	for i, r := range rows {
		out[i] = solver.Decision{
			Slot:      r.DecidedSlotStart.Time.UTC(),
			DecidedAt: r.DecidedAt.Time.UTC(),
		}
	}
	return out, nil
}

// --- claiming an event seat from a group membership ---------------------------

// ClaimEventSeat resolves a group member into a participant of one of that
// group's events, creating the seat on first claim and returning it unchanged
// on every claim after.
//
// The lookup is idempotent by construction: participants_event_group_member_idx
// makes a second insert for the same pair a constraint violation rather than a
// duplicate row, so a retried or repeated claim from another device finds the
// same seat instead of minting a second one.
func (s *Store) ClaimEventSeat(ctx context.Context, eventID, groupMemberID string) (Participant, string, error) {
	eid, err := parseUUID(eventID)
	if err != nil {
		return Participant{}, "", err
	}
	mid, err := parseUUID(groupMemberID)
	if err != nil {
		return Participant{}, "", err
	}

	raw, digest, err := newToken()
	if err != nil {
		return Participant{}, "", err
	}

	existing, err := s.q.GetParticipantByGroupMember(ctx, dbgen.GetParticipantByGroupMemberParams{
		EventID: eid, GroupMemberID: pgtype.UUID{Bytes: mid.Bytes, Valid: true},
	})
	if err == nil {
		// Seat already exists; rotate its token to this device rather than
		// leaving a stale one from wherever it was last claimed.
		row, err := s.q.RotateParticipantToken(ctx, dbgen.RotateParticipantTokenParams{
			ID: existing.ID, TokenHash: digest,
		})
		if err != nil {
			return Participant{}, "", fmt.Errorf("rotate token: %w", err)
		}
		return decodeParticipant(row), raw, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Participant{}, "", fmt.Errorf("get participant: %w", err)
	}

	member, err := s.q.GetGroupMemberByIDUnscoped(ctx, mid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Participant{}, "", ErrNotFound
		}
		return Participant{}, "", fmt.Errorf("get group member: %w", err)
	}

	row, err := s.q.CreateParticipantFromGroupMember(ctx, dbgen.CreateParticipantFromGroupMemberParams{
		EventID:       eid,
		TokenHash:     digest,
		DisplayName:   member.DisplayName,
		Tz:            member.Tz,
		Role:          member.DefaultRole,
		GroupMemberID: pgtype.UUID{Bytes: mid.Bytes, Valid: true},
	})
	if err != nil {
		return Participant{}, "", fmt.Errorf("create participant from group member: %w", err)
	}
	return decodeParticipant(row), raw, nil
}

// --- conversion --------------------------------------------------------------

func decodeGroup(row dbgen.Group) Group {
	g := Group{
		ID:        formatUUID(row.ID),
		Slug:      row.Slug,
		Name:      row.Name,
		CreatedAt: row.CreatedAt.Time.UTC(),
	}
	if row.CreatedFrom.Valid {
		id := formatUUID(row.CreatedFrom)
		g.CreatedFrom = &id
	}
	return g
}

func decodeGroupMember(row dbgen.GroupMember) GroupMember {
	return GroupMember{
		ID:             formatUUID(row.ID),
		DisplayName:    row.DisplayName,
		TZ:             row.Tz,
		DefaultRole:    row.DefaultRole,
		CalendarSource: row.CalendarSource,
		CalendarURL:    row.CalendarUrl.String,
		Claimed:        row.TokenHash != nil,
		JoinedAt:       row.JoinedAt.Time.UTC(),
	}
}

// --- graduation ----------------------------------------------------------------

// GraduatedGroup is what Graduate returns.
type GraduatedGroup struct {
	Group Group
	// CallerMember is the membership row for whoever graduated the event, the
	// one person who can be handed a token directly since the graduate call
	// came from their browser. Everyone else is seeded unclaimed and claims
	// their seat by opening the group link.
	CallerMember GroupMember
	CallerToken  string
}

// Graduate mints a group from a resolved event's participants, seeding one
// member row per participant with their name, timezone and role carried over,
// and links the event to the new group.
//
// The caller must already be a participant of the event; graduating hands
// their own seat a token immediately; the rest stay unclaimed until each
// person opens the group link and picks their name.
func (s *Store) Graduate(ctx context.Context, eventID, groupName, callerParticipantID string) (GraduatedGroup, error) {
	eid, err := parseUUID(eventID)
	if err != nil {
		return GraduatedGroup{}, err
	}

	ps, err := s.Participants(ctx, eventID)
	if err != nil {
		return GraduatedGroup{}, fmt.Errorf("list participants: %w", err)
	}
	if len(ps) == 0 {
		return GraduatedGroup{}, fmt.Errorf("store: cannot graduate an event with no participants")
	}

	members := make([]NewGroupMember, len(ps))
	for i, p := range ps {
		members[i] = NewGroupMember{DisplayName: p.DisplayName, TZ: p.TZ, DefaultRole: p.Role}
	}

	created, err := s.CreateGroup(ctx, NewGroup{Name: groupName, CreatedFrom: eventID, Members: members})
	if err != nil {
		return GraduatedGroup{}, fmt.Errorf("create group: %w", err)
	}

	if _, err := s.q.LinkEventToGroup(ctx, dbgen.LinkEventToGroupParams{
		ID:      eid,
		GroupID: pgtype.UUID{Bytes: parseUUIDBytes(created.Group.ID), Valid: true},
	}); err != nil {
		return GraduatedGroup{}, fmt.Errorf("link event to group: %w", err)
	}

	// Match the caller's own new member row by name, then mint their token.
	// Matching by name rather than position keeps this correct regardless of
	// how CreateGroup ordered the seeded rows.
	var callerName string
	for _, p := range ps {
		if p.ID == callerParticipantID {
			callerName = p.DisplayName
			break
		}
	}
	var callerMember GroupMember
	for _, m := range created.Members {
		if m.DisplayName == callerName {
			callerMember = m
			break
		}
	}
	if callerMember.ID == "" {
		return GraduatedGroup{}, fmt.Errorf("store: graduating participant not found among seeded members")
	}

	claimed, token, err := s.ClaimGroupMember(ctx, created.Group.ID, callerMember.ID)
	if err != nil {
		return GraduatedGroup{}, fmt.Errorf("claim caller's seat: %w", err)
	}

	return GraduatedGroup{Group: created.Group, CallerMember: claimed, CallerToken: token}, nil
}

// parseUUIDBytes extracts the raw bytes from a canonical UUID string. Used
// only where a pgtype.UUID has to be built from a formatted id this package
// already produced, so the error case genuinely cannot happen.
func parseUUIDBytes(s string) [16]byte {
	var u pgtype.UUID
	_ = u.Scan(s)
	return u.Bytes
}

// GroupByID loads a group by its internal id, for resolving an event's
// GroupID back into something a client can link to.
func (s *Store) GroupByID(ctx context.Context, groupID string) (Group, error) {
	gid, err := parseUUID(groupID)
	if err != nil {
		return Group{}, err
	}
	row, err := s.q.GetGroupByID(ctx, gid)
	if errors.Is(err, pgx.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, fmt.Errorf("get group: %w", err)
	}
	return decodeGroup(row), nil
}

// LinkParticipantToGroupMember records which group member owns an event
// participant seat, so a later claim from another device finds this seat
// instead of minting a duplicate.
func (s *Store) LinkParticipantToGroupMember(ctx context.Context, participantID, groupMemberID string) (Participant, error) {
	pid, err := parseUUID(participantID)
	if err != nil {
		return Participant{}, err
	}
	mid, err := parseUUID(groupMemberID)
	if err != nil {
		return Participant{}, err
	}
	row, err := s.q.LinkParticipantToGroupMember(ctx, dbgen.LinkParticipantToGroupMemberParams{
		ID: pid, GroupMemberID: pgtype.UUID{Bytes: mid.Bytes, Valid: true},
	})
	if err != nil {
		return Participant{}, fmt.Errorf("link participant to group member: %w", err)
	}
	return decodeParticipant(row), nil
}
