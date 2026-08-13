// Package fetchguard fetches a URL supplied by an untrusted caller.
//
// Letting a server fetch an arbitrary URL hands the caller the server's network
// position. Anything the API can reach, the caller can now reach: Postgres on a
// private address, the Fly.io internal network, a cloud metadata endpoint that
// hands out credentials to whoever asks. That is server-side request forgery,
// and it is the entire reason this package exists rather than a bare
// http.Get.
//
// The defence is to resolve the host ourselves, check every address it resolves
// to, and then dial the address we checked. Checking the hostname and letting
// the transport resolve it again is the classic DNS-rebinding hole: the name
// resolves to a public address for the check and a private one for the dial.
package fetchguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Limits on what a fetch may cost us.
const (
	// MaxBytes caps the response body. A calendar feed for one person is
	// kilobytes; anything vastly larger is a mistake or an attempt to exhaust
	// memory.
	MaxBytes = 5 << 20

	// Timeout bounds the whole request including redirects.
	Timeout = 10 * time.Second

	// MaxRedirects stops a redirect loop. Each hop is re-checked, so a redirect
	// cannot be used to escape into private space.
	MaxRedirects = 3
)

// ErrBlocked is returned when a URL resolves somewhere it must not.
var ErrBlocked = errors.New("fetchguard: destination not allowed")

// Fetcher retrieves untrusted URLs.
//
// The zero value is not usable; call New.
type Fetcher struct {
	client *http.Client

	// resolve is swappable so tests can exercise the address checks without
	// depending on what DNS happens to return.
	resolve func(ctx context.Context, host string) ([]net.IP, error)

	// allow is the address policy, defaulting to IsPublic. It is a field only
	// so that tests covering HTTP mechanics -- size limits, status codes,
	// redirects -- can reach a local server without the policy tests losing
	// their teeth. Production never replaces it.
	allow func(net.IP) bool
}

// New returns a Fetcher with a hardened transport.
func New() *Fetcher {
	f := &Fetcher{
		resolve: func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		},
		allow: IsPublic,
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}

	f.client = &http.Client{
		Timeout: Timeout,
		Transport: &http.Transport{
			// Every dial is re-checked here as well as before the request.
			// net/http resolves the host itself, so without this hook a name
			// that passed the pre-flight check could still be dialled at a
			// different address a moment later.
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := f.resolve(ctx, host)
				if err != nil {
					return nil, fmt.Errorf("%w: cannot resolve %q", ErrBlocked, host)
				}
				for _, ip := range ips {
					if !f.allow(ip) {
						return nil, fmt.Errorf("%w: %s resolves to %s", ErrBlocked, host, ip)
					}
				}
				// Dial the address we just checked rather than the name, so no
				// second resolution can return something different.
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
			},
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("fetchguard: stopped after %d redirects", MaxRedirects)
			}
			// A redirect is a fresh URL from an untrusted source and gets the
			// same scrutiny as the original.
			return f.checkURL(req.URL)
		},
	}
	return f
}

// AllowPrivateForDevelopment relaxes the address policy to permit loopback and
// private ranges.
//
// It exists so that a developer can point calendar import at a feed served from
// their own machine, which the real policy correctly refuses. Nothing else
// should ever call it: with this on, the process will happily fetch anything it
// can route to, which is the entire vulnerability this package prevents.
//
// The caller is responsible for making sure this cannot be reached in a
// deployed environment. server.New gates it on the environment being
// development, so setting the flag in production has no effect.
func (f *Fetcher) AllowPrivateForDevelopment() {
	f.allow = func(net.IP) bool { return true }
}

// Get retrieves url, returning at most MaxBytes of body.
func (f *Fetcher) Get(ctx context.Context, raw string) ([]byte, error) {
	u, err := Normalize(raw)
	if err != nil {
		return nil, err
	}
	if err := f.checkURL(u); err != nil {
		return nil, err
	}

	// Pre-flight resolution. The dialler checks again; doing it here too turns
	// "your calendar host points at a private address" into a clear error
	// rather than an opaque dial failure.
	ips, err := f.resolve(ctx, u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("%w: cannot resolve %q", ErrBlocked, u.Hostname())
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: %q resolves to nothing", ErrBlocked, u.Hostname())
	}
	for _, ip := range ips {
		if !f.allow(ip) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrBlocked, u.Hostname(), ip)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/calendar, text/plain;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", "Overlap/1.0 (+https://github.com/maazin/Overlap)")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetchguard: server returned %s", resp.Status)
	}

	// MaxBytes+1 so a body sitting exactly on the limit is distinguishable
	// from one that overruns it.
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBytes {
		return nil, fmt.Errorf("fetchguard: response larger than %d bytes", MaxBytes)
	}
	return body, nil
}

// Normalize parses a user-supplied URL, accepting the webcal scheme calendar
// apps hand out and rewriting it to https.
func Normalize(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("fetchguard: empty URL")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("fetchguard: not a valid URL")
	}

	// "webcal://" is what Apple and Outlook put behind a subscribe button. It
	// is https in every respect except the label.
	if strings.EqualFold(u.Scheme, "webcal") {
		u.Scheme = "https"
	}
	return u, nil
}

// checkURL is CheckURL under this Fetcher's address policy.
func (f *Fetcher) checkURL(u *url.URL) error {
	if err := checkURLShape(u); err != nil {
		return err
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !f.allow(ip) {
		return fmt.Errorf("%w: %s is not a public address", ErrBlocked, ip)
	}
	return nil
}

// CheckURL rejects anything about a URL that makes it unsafe to fetch, before
// any name resolution happens.
func CheckURL(u *url.URL) error {
	if err := checkURLShape(u); err != nil {
		return err
	}
	// A literal IP skips DNS entirely, so check it here as well.
	if ip := net.ParseIP(u.Hostname()); ip != nil && !IsPublic(ip) {
		return fmt.Errorf("%w: %s is not a public address", ErrBlocked, ip)
	}
	return nil
}

// checkURLShape covers everything decidable from the URL alone.
func checkURLShape(u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		// file:, gopher:, ftp: and friends are all ways to read something that
		// is not a web page.
		return fmt.Errorf("%w: scheme %q is not http or https", ErrBlocked, u.Scheme)
	}

	if u.Hostname() == "" {
		return fmt.Errorf("%w: no host", ErrBlocked)
	}

	// Credentials in the URL would be forwarded to whatever it redirects to.
	if u.User != nil {
		return fmt.Errorf("%w: URLs with embedded credentials are not accepted", ErrBlocked)
	}
	return nil
}

// IsPublic reports whether an address is one we are willing to talk to.
//
// The list is deny-by-default in spirit: everything with a special meaning is
// excluded, and what remains is the ordinary public internet.
func IsPublic(ip net.IP) bool {
	if ip == nil {
		return false
	}

	// Map IPv4-in-IPv6 back to v4 so ::ffff:127.0.0.1 cannot slip through the
	// v4 checks by wearing a v6 costume.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}

	for _, block := range blockedNets {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

// blockedNets are ranges Go's own predicates do not cover but which are still
// not the public internet.
var blockedNets = func() []*net.IPNet {
	cidrs := []string{
		"100.64.0.0/10",   // carrier-grade NAT, and Tailscale
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"240.0.0.0/4",     // reserved
		"255.255.255.255/32",
		"::/128",       // unspecified
		"64:ff9b::/96", // NAT64, which can be pointed at private v4 space
		"100::/64",     // discard-only
		"2001:db8::/32",
		"fc00::/7", // unique local, belt and braces alongside IsPrivate
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()
