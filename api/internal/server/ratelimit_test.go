package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func limitedServer(burst int, headers ...string) *Server {
	return New(Config{
		Env:                "test",
		AllowedOrigins:     []string{"http://localhost:5173"},
		ClientIPHeaders:    headers,
		RateLimitBurst:     burst,
		RateLimitPerMinute: 60,
		RateLimitMaxKeys:   100,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

// createRequest is the cheapest limited endpoint to drive. The body is
// deliberately malformed: reaching the handler at all is the signal, and a 400
// proves the limiter let it through without needing a database.
func createRequest(from string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/events", strings.NewReader("{"))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = from + ":54321"
	return r
}

func TestRateLimitRefusesPastTheBurst(t *testing.T) {
	srv := limitedServer(3)
	routes := srv.Routes()

	for i := range 3 {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, createRequest("203.0.113.10"))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d of the burst was limited", i+1)
		}
	}

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, createRequest("203.0.113.10"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 once the burst is spent", rec.Code)
	}

	// Retry-After has to be a usable number. Zero would invite an immediate
	// retry that cannot succeed.
	after := rec.Header().Get("Retry-After")
	n, err := strconv.Atoi(after)
	if err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", after)
	}
}

func TestRateLimitIsPerAddress(t *testing.T) {
	srv := limitedServer(1)
	routes := srv.Routes()

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, createRequest("203.0.113.10"))
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("first address was limited on its first request")
	}

	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, createRequest("203.0.113.11"))
	if rec.Code == http.StatusTooManyRequests {
		t.Error("one address exhausting its budget must not limit another")
	}
}

// TestRateLimitUsesConfiguredHeader is the deployment-shaped case. Behind a
// proxy every request shares one RemoteAddr, so a limiter that ignores the
// header puts the whole internet in one bucket.
func TestRateLimitUsesConfiguredHeader(t *testing.T) {
	srv := limitedServer(1, "Fly-Client-IP")
	routes := srv.Routes()

	send := func(clientIP string) int {
		r := createRequest("10.0.0.1") // the proxy, identical every time
		r.Header.Set("Fly-Client-IP", clientIP)
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r)
		return rec.Code
	}

	if code := send("203.0.113.20"); code == http.StatusTooManyRequests {
		t.Fatal("first client limited immediately")
	}
	if code := send("203.0.113.21"); code == http.StatusTooManyRequests {
		t.Error("a second client behind the same proxy shared the first one's bucket")
	}
	if code := send("203.0.113.20"); code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 for the client that already spent its burst", code)
	}
}

// TestClientIPHeaderIgnoredWhenUnconfigured is the other half, and the reason
// the default is empty. If the header were always trusted, anyone could mint a
// fresh bucket per request by making one up.
func TestClientIPHeaderIgnoredWhenUnconfigured(t *testing.T) {
	srv := limitedServer(1)
	routes := srv.Routes()

	send := func(claimed string) int {
		r := createRequest("203.0.113.30")
		r.Header.Set("Fly-Client-IP", claimed)
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r)
		return rec.Code
	}

	if code := send("198.51.100.1"); code == http.StatusTooManyRequests {
		t.Fatal("first request limited immediately")
	}
	if code := send("198.51.100.2"); code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429; a spoofed header bought a fresh bucket", code)
	}
}

// TestPreflightIsNotRateLimited keeps a browser from spending its budget
// twice for one real request.
func TestPreflightIsNotRateLimited(t *testing.T) {
	srv := limitedServer(1)
	routes := srv.Routes()

	for range 5 {
		r := httptest.NewRequest(http.MethodOptions, "/api/events", nil)
		r.Header.Set("Origin", "http://localhost:5173")
		r.RemoteAddr = "203.0.113.40:1234"
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatal("a CORS preflight was rate limited")
		}
	}

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, createRequest("203.0.113.40"))
	if rec.Code == http.StatusTooManyRequests {
		t.Error("preflights consumed the budget meant for real requests")
	}
}

// TestReadsAreNotRateLimited pins the scope. Limiting reads would throttle
// people watching a poll they are already part of.
func TestReadsAreNotRateLimited(t *testing.T) {
	srv := limitedServer(1)
	routes := srv.Routes()

	for i := range 10 {
		r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		r.RemoteAddr = "203.0.113.50:1234"
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("read %d was rate limited", i+1)
		}
	}
}

