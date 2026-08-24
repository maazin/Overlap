package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/maazin/Overlap/api/internal/ics"
	"github.com/maazin/Overlap/api/internal/results"
	"github.com/maazin/Overlap/api/internal/solver"
	"github.com/maazin/Overlap/api/internal/sse"
	"github.com/maazin/Overlap/api/internal/store"
	"github.com/maazin/Overlap/api/internal/tz"
)

// rankedSlot is one scored slot as the API reports it.
//
// There is deliberately no score field. The composite is what orders the list,
// but a number like 0.7855 shown next to a time reads as precision the model
// does not have, and invites arguing with it. The honest summary is who can
// come and who it costs, so that is all that leaves the server.
type rankedSlot struct {
	SlotStart time.Time `json:"slot_start"`

	Coverage int `json:"coverage"`
	Total    int `json:"total"`

	Eliminated   bool     `json:"eliminated"`
	EliminatedBy []string `json:"eliminated_by,omitempty"`
	Excludes     []string `json:"excludes,omitempty"`
	Unknown      []string `json:"unknown,omitempty"`

	// Unsociable is true when the slot falls outside working hours for at least
	// one person, which is worth surfacing as a caveat even though the ranking
	// has already accounted for it.
	Unsociable bool `json:"unsociable"`
}

type solveResponse struct {
	Slug      string       `json:"slug"`
	Status    string       `json:"status"`
	Responded int          `json:"responded"`
	Total     int          `json:"total"`
	Ranked    []rankedSlot `json:"ranked"`

	Dominance dominanceView `json:"dominance"`

	DecidedSlotStart *time.Time `json:"decided_slot_start,omitempty"`
}

// Verdicts the dominance analysis can reach. These are the distinct situations
// an organizer can be in, and each one implies a different next action, which
// is why the server names the state rather than leaving the client to infer it
// from three booleans.
const (
	// VerdictDecided -- already locked.
	VerdictDecided = "decided"
	// VerdictDecidable -- one slot wins whatever the outstanding people say.
	VerdictDecidable = "decidable"
	// VerdictWaitingRequired -- a required person is silent, so they could
	// still veto anything and nothing can be settled.
	VerdictWaitingRequired = "waiting_on_required"
	// VerdictWaitingRelevant -- named people could still change the order.
	VerdictWaitingRelevant = "waiting_on_relevant"
	// VerdictTied -- undecided with nobody worth chasing, because no
	// outstanding answer separates the leaders. Either will do.
	VerdictTied = "tied"
	// VerdictNoSlots -- every slot is vetoed by someone required.
	VerdictNoSlots = "no_slots"
)

type dominanceView struct {
	// Verdict is what the UI should say. The other fields are what it needs to
	// say it specifically.
	Verdict   string `json:"verdict"`
	Decidable bool   `json:"decidable"`

	Leader *time.Time `json:"leader,omitempty"`

	// BlockingRequired names required people who have not answered. While this
	// is non-empty nothing is decidable, which is not a limitation but the
	// correct answer: any of them could still rule out the leader.
	BlockingRequired []string `json:"blocking_required,omitempty"`

	// Relevant names outstanding people whose answer could still change which
	// slot wins. Anyone absent from this list should not be chased.
	Relevant []string `json:"relevant,omitempty"`
}

// toDominanceView turns the analysis into a verdict plus its supporting names.
func toDominanceView(d solver.Dominance, ranked []solver.SlotScore, status string) dominanceView {
	out := dominanceView{
		Decidable:        d.Decidable,
		BlockingRequired: d.BlockingRequired,
		Relevant:         d.Relevant,
	}
	if !d.Leader.IsZero() {
		leader := d.Leader.UTC()
		out.Leader = &leader
	}

	anyAlive := false
	for _, r := range ranked {
		if !r.Eliminated {
			anyAlive = true
			break
		}
	}

	switch {
	case status == "decided":
		out.Verdict = VerdictDecided
	case !anyAlive:
		out.Verdict = VerdictNoSlots
	case d.Decidable:
		out.Verdict = VerdictDecidable
	case len(d.BlockingRequired) > 0:
		out.Verdict = VerdictWaitingRequired
	case len(d.Relevant) > 0:
		out.Verdict = VerdictWaitingRelevant
	default:
		// Undecided with nobody to wait for is a genuine tie, not a stall.
		out.Verdict = VerdictTied
	}
	return out
}

