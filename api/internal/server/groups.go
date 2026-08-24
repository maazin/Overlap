package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maazin/Overlap/api/internal/fetchguard"
	"github.com/maazin/Overlap/api/internal/icsparse"
	"github.com/maazin/Overlap/api/internal/proposal"
	"github.com/maazin/Overlap/api/internal/store"
	"github.com/maazin/Overlap/api/internal/tz"
)

// --- graduation ----------------------------------------------------------------

type graduateRequest struct {
	Name string `json:"name"`
}

type graduateResponse struct {
	GroupSlug string `json:"group_slug"`
	MemberID  string `json:"member_id"`
	Token     string `json:"token"`
}

// handleGraduate mints a group from a resolved event's participants.
//
// Restricted to the organizer and to a decided event on purpose. Graduating an
// event nobody has actually settled offers a group before its members have any
// evidence the thing worked, which is the one moment the PRD says people will
// accept the setup cost.
func (s *Server) handleGraduate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	a := mustAuthed(r)

	if !a.participant.IsOrganizer {
		s.writeError(w, r, http.StatusForbidden, "only the organizer can graduate this event")
		return
	}
	if a.event.Status != "decided" {
		s.writeError(w, r, http.StatusConflict, "this event has not been decided yet")
		return
	}

	var req graduateRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = a.event.Title
	}
	if utf8.RuneCountInString(name) > maxTitleRunes {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("name must be at most %d characters", maxTitleRunes))
		return
	}

	out, err := s.store.Graduate(r.Context(), a.event.ID, name, a.participant.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "graduate", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not create a group from this event")
		return
	}

	s.writeJSON(w, r, http.StatusCreated, graduateResponse{
		GroupSlug: out.Group.Slug,
		MemberID:  out.CallerMember.ID,
		Token:     out.CallerToken,
	})
}

// --- group view ----------------------------------------------------------------

type groupMemberView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Claimed     bool   `json:"claimed"`
	HasCalendar bool   `json:"has_calendar"`
}

type groupEventView struct {
	Slug             string     `json:"slug"`
	Title            string     `json:"title"`
	Status           string     `json:"status"`
	DecidedSlotStart *time.Time `json:"decided_slot_start,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type groupView struct {
	Slug    string            `json:"slug"`
	Name    string            `json:"name"`
	Members []groupMemberView `json:"members"`
	Events  []groupEventView  `json:"events"`

	// You is present only when a recognised member token was sent, the same
	// convention GET /api/events/{slug} uses for its own "you" section.
	You *groupMemberView `json:"you,omitempty"`
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.GroupBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.groupLookupError(w, r, err)
		return
	}

	members, err := s.store.GroupMembers(r.Context(), g.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list group members", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not load members")
		return
	}
	events, err := s.store.GroupEvents(r.Context(), g.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list group events", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not load events")
		return
	}

	out := groupView{Slug: g.Slug, Name: g.Name}
	for _, m := range members {
		out.Members = append(out.Members, toGroupMemberView(m))
	}
	for _, ev := range events {
		out.Events = append(out.Events, groupEventView{
			Slug: ev.Slug, Title: ev.Title, Status: ev.Status,
			DecidedSlotStart: ev.DecidedSlotStart, CreatedAt: ev.CreatedAt,
		})
	}

	if token := strings.TrimSpace(r.Header.Get(GroupTokenHeader)); token != "" {
		if me, err := s.store.GroupMemberByToken(r.Context(), g.ID, token); err == nil {
			v := toGroupMemberView(me)
			out.You = &v
		}
	}

	s.writeJSON(w, r, http.StatusOK, out)
}

func toGroupMemberView(m store.GroupMember) groupMemberView {
	return groupMemberView{
		ID: m.ID, Name: m.DisplayName, Role: m.DefaultRole,
		Claimed: m.Claimed, HasCalendar: m.CalendarSource != store.CalendarNone,
	}
}

func (s *Server) groupLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, "no such group")
		return
	}
	s.log.ErrorContext(r.Context(), "load group", "err", err)
	s.writeError(w, r, http.StatusInternalServerError, "could not load group")
}

// --- membership ------------------------------------------------------------

type joinGroupRequest struct {
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

type memberTokenResponse struct {
	MemberID string `json:"member_id"`
	Token    string `json:"token"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Role     string `json:"role"`
}

