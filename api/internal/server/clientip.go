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
			// X-Forwarded-For style lists: the leftmost entry is the original
			// client. Trustworthy only because the configured proxy is what
			// wrote the list.
			if comma := strings.IndexByte(v, ','); comma >= 0 {
				v = strings.TrimSpace(v[:comma])
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