func (s *Server) handleSolve(w http.ResponseWriter, r *http.Request) {
	ev, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.eventLookupError(w, r, err)
		return
	}

	ranked, ps, dom, err := s.rankAndAnalyze(r, ev)
	if err != nil {
		s.log.ErrorContext(r.Context(), "solve", "err", err, "slug", ev.Slug)
		s.writeError(w, r, http.StatusInternalServerError, "could not compute results")
		return
	}

	out := solveResponse{
		Slug:             ev.Slug,
		Status:           ev.Status,
		Responded:        results.RespondedCount(ps),
		Total:            len(ps),
		Ranked:           make([]rankedSlot, 0, len(ranked)),
		Dominance:        toDominanceView(dom, ranked, ev.Status),
		DecidedSlotStart: ev.DecidedSlotStart,
	}
	for _, sc := range ranked {
		out.Ranked = append(out.Ranked, toRankedSlot(sc, len(ps)))
	}

	s.writeJSON(w, r, http.StatusOK, out)
}

// rank loads everything an event's results depend on and scores it.
func (s *Server) rank(r *http.Request, ev store.Event) ([]solver.SlotScore, []store.Participant, error) {
	ranked, ps, _, err := s.rankAndAnalyze(r, ev)
	return ranked, ps, err
}

// rankAndAnalyze loads the event's data once and derives both the ranking and
// the dominance verdict from it, so the two can never be computed from
// different snapshots and disagree about who is winning.
func (s *Server) rankAndAnalyze(r *http.Request, ev store.Event) (
	[]solver.SlotScore, []store.Participant, solver.Dominance, error,
) {
	exp, err := ev.Slots()
	if err != nil {
		return nil, nil, solver.Dominance{}, fmt.Errorf("expand slots: %w", err)
	}

	ps, err := s.store.Participants(r.Context(), ev.ID)
	if err != nil {
		return nil, nil, solver.Dominance{}, err
	}

	rs, err := s.store.ResponsesForEvent(r.Context(), ev.ID)
	if err != nil {
		return nil, nil, solver.Dominance{}, err
	}

	in := results.Build(ps, rs, exp.Starts)
	cfg := solver.DefaultConfig()

	// A group's scheduling habit only ever breaks a near-tie, and only for a
	// group-owned event -- a one-off link has no history to consult. Dominance
	// is left on the plain scores regardless: whether an outcome is certain is
	// a mathematical question, and a habit has no bearing on certainty.
	var hist solver.HistoryAffinity
	if ev.GroupID != nil {
		loc, err := tz.Load(ev.OrganizerTZ)
		if err != nil {
			loc = time.UTC
		}
		decisions, err := s.store.GroupDecisionHistory(r.Context(), *ev.GroupID)
		if err != nil {
			s.log.ErrorContext(r.Context(), "load group history", "err", err)
		} else {
			hist = solver.AffinityFromDecisions(decisions, loc, historyHalfLife, time.Now())
		}
	}

	return solver.RankWithHistory(in.Slots, in.Participants, in.Responses, cfg, hist),
		ps,
		solver.Analyze(in.Slots, in.Participants, in.Responses, cfg),
		nil
}

// historyHalfLife is the 30-day decay the PRD specifies. Without decay a
// group ossifies on whatever it picked a year ago, silently suppressing the
// exact preference drift -- a new class, a teammate's timezone change -- the
// feature is meant to surface instead.
const historyHalfLife = 30 * 24 * time.Hour

func toRankedSlot(sc solver.SlotScore, total int) rankedSlot {
	return rankedSlot{
		SlotStart:    sc.Start.UTC(),
		Coverage:     sc.Coverage,
		Total:        total,
		Eliminated:   sc.Eliminated,
		EliminatedBy: sc.EliminatedBy,
		Excludes:     sc.Excludes,
		Unknown:      sc.Unknown,
		Unsociable:   sc.Penalty > 0,
	}
}

