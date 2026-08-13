package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/maazin/Overlap/api/internal/store"
)

// TokenHeader carries the opaque participant token. It is a header rather than
// a query parameter so it cannot end up in logs, referrers or a pasted URL.
const TokenHeader = "X-Participant-Token"

type ctxKey int

const (
	participantKey ctxKey = iota
	eventKey
)

// authed is the pair every token-guarded handler needs, resolved once.
type authed struct {
	event       store.Event
	participant store.Participant
}

// requireParticipant resolves the event from the path and the caller from the
// token header, rejecting anything that does not match.
//
// The token is looked up *within* the event, so a token minted for a different
// event is not a mismatch to detect but simply a row that does not exist. That
// makes cross-event reuse impossible to get wrong by forgetting a comparison,
// rather than merely wrong when someone remembers to write one.
func (s *Server) requireParticipant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.Header.Get(TokenHeader))
		if token == "" {
			s.writeError(w, r, http.StatusUnauthorized, "missing "+TokenHeader)
			return
		}

		ev, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusNotFound, "no such event")
			return
		}
		if err != nil {
			s.log.ErrorContext(r.Context(), "auth: load event", "err", err)
			s.writeError(w, r, http.StatusInternalServerError, "could not load event")
			return
		}

		p, err := s.store.ParticipantByToken(r.Context(), ev.ID, token)
		if errors.Is(err, store.ErrNotFound) {
			// Deliberately identical to the missing-token response. Saying
			// "that token exists but not here" would confirm a token is valid
			// somewhere, which is information the caller has not earned.
			s.writeError(w, r, http.StatusUnauthorized, "unrecognised participant token")
			return
		}
		if err != nil {
			s.log.ErrorContext(r.Context(), "auth: load participant", "err", err)
			s.writeError(w, r, http.StatusInternalServerError, "could not verify token")
			return
		}

		ctx := context.WithValue(r.Context(), eventKey, ev)
		ctx = context.WithValue(ctx, participantKey, p)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// mustAuthed reads what requireParticipant stored. It panics when the value is
// missing, because that means a handler was mounted without the middleware --
// a wiring bug that should fail immediately and loudly in development rather
// than silently serving an unauthenticated request.
func mustAuthed(r *http.Request) authed {
	ev, ok := r.Context().Value(eventKey).(store.Event)
	if !ok {
		panic("handler requires requireParticipant middleware: no event in context")
	}
	p, ok := r.Context().Value(participantKey).(store.Participant)
	if !ok {
		panic("handler requires requireParticipant middleware: no participant in context")
	}
	return authed{event: ev, participant: p}
}

// optionalParticipant resolves the caller when a token is present, and reports
// whether one was found. A missing or bad token is not an error: the event view
// is public, and an anonymous reader simply does not get a "you" section.
func (s *Server) optionalParticipant(r *http.Request, eventID string) (store.Participant, bool) {
	token := strings.TrimSpace(r.Header.Get(TokenHeader))
	if token == "" {
		return store.Participant{}, false
	}
	p, err := s.store.ParticipantByToken(r.Context(), eventID, token)
	if err != nil {
		return store.Participant{}, false
	}
	return p, true
}
