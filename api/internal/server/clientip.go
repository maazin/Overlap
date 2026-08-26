package server

import (
	"net"
	"net/http"
	"strings"
)

// clientIP resolves the address a rate limit should be keyed on.
//
// Behind Fly, r.RemoteAddr is the proxy, so keying on it puts every user on
// Earth into one bucket. The symptom is a limiter that passes every test on a
// laptop and locks out all traffic the moment it is deployed.
//
// Which header carries the truth is configuration rather than a guess.
// ClientIPHeader is empty by default, so a server reachable directly uses the
// socket address and a client cannot promote itself to a fresh bucket by
// inventing a header. fly.toml sets it to Fly-Client-IP, which is safe there
// precisely because Fly overwrites that header on every inbound request.
func (s *Server) clientIP(r *http.Request) string {
	if h := s.cfg.ClientIPHeader; h != "" {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			// The rightmost entry, not the leftmost.
			//
			// This looks backwards and is the whole point. A proxy appends the
			// address it received the request from, so on X-Forwarded-For the
			// last entry is the one our own proxy wrote and everything to its
			// left was supplied by whoever called it. A client that sends
			// "X-Forwarded-For: 1.2.3.4" gets "1.2.3.4, <their real address>",
			// so reading from the left hands every caller a rate limit bucket
			// of their own choosing, which is the same as having no limiter.
			//
			// For a single-value header the platform overwrites, such as
			// Fly-Client-IP, the rightmost entry is the only entry, so this is
			// correct there too.
			//
			// It assumes exactly one trusted proxy in front. Behind two, the
			// last entry is the inner proxy rather than the client, and the
			// symptom is everyone sharing a bucket again. Prefer a single-value
			// header the platform guarantees to overwrite wherever one exists.
			if comma := strings.LastIndexByte(v, ','); comma >= 0 {
				v = strings.TrimSpace(v[comma+1:])
			}
			return v
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port to strip. Whatever it is, it is still a stable key.
		return r.RemoteAddr
	}
	return host
}