// --- decide ------------------------------------------------------------------

type decideRequest struct {
	SlotStart time.Time `json:"slot_start"`

	// Force locks a slot a required participant has ruled out. It is off by
	// default because doing so silently is exactly the confident wrongness this
	// product exists to avoid, and present at all because the organizer may
	// know something the tool does not.
	Force bool `json:"force"`
}

func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	a := mustAuthed(r)

	if !a.participant.IsOrganizer {
		// 403 rather than 401: the caller is who they say they are, they just
		// are not allowed to do this.
		s.writeError(w, r, http.StatusForbidden, "only the organizer can decide this event")
		return
	}

	var req decideRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}

	ranked, _, err := s.rank(r, a.event)
	if err != nil {
		s.log.ErrorContext(r.Context(), "decide: rank", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not compute results")
		return
	}

	var chosen *solver.SlotScore
	for i := range ranked {
		if ranked[i].Start.Equal(req.SlotStart) {
			chosen = &ranked[i]
			break
		}
	}
	if chosen == nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf(
			"%s is not a slot of this event", req.SlotStart.UTC().Format(time.RFC3339)))
		return
	}

	if chosen.Eliminated && !req.Force {
		s.writeError(w, r, http.StatusConflict, fmt.Sprintf(
			"%s is ruled out by %s. Send force to lock it anyway.",
			req.SlotStart.UTC().Format(time.RFC3339), strings.Join(chosen.EliminatedBy, ", ")))
		return
	}

	updated, err := s.store.Decide(r.Context(), a.event.ID, chosen.Start)
	if err != nil {
		s.log.ErrorContext(r.Context(), "decide", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not lock the slot")
		return
	}

	s.publish(a.event.ID, sse.EventDecided)
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"slug":               updated.Slug,
		"status":             updated.Status,
		"decided_slot_start": updated.DecidedSlotStart,
	})
}

func (s *Server) handleReopen(w http.ResponseWriter, r *http.Request) {
	a := mustAuthed(r)

	if !a.participant.IsOrganizer {
		s.writeError(w, r, http.StatusForbidden, "only the organizer can reopen this event")
		return
	}

	updated, err := s.store.Reopen(r.Context(), a.event.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "reopen", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not reopen the event")
		return
	}

	s.publish(a.event.ID, sse.EventReopened)
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"slug":   updated.Slug,
		"status": updated.Status,
	})
}

// --- calendar download -------------------------------------------------------

func (s *Server) handleDecidedICS(w http.ResponseWriter, r *http.Request) {
	ev, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.eventLookupError(w, r, err)
		return
	}

	if ev.Status != "decided" || ev.DecidedSlotStart == nil {
		s.writeError(w, r, http.StatusConflict, "this event has not been decided yet")
		return
	}

	ps, err := s.store.Participants(r.Context(), ev.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "ics: participants", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not load participants")
		return
	}

	start := *ev.DecidedSlotStart
	body := ics.Render(ics.Event{
		// Stable across re-downloads and across a changed decision, so a
		// calendar client updates the meeting in place instead of leaving the
		// old time behind alongside the new one.
		UID:         ev.Slug + "@overlap",
		Summary:     ev.Title,
		Description: attendeeLine(ps),
		Start:       start,
		End:         start.Add(time.Duration(ev.SlotMinutes) * time.Minute),
		URL:         s.cfg.WebURL + "/e/" + ev.Slug,
	}, time.Now())

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+ics.Filename(ev.Slug)+`"`)
	// The file changes whenever the decision or the roster does, and it is
	// small, so revalidating beats serving a stale invite.
	w.Header().Set("Cache-Control", "no-store")

	if _, err := w.Write([]byte(body)); err != nil {
		s.log.DebugContext(r.Context(), "write ics", "err", err)
	}
}

// attendeeLine lists who is expected, for the calendar entry's description.
//
// Names only. Overlap holds no attendee email addresses, and inventing ATTENDEE
// properties without them would produce an invitation clients cannot act on.
func attendeeLine(ps []store.Participant) string {
	if len(ps) == 0 {
		return ""
	}
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.DisplayName)
	}
	return "With: " + strings.Join(names, ", ")
}
