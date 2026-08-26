package server

import (
	"net"
	"net/http"
	"strings"
)

// clientIP resolves the address a rate limit should be keyed on.
//
// Behind a proxy, r.RemoteAddr is the proxy, so keying on it puts every user on
// Earth into one bucket. The symptom is a limiter that passes every test on a
// laptop and either locks out all traffic or limits nobody once deployed.
//
// Which headers to trust is configuration rather than a guess. ClientIPHeaders
// is empty by default, so a server reachable directly uses the socket address
// and a client cannot promote itself to a fresh bucket by inventing a header.
//
// It is a list because a platform usually offers more than one and not every
// request carries all of them. The first header actually present wins, so the
// order expresses preference: name the single-value headers a proxy overwrites
// first, and leave list-shaped ones like X-Forwarded-For last or out entirely.
func (s *Server) clientIP(r *http.Request) string {
	for _, h := range s.cfg.ClientIPHeaders {
		v := strings.TrimSpace(r.Header.Get(h))
		if v == "" {
			continue
		}

		// The rightmost entry, not the leftmost.
		//
		// This looks backwards and is the point. A proxy appends the address it
		// received the request from, so on a list-shaped header the last entry
		// is the one our own proxy wrote and everything left of it came from
		// whoever called it. A client sending "X-Forwarded-For: 1.2.3.4" gets
		// "1.2.3.4, <their real address>", so reading from the left hands every
		// caller a bucket of their own choosing.
		//
		// For a single-value header the platform overwrites, which is what this
		// list should mostly contain, the rightmost entry is the only entry.
		if comma := strings.LastIndexByte(v, ','); comma >= 0 {
			v = strings.TrimSpace(v[comma+1:])
		}

		// A value that is not an address is not a key worth trusting. Behind
		// two proxies the last X-Forwarded-For entry can be an internal hop
		// that changes per request, which silently gives every request its own
		// bucket and disables limiting altogether. This does not catch that,
		// but it does stop obvious garbage from becoming a bucket.
		if net.ParseIP(v) == nil {
			continue
		}
		return v
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port to strip. Whatever it is, it is still a stable key.
		return r.RemoteAddr
	}
	return host
}
