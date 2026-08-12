package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/maazinshaikh/overlap/api/internal/ics"
	"github.com/maazinshaikh/overlap/api/internal/results"
	"github.com/maazinshaikh/overlap/api/internal/solver"
	"github.com/maazinshaikh/overlap/api/internal/store"
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

	DecidedSlotStart *time.Time `json:"decided_slot_start,omitempty"`
}

func (s *Server) handleSolve(w http.ResponseWriter, r *http.Request) {
	ev, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.eventLookupError(w, r, err)
		return
	}

	ranked, ps, err := s.rank(r, ev)
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
		DecidedSlotStart: ev.DecidedSlotStart,
	}
	for _, sc := range ranked {
		out.Ranked = append(out.Ranked, toRankedSlot(sc, len(ps)))
	}

	s.writeJSON(w, r, http.StatusOK, out)
}

// rank loads everything an event's results depend on and scores it.
func (s *Server) rank(r *http.Request, ev store.Event) ([]solver.SlotScore, []store.Participant, error) {
	exp, err := ev.Slots()
	if err != nil {
		return nil, nil, fmt.Errorf("expand slots: %w", err)
	}

	ps, err := s.store.Participants(r.Context(), ev.ID)
	if err != nil {
		return nil, nil, err
	}

	rs, err := s.store.ResponsesForEvent(r.Context(), ev.ID)
	if err != nil {
		return nil, nil, err
	}

	in := results.Build(ps, rs, exp.Starts)
	return solver.Rank(in.Slots, in.Participants, in.Responses, solver.DefaultConfig()), ps, nil
}

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