// handleJoinGroup adds a genuinely new person to the group by name.
func (s *Server) handleJoinGroup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	g, err := s.store.GroupBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.groupLookupError(w, r, err)
		return
	}

	var req joinGroupRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "name is required")
		return
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("name must be at most %d characters", maxNameRunes))
		return
	}
	timezone := req.Timezone
	if !tz.Valid(timezone) {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("timezone %q is not a known IANA zone name", req.Timezone))
		return
	}

	m, token, err := s.store.JoinGroup(r.Context(), g.ID, store.NewGroupMember{DisplayName: name, TZ: timezone})
	if err != nil {
		s.log.ErrorContext(r.Context(), "join group", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not join group")
		return
	}

	s.writeJSON(w, r, http.StatusCreated, memberTokenResponse{
		MemberID: m.ID, Token: token, Name: m.DisplayName, Timezone: m.TZ, Role: m.DefaultRole,
	})
}

type claimMemberRequest struct {
	MemberID string `json:"member_id"`
}

// handleClaimGroupMember binds a device to an existing roster row: a seat
// graduation created and nobody has opened yet, or recovery on a new device
// after clearing storage.
//
// No verification beyond picking the right row, deliberately: section 9 of the
// PRD states the trust model explicitly, and there is nothing behind this door
// worth protecting with anything stronger.
func (s *Server) handleClaimGroupMember(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	g, err := s.store.GroupBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.groupLookupError(w, r, err)
		return
	}

	var req claimMemberRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}
	if req.MemberID == "" {
		s.writeError(w, r, http.StatusBadRequest, "member_id is required")
		return
	}

	m, token, err := s.store.ClaimGroupMember(r.Context(), g.ID, req.MemberID)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, "no such member")
		return
	}
	if err != nil {
		s.log.ErrorContext(r.Context(), "claim group member", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not claim membership")
		return
	}

	s.writeJSON(w, r, http.StatusOK, memberTokenResponse{
		MemberID: m.ID, Token: token, Name: m.DisplayName, Timezone: m.TZ, Role: m.DefaultRole,
	})
}

// --- group-level calendar ----------------------------------------------------

// groupCalendarWindow is how far ahead a group member's calendar is read when
// connecting outside the context of any one event. A specific event's proposal
// refreshes against that event's real window regardless; this only bounds what
// gets fetched and stored at connect time, so the parse is not unbounded.
const groupCalendarWindow = 90 * 24 * time.Hour

func (s *Server) handleConnectGroupICS(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	a := mustAuthedMember(r)

	var req connectICSRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}

	body, err := s.fetcher.Get(r.Context(), req.URL)
	if err != nil {
		if errors.Is(err, fetchguard.ErrBlocked) {
			s.writeError(w, r, http.StatusBadRequest, err.Error())
			return
		}
		s.log.InfoContext(r.Context(), "group calendar: fetch failed", "err", err)
		s.writeError(w, r, http.StatusBadGateway,
			"could not read that calendar feed. Check the URL is the secret iCal address, not the web page.")
		return
	}

	now := time.Now().UTC()
	blocks := icsparse.Parse(string(body), now, now.Add(groupCalendarWindow))

	if err := s.store.SaveGroupMemberBusyBlocks(r.Context(), a.member.ID, blocks, req.URL); err != nil {
		s.log.ErrorContext(r.Context(), "group calendar: save busy blocks", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not save your calendar")
		return
	}

	s.writeJSON(w, r, http.StatusOK, connectICSResponse{
		Source: store.CalendarICS, BusyBlocks: len(blocks), FetchedAt: now,
	})
}

func (s *Server) handleDisconnectGroupCalendar(w http.ResponseWriter, r *http.Request) {
	a := mustAuthedMember(r)

	if err := s.store.SaveGroupMemberBusyBlocks(r.Context(), a.member.ID, nil, ""); err != nil {
		s.log.ErrorContext(r.Context(), "group calendar: disconnect", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not disconnect your calendar")
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]string{"source": store.CalendarNone})
}

// --- events from a group -----------------------------------------------------

type createGroupEventResponse struct {
	EventSlug string `json:"event_slug"`

	// OrganizerToken seats the creator into their own event, the same way
	// POST /api/events does. Without it "Confirm this time" would have no
	// credential to call decide with.
	OrganizerToken string        `json:"organizer_token,omitempty"`
	ParticipantID  string        `json:"participant_id,omitempty"`
	Proposal       *proposalView `json:"proposal,omitempty"`
}

type proposalView struct {
	SlotStart  time.Time `json:"slot_start"`
	Considered []string  `json:"considered"`
}

