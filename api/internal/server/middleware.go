package server

import (
	"net/http"
	"slices"
	"time"
)

// statusRecorder captures the status code so logRequests can report it.
// http.ResponseWriter gives no way to read back what was written.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer when it supports flushing. Phase 5's
// SSE handler type-asserts for http.Flusher, and without this the assertion
// would fail against the wrapper and streaming would silently buffer.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		s.log.InfoContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// recoverPanic keeps one bad handler from taking down the process. Go's default
// is to kill the whole server on an unrecovered panic in a handler goroutine;
// for a public API that is the wrong trade.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				// ErrAbortHandler is how the stdlib signals a deliberate
				// abort (a hijacked or dead connection). Re-panicking lets
				// net/http handle it as intended rather than logging noise.
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.ErrorContext(r.Context(), "panic in handler",
					"panic", v, "method", r.Method, "path", r.URL.Path)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// cors allows the SvelteKit origin to call the API. The two are deployed to
// different hosts (Vercel and Fly.io), so this is load-bearing rather than
// boilerplate. Origins are an explicit allowlist; "*" is never echoed because
// Phase 2 sends a participant token header.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(s.cfg.AllowedOrigins, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Participant-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "600")
			// Responses vary by Origin, so a shared cache must not serve one
			// origin's response to another.
			w.Header().Add("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
