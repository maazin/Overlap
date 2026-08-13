package server

import (
	"os"
	"strings"
	"time"
)

// Config is the process-level configuration, resolved once at startup.
//
// It is a plain struct with no behaviour so that tests can construct one
// directly instead of setting environment variables.
type Config struct {
	Addr           string
	Env            string
	DatabaseURL    string
	AllowedOrigins []string

	// AllowPrivateCalendarHosts lets calendar import reach loopback and private
	// addresses. Honoured only when Env is development; see server.New.
	AllowPrivateCalendarHosts bool

	// WebURL is where the SvelteKit app lives. The API needs it to write a
	// link back to the event into the downloaded calendar entry.
	WebURL string

	ShutdownTimeout time.Duration
}

// ConfigFromEnv reads configuration from the environment, applying defaults
// that make `go run ./cmd/api` work with no setup.
func ConfigFromEnv() Config {
	return Config{
		Addr:           ":" + env("PORT", "8080"),
		Env:            env("APP_ENV", "development"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AllowedOrigins: splitList(env("ALLOWED_ORIGINS", "http://localhost:5173")),
		WebURL:         strings.TrimRight(env("WEB_URL", "http://localhost:5173"), "/"),

		AllowPrivateCalendarHosts: env("ALLOW_PRIVATE_CALENDAR_HOSTS", "") == "true",
		ShutdownTimeout:           15 * time.Second,
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