// handleCreateGroupEvent starts a new event for the group, with the creating
// member seated as organizer, and attaches a calendar-derived proposal when
// one clears.
//
// Any member can do this. Groups here are peers -- roommates, friends, a club
// board -- not an org chart, and the PRD is explicit that there is no admin
// role and nothing to configure.
func (s *Server) handleCreateGroupEvent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	a := mustAuthedMember(r)

	var req createEventRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}
	// The creator's own name is carried over from their membership rather than
	// re-typed, which is the entire point of a group event: zero re-entry.
	req.OrganizerName = a.member.DisplayName

	in, err := req.toNewEvent()
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	in.GroupID = a.group.ID
	if in.Organizer != nil {
		in.Organizer.TZ = a.member.TZ
	}

	if _, err := (store.Event{
		OrganizerTZ: in.OrganizerTZ, WindowStart: in.WindowStart, WindowEnd: in.WindowEnd,
		DayStart: in.DayStart, DayEnd: in.DayEnd, SlotMinutes: in.SlotMinutes,
	}).Slots(); err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	created, err := s.store.CreateEvent(r.Context(), in)
	if err != nil {
		s.log.ErrorContext(r.Context(), "create group event", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not create event")
		return
	}

	// Link the creator's own group membership to the seat CreateEvent just
	// made, so a later claim from another device finds it instead of minting
	// a second one for the same person.
	if created.Organizer != nil {
		if _, err := s.store.LinkParticipantToGroupMember(r.Context(), created.Organizer.ID, a.member.ID); err != nil {
			s.log.ErrorContext(r.Context(), "link organizer to group member", "err", err)
		}
	}

	out := createGroupEventResponse{EventSlug: created.Event.Slug, OrganizerToken: created.OrganizerToken}
	if created.Organizer != nil {
		out.ParticipantID = created.Organizer.ID
	}
	if prop, err := s.computeGroupProposal(r, a.group.ID, created.Event); err != nil {
		s.log.ErrorContext(r.Context(), "compute proposal", "err", err)
	} else if prop.Found {
		out.Proposal = &proposalView{SlotStart: prop.Slot.UTC(), Considered: prop.Considered}
	}

	s.writeJSON(w, r, http.StatusCreated, out)
}

// computeGroupProposal refreshes every connected member's calendar live and
// looks for a slot free for all of them.
//
// Refreshing rather than trusting whatever is cached is not optional: PRD
// section 10 requires that stale calendar data never silently produce a wrong
// proposal, and the only way to guarantee that is to not use stale data. A
// member whose refresh fails is excluded from this one proposal rather than
// falling back to their last-known busy blocks, which would be exactly the
// staleness the rule exists to rule out.
func (s *Server) computeGroupProposal(r *http.Request, groupID string, ev store.Event) (proposal.Result, error) {
	exp, err := ev.Slots()
	if err != nil {
		return proposal.Result{}, err
	}
	if len(exp.Starts) == 0 {
		return proposal.Result{}, nil
	}

	members, err := s.store.GroupMembers(r.Context(), groupID)
	if err != nil {
		return proposal.Result{}, err
	}

	window := exp.Starts[0]
	last := exp.Starts[len(exp.Starts)-1].Add(time.Duration(ev.SlotMinutes) * time.Minute)

	connected := make([]store.GroupMember, 0, len(members))
	for _, m := range members {
		if m.CalendarSource == store.CalendarICS {
			connected = append(connected, m)
		}
	}
	if len(connected) == 0 {
		return proposal.Result{}, nil
	}

	// Fetched concurrently, because these are independent calls to other
	// people's calendar hosts and this runs inside an interactive request.
	// Serially, each member costs a fresh DNS lookup, TCP connect and TLS
	// handshake against a server we do not control, up to fetchguard.Timeout
	// each; a group of ten turned "create an event" into a minute-plus wait in
	// the worst case. Concurrently the whole step costs roughly one fetch.
	results := make([]proposal.Member, len(connected))
	ok := make([]bool, len(connected))

	var wg sync.WaitGroup
	// Bounded so a large group cannot open an unlimited number of outbound
	// connections at once. Concurrency is the fix here; unbounded concurrency
	// would just be a different resource problem.
	sem := make(chan struct{}, maxConcurrentCalendarFetches)

	for i, m := range connected {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			blocks, err := s.refreshGroupMemberCalendar(r, m, window, last)
			if err != nil {
				s.log.InfoContext(r.Context(), "proposal: refresh failed, excluding member",
					"member", m.DisplayName, "err", err)
				return
			}
			// icsparse.Parse returns nil for "checked, found nothing", the same
			// zero value proposal.Member uses for "never checked". A refresh
			// that succeeded but found no conflicts must still mark this member
			// as connected, or a genuinely clear calendar gets silently treated
			// as an absent one and dropped from its own proposal.
			if blocks == nil {
				blocks = []icsparse.Interval{}
			}
			results[i] = proposal.Member{ID: m.ID, Name: m.DisplayName, Busy: blocks}
			ok[i] = true
		}()
	}
	wg.Wait()

	// Collected in the original member order rather than completion order, so
	// a proposal does not depend on which calendar host happened to answer
	// first. Each goroutine writes only its own index, so no lock is needed.
	pmembers := make([]proposal.Member, 0, len(connected))
	for i := range connected {
		if ok[i] {
			pmembers = append(pmembers, results[i])
		}
	}

	return proposal.Best(exp.Starts, time.Duration(ev.SlotMinutes)*time.Minute, pmembers), nil
}

