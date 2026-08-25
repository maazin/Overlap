package server

import (
	"net/http"
	"strconv"
	"time"
)

// rateLimit refuses a caller who is asking too often, keyed on their address.
//
// Applied to the endpoints that both write rows and require no token to reach.
// Everything else is either a read or already gated by a token the caller had
// to be given.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.limiter == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Preflight carries no body and creates nothing. Counting it would
		// spend a browser's budget twice for one real request.
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		ok, wait := s.limiter.Allow(s.clientIP(r))
		if !ok {
			// Rounded up: a Retry-After of 0 invites an immediate retry that
			// is guaranteed to fail.
			seconds := int(wait/time.Second) + 1
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			s.writeError(w, r, http.StatusTooManyRequests, "too many requests, try again shortly")
			return
		}

		next.ServeHTTP(w, r)
	})
}
