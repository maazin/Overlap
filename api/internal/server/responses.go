package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maazin/Overlap/api/internal/dayparts"
	"github.com/maazin/Overlap/api/internal/solver"
	"github.com/maazin/Overlap/api/internal/sse"
	"github.com/maazin/Overlap/api/internal/store"
	"github.com/maazin/Overlap/api/internal/tz"
)

const maxNameRunes = 60

// --- join --------------------------------------------------------------------

type joinRequest struct {
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Role     string `json:"role"`
}

type joinResponse struct {
	ParticipantID string `json:"participant_id"`
	Token         string `json:"token"`
	Name          string `json:"name"`
	Timezone      string `json:"timezone"`
	Role          string `json:"role"`
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	ev, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.eventLookupError(w, r, err)
		return
	}

	var req joinRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}

	in, err := req.toNewParticipant(ev.OrganizerTZ)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	p, token, err := s.store.JoinEvent(r.Context(), ev.ID, in)
	if err != nil {
		s.log.ErrorContext(r.Context(), "join event", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not join event")
		return
	}

	s.writeJSON(w, r, http.StatusCreated, joinResponse{
		ParticipantID: p.ID,
		Token:         token,
		Name:          p.DisplayName,
		Timezone:      p.TZ,
		Role:          p.Role,
	})
}

func (req joinRequest) toNewParticipant(fallbackTZ string) (store.NewParticipant, error) {
	var out store.NewParticipant

	out.DisplayName = strings.TrimSpace(req.Name)
	if out.DisplayName == "" {
		return out, fmt.Errorf("name is required")
	}
	if utf8.RuneCountInString(out.DisplayName) > maxNameRunes {
		return out, fmt.Errorf("name must be at most %d characters", maxNameRunes)
	}

	// Falling back to the organizer's zone keeps a joiner whose browser reports
	// nothing usable from being written down as UTC, which would silently shift
	// every day part they see.
	out.TZ = req.Timezone
	if out.TZ == "" {
		out.TZ = fallbackTZ
	}
	if !tz.Valid(out.TZ) {
		return out, fmt.Errorf("timezone %q is not a known IANA zone name", req.Timezone)
	}

	switch req.Role {
	case "", store.RoleOptional:
		out.Role = store.RoleOptional
	case store.RoleRequired:
		out.Role = store.RoleRequired
	default:
		return out, fmt.Errorf("role must be %q or %q", store.RoleRequired, store.RoleOptional)
	}

	return out, nil
}

// --- responses ---------------------------------------------------------------

type coarseSelection struct {
	Date  string `json:"date"`
	Block string `json:"block"`
	Tier  string `json:"tier"`
}

type slotSelection struct {
	SlotStart time.Time `json:"slot_start"`
	Tier      string    `json:"tier"`
}

// putResponsesRequest is the whole response set, always submitted complete.
//
// Coarse selections are expanded server-side using the responder's own zone,
// which is why the client sends day parts rather than the slots it thinks they
// cover: the server already knows the slot list and the responder's zone, and
// letting the client compute the mapping would let the two disagree.
type putResponsesRequest struct {
	Coarse []coarseSelection `json:"coarse"`
	Slots  []slotSelection   `json:"slots"`

	// Timezone lets a responder correct their zone at submit time without a
	// separate round trip. Omitted means "leave it alone".
	Timezone string `json:"timezone"`
}

type putResponsesResponse struct {
	Saved int `json:"saved"`
}

func (s *Server) handlePutResponses(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxResponseBodyBytes)
	a := mustAuthed(r)

	var req putResponsesRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}

	exp, err := a.event.Slots()
	if err != nil {
		s.log.ErrorContext(r.Context(), "expand slots", "err", err, "slug", a.event.Slug)
		s.writeError(w, r, http.StatusInternalServerError, "could not expand slots")
		return
	}

	zone := a.participant.TZ
	if req.Timezone != "" {
		if !tz.Valid(req.Timezone) {
			s.writeError(w, r, http.StatusBadRequest,
				fmt.Sprintf("timezone %q is not a known IANA zone name", req.Timezone))
			return
		}
		zone = req.Timezone
	}
	loc, err := tz.Load(zone)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	rs, err := buildResponses(exp.Starts, loc, req)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// Calendar-derived rows are carried across a submission.
	//
	// A response replaces the whole set, which is what lets somebody withdraw a
	// slot they previously offered. Without this, submitting the grid would
	// also silently delete everything the calendar contributed, and the times
	// somebody is genuinely booked would quietly come back as available.
	// Anything the person stated still wins: only slots the submission does not
	// mention are filled from the calendar.
	prior, err := s.store.ResponsesForParticipant(r.Context(), a.participant.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "load prior responses", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not save responses")
		return
	}
	rs = mergeCalendarRows(rs, prior)

	if err := s.store.SaveResponses(r.Context(), a.participant.ID, rs); err != nil {
		s.log.ErrorContext(r.Context(), "save responses", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not save responses")
		return
	}

	s.publish(a.event.ID, sse.EventResponseSubmitted)
	s.writeJSON(w, r, http.StatusOK, putResponsesResponse{Saved: len(rs)})
}