// maxConcurrentCalendarFetches bounds the fan-out above. Eight is comfortably
// more than a typical group's connected members while still being a hard
// ceiling on outbound sockets per proposal.
const maxConcurrentCalendarFetches = 8

// refreshGroupMemberCalendar re-fetches a member's stored feed URL against a
// specific window. It does not touch what is cached for connect-time display;
// the point is precisely to bypass that cache for a proposal.
func (s *Server) refreshGroupMemberCalendar(r *http.Request, m store.GroupMember, from, to time.Time) ([]icsparse.Interval, error) {
	if m.CalendarURL == "" {
		return nil, errors.New("no calendar url on file")
	}
	body, err := s.fetcher.Get(r.Context(), m.CalendarURL)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	return icsparse.Parse(string(body), from, to), nil
}

func (s *Server) handleGroupProposal(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.GroupBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.groupLookupError(w, r, err)
		return
	}

	eventSlug := r.URL.Query().Get("event")
	if eventSlug == "" {
		s.writeError(w, r, http.StatusBadRequest, "event query parameter is required")
		return
	}
	ev, err := s.store.EventBySlug(r.Context(), eventSlug)
	if err != nil {
		s.eventLookupError(w, r, err)
		return
	}
	if ev.GroupID == nil || *ev.GroupID != g.ID {
		s.writeError(w, r, http.StatusNotFound, "that event does not belong to this group")
		return
	}
	if ev.Status != "open" {
		s.writeJSON(w, r, http.StatusOK, map[string]any{"proposal": nil})
		return
	}

	prop, err := s.computeGroupProposal(r, g.ID, ev)
	if err != nil {
		s.log.ErrorContext(r.Context(), "compute proposal", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not compute a proposal")
		return
	}
	if !prop.Found {
		s.writeJSON(w, r, http.StatusOK, map[string]any{"proposal": nil})
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"proposal": proposalView{SlotStart: prop.Slot.UTC(), Considered: prop.Considered},
	})
}

// --- claiming a seat in a group event ----------------------------------------

// handleClaimEventSeat lets a group member without a browser-local token for
// this specific event authenticate into the seat their membership already
// carries a name, timezone and role for.
//
// The header is a group token rather than a participant token on purpose: the
// whole value of a group is that membership, not a per-event login, is what
// gets someone back in.
func (s *Server) handleClaimEventSeat(w http.ResponseWriter, r *http.Request) {
	ev, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.eventLookupError(w, r, err)
		return
	}
	if ev.GroupID == nil {
		s.writeError(w, r, http.StatusConflict, "this event does not belong to a group")
		return
	}

	token := strings.TrimSpace(r.Header.Get(GroupTokenHeader))
	if token == "" {
		s.writeError(w, r, http.StatusUnauthorized, "missing "+GroupTokenHeader)
		return
	}
	member, err := s.store.GroupMemberByToken(r.Context(), *ev.GroupID, token)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusUnauthorized, "unrecognised member token")
		return
	}
	if err != nil {
		s.log.ErrorContext(r.Context(), "claim seat: load member", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not verify membership")
		return
	}

	p, ptoken, err := s.store.ClaimEventSeat(r.Context(), ev.ID, member.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "claim event seat", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not claim your seat")
		return
	}

	s.writeJSON(w, r, http.StatusOK, joinResponse{
		ParticipantID: p.ID, Token: ptoken, Name: p.DisplayName, Timezone: p.TZ, Role: p.Role,
	})
}
