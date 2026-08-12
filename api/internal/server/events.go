package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maazinshaikh/overlap/api/internal/slots"
	"github.com/maazinshaikh/overlap/api/internal/store"
	"github.com/maazinshaikh/overlap/api/internal/tz"
)

// maxBodyBytes caps a request body. Event creation is a handful of short
// fields, so anything larger is a mistake or an attack, and either way reading
// it into memory serves nobody.
const maxBodyBytes = 16 << 10

const maxTitleRunes = 120

type createEventRequest struct {
	Title       string `json:"title"`
	Timezone    string `json:"timezone"`
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	DayStart    string `json:"day_start"`
	DayEnd      string `json:"day_end"`
	SlotMinutes int    `json:"slot_minutes"`
}

type createEventResponse struct {
	Slug string `json:"slug"`
}

// dstNote explains a wall clock that did not map cleanly onto the timeline, so
// the organizer can be told rather than quietly given a different schedule than
// the one they asked for.
type dstNote struct {
	Date      string `json:"date"`
	LocalTime string `json:"local_time"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail"`
}

type eventResponse struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Timezone    string `json:"timezone"`
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	DayStart    string `json:"day_start"`
	DayEnd      string `json:"day_end"`
	SlotMinutes int    `json:"slot_minutes"`
	Status      string `json:"status"`

	// Slots are absolute instants in UTC. The client renders them in the
	// viewer's own zone; the server never guesses what that is.
	Slots []time.Time `json:"slots"`

	DSTNotes  []dstNote `json:"dst_notes,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req createEventRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}

	in, err := req.toNewEvent()
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// Expanding before inserting means an event that cannot produce slots is
	// rejected outright instead of being stored and failing on every read.
	if _, err := (store.Event{
		OrganizerTZ: in.OrganizerTZ,
		WindowStart: in.WindowStart, WindowEnd: in.WindowEnd,
		DayStart: in.DayStart, DayEnd: in.DayEnd,
		SlotMinutes: in.SlotMinutes,
	}).Slots(); err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	ev, err := s.store.CreateEvent(r.Context(), in)
	if err != nil {
		s.log.ErrorContext(r.Context(), "create event", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not create event")
		return
	}

	w.Header().Set("Location", "/api/events/"+ev.Slug)
	s.writeJSON(w, r, http.StatusCreated, createEventResponse{Slug: ev.Slug})
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	ev, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, "no such event")
		return
	}
	if err != nil {
		s.log.ErrorContext(r.Context(), "get event", "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "could not load event")
		return
	}

	exp, err := ev.Slots()
	if err != nil {
		// The row is already stored, so this is a server-side inconsistency
		// rather than a client error.
		s.log.ErrorContext(r.Context(), "expand slots", "err", err, "slug", ev.Slug)
		s.writeError(w, r, http.StatusInternalServerError, "could not expand slots")
		return
	}

	out := eventResponse{
		Slug:        ev.Slug,
		Title:       ev.Title,
		Timezone:    ev.OrganizerTZ,
		WindowStart: ev.WindowStart.String(),
		WindowEnd:   ev.WindowEnd.String(),
		DayStart:    ev.DayStart.String(),
		DayEnd:      ev.DayEnd.String(),
		SlotMinutes: ev.SlotMinutes,
		Status:      ev.Status,
		Slots:       make([]time.Time, len(exp.Starts)),
		DSTNotes:    dstNotes(exp),
		ExpiresAt:   ev.ExpiresAt.UTC(),
	}
	for i, t := range exp.Starts {
		out.Slots[i] = t.UTC()
	}

	s.writeJSON(w, r, http.StatusOK, out)
}

func dstNotes(exp slots.Expansion) []dstNote {
	notes := make([]dstNote, 0, len(exp.Skipped)+len(exp.Ambiguous))
	for _, a := range exp.Skipped {
		notes = append(notes, dstNote{
			Date: a.Date.String(), LocalTime: a.Local.String(), Reason: "nonexistent",
			Detail: "the clock skipped this time when it moved forward, so no slot was created",
		})
	}
	for _, a := range exp.Ambiguous {
		notes = append(notes, dstNote{
			Date: a.Date.String(), LocalTime: a.Local.String(), Reason: "ambiguous",
			Detail: "the clock read this time twice; the slot uses the first occurrence",
		})
	}
	if len(notes) == 0 {
		return nil
	}
	return notes
}

// --- request parsing ---------------------------------------------------------

func (req createEventRequest) toNewEvent() (store.NewEvent, error) {
	var out store.NewEvent

	out.Title = strings.TrimSpace(req.Title)
	if out.Title == "" {
		return out, errors.New("title is required")
	}
	if utf8.RuneCountInString(out.Title) > maxTitleRunes {
		return out, fmt.Errorf("title must be at most %d characters", maxTitleRunes)
	}

	if !tz.Valid(req.Timezone) {
		return out, fmt.Errorf("timezone %q is not a known IANA zone name", req.Timezone)
	}
	out.OrganizerTZ = req.Timezone

	var err error
	if out.WindowStart, err = parseDate(req.WindowStart); err != nil {
		return out, fmt.Errorf("window_start: %w", err)
	}
	if out.WindowEnd, err = parseDate(req.WindowEnd); err != nil {
		return out, fmt.Errorf("window_end: %w", err)
	}

	// Defaults match the column defaults, so an omitted field and an absent
	// column can never disagree.
	if out.DayStart, err = parseTimeOfDayOr(req.DayStart, slots.TimeOfDay{Hour: 9}); err != nil {
		return out, fmt.Errorf("day_start: %w", err)
	}
	if out.DayEnd, err = parseTimeOfDayOr(req.DayEnd, slots.TimeOfDay{Hour: 17}); err != nil {
		return out, fmt.Errorf("day_end: %w", err)
	}

	out.SlotMinutes = req.SlotMinutes
	if out.SlotMinutes == 0 {
		out.SlotMinutes = 30
	}
	// Mirrors the events_slot_minutes check constraint. Duplicated on purpose:
	// the constraint is the guarantee, this is the readable error message.
	if out.SlotMinutes < 5 || out.SlotMinutes > 480 {
		return out, errors.New("slot_minutes must be between 5 and 480")
	}

	return out, nil
}

func parseDate(s string) (slots.Date, error) {
	if s == "" {
		return slots.Date{}, errors.New("required, expected YYYY-MM-DD")
	}
	// time.Parse rejects impossible dates such as 2026-02-30 rather than
	// rolling them over, which is what we want here.
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return slots.Date{}, fmt.Errorf("expected YYYY-MM-DD, got %q", s)
	}
	return slots.Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}

// parseTimeOfDayOr parses HH:MM, falling back to def when the field is absent.
//
// It is hand-rolled rather than using time.Parse because 24:00 is a legal end
// bound for a day band, and time.Parse rejects hour 24.
func parseTimeOfDayOr(s string, def slots.TimeOfDay) (slots.TimeOfDay, error) {
	if s == "" {
		return def, nil
	}

	hh, mm, ok := strings.Cut(s, ":")
	if !ok {
		return slots.TimeOfDay{}, fmt.Errorf("expected HH:MM, got %q", s)
	}
	h, err := strconv.Atoi(hh)
	if err != nil {
		return slots.TimeOfDay{}, fmt.Errorf("expected HH:MM, got %q", s)
	}
	m, err := strconv.Atoi(mm)
	if err != nil {
		return slots.TimeOfDay{}, fmt.Errorf("expected HH:MM, got %q", s)
	}

	switch {
	case h < 0 || h > 24:
		return slots.TimeOfDay{}, fmt.Errorf("hour out of range in %q", s)
	case m < 0 || m > 59:
		return slots.TimeOfDay{}, fmt.Errorf("minute out of range in %q", s)
	case h == 24 && m != 0:
		return slots.TimeOfDay{}, fmt.Errorf("24:00 is the latest end of day, got %q", s)
	}
	return slots.TimeOfDay{Hour: h, Minute: m}, nil
}
