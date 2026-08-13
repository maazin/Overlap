package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/maazin/Overlap/api/internal/fetchguard"
	"github.com/maazin/Overlap/api/internal/icsparse"
	"github.com/maazin/Overlap/api/internal/solver"
	"github.com/maazin/Overlap/api/internal/store"
)

type connectICSRequest struct {
	URL string `json:"url"`
}

type connectICSResponse struct {
	Source string `json:"source"`

	// BusyBlocks and SlotsBlocked let the UI say what actually happened, which
	// is the difference between "connected" and "connected and it found the
	// three afternoons you are booked".
	BusyBlocks   int       `json:"busy_blocks"`
	SlotsBlocked int       `json:"slots_blocked"`
	FetchedAt    time.Time `json:"fetched_at"`
}

// handleConnectICS subscribes a participant to a calendar feed.
//
// This is the no-OAuth path, and it covers Apple and Outlook, both of which
// hand out a secret feed URL from their share menus. It exists partly so that
// calendar import is not gated on anybody's app-verification queue.
func (s *Server) handleConnectICS(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	a := mustAuthed(r)

	var req connectICSRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}

	exp, err := a.event.Slots()
	if err != nil {
		s.log.ErrorContext(r.Context(), "calendar: expand slots", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not expand slots")
		return
	}
	if len(exp.Starts) == 0 {
		s.writeError(w, r, http.StatusConflict, "this event has no slots to check against")
		return
	}

	body, err := s.fetcher.Get(r.Context(), req.URL)
	if err != nil {
		// A blocked destination is the caller's mistake and is safe to explain:
		// they supplied the URL, so naming what was wrong with it tells them
		// nothing they did not already provide.
		if errors.Is(err, fetchguard.ErrBlocked) {
			s.writeError(w, r, http.StatusBadRequest, err.Error())
			return
		}
		// Anything else is a fetch failure against a third party. Log it, but
		// keep the response generic rather than relaying remote errors.
		s.log.InfoContext(r.Context(), "calendar: fetch failed", "err", err)
		s.writeError(w, r, http.StatusBadGateway,
			"could not read that calendar feed. Check the URL is the secret iCal address, not the web page.")
		return
	}

	// Only the event's own window is expanded; a decade of calendar history is
	// of no use here and would be pointless to store.
	window := slotWindow(exp.Starts, a.event.SlotMinutes)
	blocks := icsparse.Parse(string(body), window.Start, window.End)

	if err := s.store.SaveBusyBlocks(
		r.Context(), a.participant.ID, blocks, store.CalendarICS, req.URL,
	); err != nil {
		s.log.ErrorContext(r.Context(), "calendar: save busy blocks", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not save your calendar")
		return
	}

	blocked, err := s.applyCalendarToResponses(r, a, exp.Starts, blocks)
	if err != nil {
		s.log.ErrorContext(r.Context(), "calendar: apply to responses", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not apply your calendar")
		return
	}

	s.publish(a.event.ID, "response_submitted")
	s.writeJSON(w, r, http.StatusOK, connectICSResponse{
		Source:       store.CalendarICS,
		BusyBlocks:   len(blocks),
		SlotsBlocked: blocked,
		FetchedAt:    time.Now().UTC(),
	})
}

// applyCalendarToResponses writes a "no" for every slot the calendar covers,
// leaving anything the person stated themselves untouched.
//
// The precedence rule is the whole point. A tier someone typed always beats one
// inferred from their calendar, because the calendar is evidence about where
// they are, not a statement about what they want. The inferred rows carry
// source 'calendar' so the UI can show them as a proposal to correct rather
// than as an answer already given.
func (s *Server) applyCalendarToResponses(
	r *http.Request, a authed, starts []time.Time, blocks []icsparse.Interval,
) (int, error) {
	existing, err := s.store.ResponsesForParticipant(r.Context(), a.participant.ID)
	if err != nil {
		return 0, err
	}

	stated := make(map[int64]store.Response, len(existing))
	for _, resp := range existing {
		if resp.Source != store.SourceCalendar {
			stated[resp.SlotStart.UnixNano()] = resp
		}
	}

	out := make([]store.Response, 0, len(starts))
	blocked := 0

	for _, slot := range starts {
		if prior, ok := stated[slot.UnixNano()]; ok {
			out = append(out, prior)
			continue
		}
		if coveredBy(slot, a.event.SlotMinutes, blocks) {
			out = append(out, store.Response{
				SlotStart: slot,
				Tier:      solver.TierNo,
				Source:    store.SourceCalendar,
			})
			blocked++
		}
	}

	return blocked, s.store.SaveResponses(r.Context(), a.participant.ID, out)
}

// coveredBy reports whether a slot collides with any busy interval.
func coveredBy(start time.Time, minutes int, blocks []icsparse.Interval) bool {
	slot := icsparse.Interval{Start: start, End: start.Add(time.Duration(minutes) * time.Minute)}
	for _, b := range blocks {
		if b.Overlaps(slot) {
			return true
		}
	}
	return false
}

// slotWindow is the span the event's slots occupy, which is the only period
// worth reading a calendar for.
func slotWindow(starts []time.Time, minutes int) icsparse.Interval {
	first, last := starts[0], starts[0]
	for _, s := range starts {
		if s.Before(first) {
			first = s
		}
		if s.After(last) {
			last = s
		}
	}
	return icsparse.Interval{
		Start: first,
		End:   last.Add(time.Duration(minutes) * time.Minute),
	}
}

// handleDisconnectCalendar drops imported blocks and the tiers derived from
// them, leaving everything the person stated themselves.
func (s *Server) handleDisconnectCalendar(w http.ResponseWriter, r *http.Request) {
	a := mustAuthed(r)

	existing, err := s.store.ResponsesForParticipant(r.Context(), a.participant.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "calendar: load responses", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not load your responses")
		return
	}

	kept := make([]store.Response, 0, len(existing))
	for _, resp := range existing {
		if resp.Source != store.SourceCalendar {
			kept = append(kept, resp)
		}
	}

	if err := s.store.SaveBusyBlocks(r.Context(), a.participant.ID, nil, store.CalendarNone, ""); err != nil {
		s.log.ErrorContext(r.Context(), "calendar: clear busy blocks", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not disconnect your calendar")
		return
	}
	if err := s.store.SaveResponses(r.Context(), a.participant.ID, kept); err != nil {
		s.log.ErrorContext(r.Context(), "calendar: rewrite responses", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not disconnect your calendar")
		return
	}

	s.publish(a.event.ID, "response_submitted")
	s.writeJSON(w, r, http.StatusOK, map[string]string{"source": store.CalendarNone})
}