// TestLimiterDisabledByDefaultInTests documents why every other test in this
// package is unaffected: a zero rate means no limiter at all.
func TestLimiterDisabledWhenRateIsZero(t *testing.T) {
	srv := testServer()
	if srv.Limiter() != nil {
		t.Error("a zero RateLimitPerMinute must leave the limiter nil")
	}
}

// TestForwardedForCannotBeSpoofed is the reason clientIP reads the rightmost
// entry. A proxy appends the address it saw, so anything to the left of the
// last entry was written by the caller. Reading from the left would let anyone
// mint a fresh bucket per request by prepending a value, which is the same as
// running with no limiter at all.
func TestForwardedForCannotBeSpoofed(t *testing.T) {
	srv := limitedServer(1, "X-Forwarded-For")
	routes := srv.Routes()

	// One real client, changing the part of the header they control.
	send := func(spoofed string) int {
		r := createRequest("10.0.0.1")
		r.Header.Set("X-Forwarded-For", spoofed+", 203.0.113.77")
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r)
		return rec.Code
	}

	if code := send("198.51.100.1"); code == http.StatusTooManyRequests {
		t.Fatal("first request limited immediately")
	}
	if code := send("198.51.100.2"); code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429; prepending a value bought a fresh bucket", code)
	}
}

// TestForwardedForReadsTheRealClient is the other half: two genuinely
// different clients behind the same proxy must not share a bucket.
func TestForwardedForReadsTheRealClient(t *testing.T) {
	srv := limitedServer(1, "X-Forwarded-For")
	routes := srv.Routes()

	send := func(client string) int {
		r := createRequest("10.0.0.1")
		r.Header.Set("X-Forwarded-For", client)
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r)
		return rec.Code
	}

	if code := send("203.0.113.80"); code == http.StatusTooManyRequests {
		t.Fatal("first client limited immediately")
	}
	if code := send("203.0.113.81"); code == http.StatusTooManyRequests {
		t.Error("a second client behind the same proxy shared the first one's bucket")
	}
}

// TestFirstPresentHeaderWins covers the deployment reality that a platform
// offers several address headers and not every request carries all of them.
func TestFirstPresentHeaderWins(t *testing.T) {
	srv := limitedServer(1, "True-Client-IP", "CF-Connecting-IP")
	routes := srv.Routes()

	send := func(set func(*http.Request)) int {
		r := createRequest("10.0.0.1")
		set(r)
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r)
		return rec.Code
	}

	// Preferred header present: it decides.
	if code := send(func(r *http.Request) {
		r.Header.Set("True-Client-IP", "203.0.113.90")
		r.Header.Set("CF-Connecting-IP", "198.51.100.90")
	}); code == http.StatusTooManyRequests {
		t.Fatal("first request limited immediately")
	}

	// Same True-Client-IP, different fallback. Still the same bucket, so the
	// fallback must not have been consulted.
	if code := send(func(r *http.Request) {
		r.Header.Set("True-Client-IP", "203.0.113.90")
		r.Header.Set("CF-Connecting-IP", "198.51.100.91")
	}); code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429; the preferred header did not decide the bucket", code)
	}

	// Preferred header absent: fall through to the next one rather than
	// silently dropping to the proxy's own address, which would put every
	// caller in one bucket.
	if code := send(func(r *http.Request) {
		r.Header.Set("CF-Connecting-IP", "198.51.100.92")
	}); code == http.StatusTooManyRequests {
		t.Error("a request with only the fallback header shared the other bucket")
	}
}

// TestNonAddressHeaderIsIgnored keeps obvious garbage from becoming a bucket.
// A header carrying something that is not an address is not evidence about who
// is calling.
func TestNonAddressHeaderIsIgnored(t *testing.T) {
	srv := limitedServer(1, "True-Client-IP")
	routes := srv.Routes()

	send := func(v string) int {
		r := createRequest("203.0.113.95")
		r.Header.Set("True-Client-IP", v)
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r)
		return rec.Code
	}

	if code := send("not-an-ip"); code == http.StatusTooManyRequests {
		t.Fatal("first request limited immediately")
	}
	// Both fell back to the same RemoteAddr, so the second must be limited.
	if code := send("also-not-an-ip"); code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429; garbage header values became distinct buckets", code)
	}
}
