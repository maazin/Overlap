// Package server wires the Overlap HTTP API: configuration, middleware and
// routing. Handlers live here; business logic does not.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/maazinshaikh/overlap/api/internal/sse"
	"github.com/maazinshaikh/overlap/api/internal/store"
)

// Server holds everything a handler might need. Dependencies are injected
// rather than reached for through globals, so a test can build a Server with
// fakes and exercise Routes() through httptest.
type Server struct {
	cfg    Config
	log    *slog.Logger
	store  *store.Store
	broker *sse.Broker
}

// New returns a Server. It does no IO, so it cannot fail.
//
// The broker is per-process state rather than a package global, so two servers
// in one test binary do not broadcast into each other.
func New(cfg Config, log *slog.Logger, st *store.Store) *Server {
	return &Server{cfg: cfg, log: log, store: st, broker: sse.NewBroker()}
}

// Routes builds the handler tree. Middleware is applied outermost-first:
// recoverPanic wraps logRequests wraps cors wraps the mux, so a panic in a
// handler is still logged with its request line and still gets CORS headers.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Go 1.22+ pattern syntax: method and path in one string, with {slug}
	// wildcards read back via r.PathValue. Enough routing for the whole API;
	// chi arrives only if this stops being true.
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/events", s.handleCreateEvent)
	mux.HandleFunc("GET /api/events/{slug}", s.handleGetEvent)
	mux.HandleFunc("POST /api/events/{slug}/participants", s.handleJoin)
	mux.Handle("PUT /api/events/{slug}/responses",
		s.requireParticipant(http.HandlerFunc(s.handlePutResponses)))

	mux.HandleFunc("GET /api/events/{slug}/solve", s.handleSolve)
	mux.HandleFunc("GET /api/events/{slug}/stream", s.handleStream)
	mux.HandleFunc("GET /api/events/{slug}/decided.ics", s.handleDecidedICS)
	mux.Handle("POST /api/events/{slug}/decide",
		s.requireParticipant(http.HandlerFunc(s.handleDecide)))
	mux.Handle("POST /api/events/{slug}/reopen",
		s.requireParticipant(http.HandlerFunc(s.handleReopen)))

	return s.recoverPanic(s.logRequests(s.cors(mux)))
}

// writeError renders an error in the one shape every client can rely on.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.writeJSON(w, r, status, map[string]string{"error": msg})
}

// handleHealth is a liveness probe, not a readiness probe. It deliberately does
// not touch Postgres: a health check that fails on a transient database blip
// tells the platform to kill an otherwise healthy process, which turns a brief
// outage into a restart loop. Dependency checks belong on a separate endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, map[string]string{
		"status": "ok",
		"env":    s.cfg.Env,
	})
}

// writeJSON encodes v and writes it. Encoding happens into a buffer first so a
// mid-encode failure cannot emit a 200 with a truncated body.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		s.log.ErrorContext(r.Context(), "marshal response", "err", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		// The client hung up. Nothing to do but record it; the response is
		// already committed so we cannot change the status code.
		s.log.DebugContext(r.Context(), "write response", "err", err)
	}
}
