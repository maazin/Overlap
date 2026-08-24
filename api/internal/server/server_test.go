package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testServer builds a Server with no store. Everything it exercises — health,
// CORS, routing, panic containment — runs before any handler touches Postgres,
// so the nil is load-bearing rather than lazy: these tests would still pass
// with the database switched off.
func testServer() *Server {
	return New(Config{
		Env:            "test",
		AllowedOrigins: []string{"http://localhost:5173"},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

func TestHealthReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer().Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf(`status field = %q, want "ok"`, body["status"])
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer().Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestWrongMethodIs405(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer().Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/health", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// TestCORSOnlyEchoesAllowedOrigins guards the rule that matters once a
// participant token is a header: an attacker-controlled origin must never be
// reflected back.
func TestCORSOnlyEchoesAllowedOrigins(t *testing.T) {
	for _, tc := range []struct {
		name, origin, want string
	}{
		{"allowed", "http://localhost:5173", "http://localhost:5173"},
		{"denied", "https://evil.example", ""},
		{"absent", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			testServer().Routes().ServeHTTP(rec, req)

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
				t.Fatalf("Allow-Origin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPreflightShortCircuits(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	testServer().Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("preflight must advertise allowed methods")
	}
}

// TestCORSAllowsEveryTokenHeaderTheAPIRequires pins each custom header a
// handler actually reads against the CORS allowlist. A header a handler needs
// but CORS does not advertise fails silently from a browser: the preflight
// still returns 204, so nothing looks wrong until the real request is
// rejected client-side with no server log to point at, which is exactly how
// X-Member-Token shipped broken the first time.
func TestCORSAllowsEveryTokenHeaderTheAPIRequires(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	testServer().Routes().ServeHTTP(rec, req)

	allowed := rec.Header().Get("Access-Control-Allow-Headers")
	for _, want := range []string{TokenHeader, GroupTokenHeader} {
		if !strings.Contains(allowed, want) {
			t.Errorf("Access-Control-Allow-Headers = %q, missing %q", allowed, want)
		}
	}
}

// TestPanicIsContained pins the behaviour that one broken handler returns a 500
// instead of killing the process.
func TestPanicIsContained(t *testing.T) {
	s := testServer()
	h := s.recoverPanic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestRecorderForwardsFlush is the guard for Phase 5. If the middleware wrapper
// stops implementing http.Flusher, SSE buffers instead of streaming and the bug
// only shows up in a browser.
func TestRecorderForwardsFlush(t *testing.T) {
	var _ http.Flusher = (*statusRecorder)(nil)

	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	sr.Flush()

	if !rec.Flushed {
		t.Fatal("Flush must reach the underlying ResponseWriter")
	}
}
