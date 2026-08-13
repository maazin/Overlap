package fetchguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIsPublicRejectsEverythingSpecial is the core of the defence. Each of
// these has been used to reach something a public API was never meant to
// reach.
func TestIsPublicRejectsEverythingSpecial(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		want bool
		why  string
	}{
		{"8.8.8.8", true, "ordinary public v4"},
		{"2606:4700:4700::1111", true, "ordinary public v6"},

		{"127.0.0.1", false, "loopback"},
		{"::1", false, "v6 loopback"},
		{"0.0.0.0", false, "unspecified"},
		{"10.1.2.3", false, "private"},
		{"172.16.0.1", false, "private"},
		{"192.168.1.1", false, "private"},
		{"169.254.169.254", false, "cloud metadata, the classic SSRF target"},
		{"fe80::1", false, "v6 link local"},
		{"fd00::1", false, "v6 unique local"},
		{"100.64.0.1", false, "carrier NAT and Tailscale space"},
		{"224.0.0.1", false, "multicast"},
		{"255.255.255.255", false, "broadcast"},
		{"192.0.2.5", false, "TEST-NET-1"},
		{"198.18.0.1", false, "benchmarking range"},

		// The v6 costume for a v4 address. Checking only the v6 predicates
		// would wave this straight through to localhost.
		{"::ffff:127.0.0.1", false, "v4-mapped loopback"},
		{"::ffff:169.254.169.254", false, "v4-mapped metadata address"},
		{"::ffff:10.0.0.1", false, "v4-mapped private"},
	} {
		t.Run(tc.ip, func(t *testing.T) {
			if got := IsPublic(net.ParseIP(tc.ip)); got != tc.want {
				t.Fatalf("IsPublic(%s) = %v, want %v (%s)", tc.ip, got, tc.want, tc.why)
			}
		})
	}

	if IsPublic(nil) {
		t.Fatal("a nil address is not public")
	}
}

func TestCheckURLRejectsBadSchemes(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"ftp://example.com/cal.ics",
		"gopher://example.com/",
		"data:text/calendar,BEGIN:VCALENDAR",
	} {
		t.Run(raw, func(t *testing.T) {
			u, err := Normalize(raw)
			if err != nil {
				return // rejected at parse, equally fine
			}
			if err := CheckURL(u); !errors.Is(err, ErrBlocked) {
				t.Fatalf("CheckURL(%q) = %v, want ErrBlocked", raw, err)
			}
		})
	}
}

func TestCheckURLRejectsLiteralPrivateAddresses(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:5432/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/cal.ics",
		"http://10.0.0.5/cal.ics",
		"http://[::ffff:127.0.0.1]/cal.ics",
	} {
		t.Run(raw, func(t *testing.T) {
			u, err := Normalize(raw)
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if err := CheckURL(u); !errors.Is(err, ErrBlocked) {
				t.Fatalf("CheckURL(%q) = %v, want ErrBlocked", raw, err)
			}
		})
	}
}

// TestCheckURLRejectsEmbeddedCredentials: those would be handed to whatever the
// URL redirects to.
func TestCheckURLRejectsEmbeddedCredentials(t *testing.T) {
	u, _ := Normalize("https://user:pass@example.com/cal.ics")
	if err := CheckURL(u); !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v, want ErrBlocked", err)
	}
}

// TestNormalizeAcceptsWebcal is the scheme behind every "subscribe" button in
// Apple Calendar and Outlook.
func TestNormalizeAcceptsWebcal(t *testing.T) {
	u, err := Normalize("webcal://p1.calendar.example.com/feed.ics")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if u.Scheme != "https" {
		t.Fatalf("scheme = %q, want https", u.Scheme)
	}
	if err := CheckURL(u); err != nil {
		t.Fatalf("CheckURL: %v", err)
	}
}

func TestNormalizeRejectsJunk(t *testing.T) {
	for _, raw := range []string{"", "   ", "not a url at all", "https://"} {
		if u, err := Normalize(raw); err == nil {
			if err := CheckURL(u); err == nil {
				t.Fatalf("%q should not be accepted", raw)
			}
		}
	}
}

// --- fetching -----------------------------------------------------------------

// testFetcher points every hostname at the address of a local test server,
// while leaving the public/private checks fully in force. That is what lets us
// exercise real HTTP without either reaching the internet or weakening the
// thing under test.
func testFetcher(t *testing.T, allowLoopback bool) (*Fetcher, *httptest.Server) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Write(make([]byte, MaxBytes+100))
		case "/notfound":
			w.WriteHeader(http.StatusNotFound)
		case "/redirect-private":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "text/calendar")
			w.Write([]byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"))
		}
	}))
	t.Cleanup(srv.Close)

	host, _, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	f := New()
	f.resolve = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP(host)}, nil
	}
	if allowLoopback {
		// Only the HTTP mechanics are under test here -- size limits, status
		// codes, redirect handling -- so the address policy is relaxed to let
		// the request reach a local server. The policy itself is covered by
		// the IsPublic and CheckURL tests above, which use the real one.
		f.allow = func(net.IP) bool { return true }
	}
	return f, srv
}

// TestGetRejectsHostResolvingToPrivate is the DNS half of the defence: the URL
// looks fine, and the name resolves somewhere it must not.
func TestGetRejectsHostResolvingToPrivate(t *testing.T) {
	f := New()
	f.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}

	_, err := f.Get(t.Context(), "https://totally-innocent.example.com/cal.ics")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v, want ErrBlocked", err)
	}
}

// TestGetRejectsWhenAnyAddressIsPrivate covers a host that returns a public and
// a private address together. Accepting it because one was fine would leave the
// choice of which to dial up to chance.
func TestGetRejectsWhenAnyAddressIsPrivate(t *testing.T) {
	f := New()
	f.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.1")}, nil
	}

	_, err := f.Get(t.Context(), "https://mixed.example.com/cal.ics")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v, want ErrBlocked", err)
	}
}

func TestGetReadsABody(t *testing.T) {
	f, srv := testFetcher(t, true)

	body, err := f.Get(t.Context(), srv.URL+"/cal.ics")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.HasPrefix(string(body), "BEGIN:VCALENDAR") {
		t.Fatalf("body = %q", body)
	}
}

func TestGetRefusesAnOversizedBody(t *testing.T) {
	f, srv := testFetcher(t, true)

	if _, err := f.Get(t.Context(), srv.URL+"/big"); err == nil {
		t.Fatal("want an error for a body over the limit")
	}
}

func TestGetSurfacesHTTPErrors(t *testing.T) {
	f, srv := testFetcher(t, true)

	_, err := f.Get(t.Context(), srv.URL+"/notfound")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("got %v, want the 404 reported", err)
	}
}

// TestRedirectIntoPrivateSpaceIsBlocked: a redirect is a fresh URL from an
// untrusted source, and checking only the first one is how this defence is
// usually got around.
func TestRedirectIntoPrivateSpaceIsBlocked(t *testing.T) {
	f, srv := testFetcher(t, true)
	// The real policy is restored for the redirect target specifically: the
	// first hop must be reachable, the second must not be.
	f.allow = func(ip net.IP) bool {
		return ip.IsLoopback() || IsPublic(ip)
	}

	if _, err := f.Get(t.Context(), srv.URL+"/redirect-private"); err == nil {
		t.Fatal("a redirect to a link-local address must be refused")
	}
}
