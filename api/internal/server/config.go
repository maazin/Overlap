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
	//
	// It has a working default so `go run ./cmd/api` needs no setup, which
	// also means forgetting it in production is silent: the .ics still
	// downloads and still imports, carrying a link to localhost. Deployment
	// docs call it out for that reason.
	WebURL string

	// RunMigrations applies pending migrations at startup. On by default,
	// because the alternative for this deployment shape is a schema nobody
	// migrated and a liveness probe too shallow to notice.
	RunMigrations bool

	// PurgeInterval is how often expired events are swept. Zero disables the
	// sweeper entirely.
	PurgeInterval time.Duration

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
		RunMigrations:             env("RUN_MIGRATIONS", "true") != "false",
		PurgeInterval:             duration("PURGE_INTERVAL", 6*time.Hour),
		ShutdownTimeout:           15 * time.Second,
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// duration reads a Go duration string, falling back rather than failing. A
// malformed value is logged nowhere and ignored here on purpose: this is a
// sweep cadence, and refusing to boot over it would trade a small
// misconfiguration for an outage.
func duration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return fallback
	}
	return d
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