// maxResponseBodyBytes is larger than maxBodyBytes because a fine-grained
// response can legitimately carry one entry per slot.
const maxResponseBodyBytes = 512 << 10

// buildResponses turns a submission into the rows to store.
//
// Coarse selections are laid down first and fine selections overlay them, which
// is the inheritance rule the input model is built on: marking a morning as
// workable sets every slot in it, and then narrowing one of those slots
// overrides just that one. Source is recorded per row so the two remain
// distinguishable afterwards.
func buildResponses(starts []time.Time, loc *time.Location, req putResponsesRequest) ([]store.Response, error) {
	sel := make([]dayparts.Selection, 0, len(req.Coarse))
	for i, c := range req.Coarse {
		block, err := dayparts.ParseBlock(c.Block)
		if err != nil {
			return nil, fmt.Errorf("coarse[%d]: %w", i, err)
		}
		date, err := parseDate(c.Date)
		if err != nil {
			return nil, fmt.Errorf("coarse[%d].date: %w", i, err)
		}
		tier, err := parseTier(c.Tier)
		if err != nil {
			return nil, fmt.Errorf("coarse[%d]: %w", i, err)
		}
		sel = append(sel, dayparts.Selection{
			Cell: dayparts.Cell{Date: date, Block: block},
			Tier: tier,
		})
	}

	tiers := dayparts.Expand(starts, loc, sel)
	sources := make(map[time.Time]string, len(tiers))
	for t := range tiers {
		sources[t] = store.SourceCoarse
	}

	// A set of the event's real slots. Anything else is rejected rather than
	// stored: a response against a slot that does not exist would never be
	// scored and would quietly misrepresent how complete someone's answer is.
	valid := make(map[int64]time.Time, len(starts))
	for _, t := range starts {
		valid[t.UTC().UnixNano()] = t
	}

	for i, sl := range req.Slots {
		canonical, ok := valid[sl.SlotStart.UTC().UnixNano()]
		if !ok {
			return nil, fmt.Errorf("slots[%d]: %s is not a slot of this event",
				i, sl.SlotStart.UTC().Format(time.RFC3339))
		}
		tier, err := parseTier(sl.Tier)
		if err != nil {
			return nil, fmt.Errorf("slots[%d]: %w", i, err)
		}
		tiers[canonical] = tier
		sources[canonical] = store.SourceManual
	}

	out := make([]store.Response, 0, len(tiers))
	for t, tier := range tiers {
		out = append(out, store.Response{SlotStart: t, Tier: tier, Source: sources[t]})
	}
	// Stable order keeps stored rows and test expectations predictable.
	sort.Slice(out, func(i, j int) bool { return out[i].SlotStart.Before(out[j].SlotStart) })

	return out, nil
}

// mergeCalendarRows adds back calendar-sourced tiers for any slot the new
// submission is silent about, preserving order by slot start.
func mergeCalendarRows(stated, prior []store.Response) []store.Response {
	seen := make(map[int64]bool, len(stated))
	for _, r := range stated {
		seen[r.SlotStart.UnixNano()] = true
	}

	out := stated
	for _, r := range prior {
		if r.Source == store.SourceCalendar && !seen[r.SlotStart.UnixNano()] {
			out = append(out, r)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].SlotStart.Before(out[j].SlotStart) })
	return out
}

// --- tier vocabulary ---------------------------------------------------------

// parseTier is the inverse of solver.Tier.String(). A test pins the round trip,
// because the two living in different packages is exactly how a vocabulary
// drifts.
func parseTier(s string) (solver.Tier, error) {
	switch s {
	case "preferred":
		return solver.TierPreferred, nil
	case "ok":
		return solver.TierOK, nil
	case "if_needed":
		return solver.TierIfNeeded, nil
	case "no":
		return solver.TierNo, nil
	default:
		return 0, fmt.Errorf("unknown tier %q", s)
	}
}

// --- shared helpers ----------------------------------------------------------

func (s *Server) eventLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, "no such event")
		return
	}
	s.log.ErrorContext(r.Context(), "load event", "err", err)
	s.writeError(w, r, http.StatusInternalServerError, "could not load event")
}
