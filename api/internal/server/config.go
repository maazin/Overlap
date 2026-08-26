package server

import (
	"os"
	"strconv"
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

	// ClientIPHeaders names the headers that may carry the real client
	// address, in order of preference. The first one present on a request
	// wins.
	//
	// Empty by default, which means the socket address is used. That default
	// is the safe one: trusting a header nobody upstream overwrites lets a
	// client hand itself a fresh rate limit bucket per request. Set it only to
	// headers a proxy in front of this server rewrites on every request:
	// Fly-Client-IP on Fly, True-Client-IP or CF-Connecting-IP behind the
	// Cloudflare that Render runs on.
	//
	// Prefer single-value headers. X-Forwarded-For is a list, and behind two
	// proxies its last entry is an internal hop that can change per request,
	// which gives every request its own bucket and quietly disables limiting.
	ClientIPHeaders []string

	// RateLimitBurst is how many requests one address may make at once, and
	// RateLimitPerMinute the sustained rate it refills at. Zero per-minute
	// disables limiting.
	RateLimitBurst     int
	RateLimitPerMinute float64

	// RateLimitMaxKeys caps how many addresses are tracked. Untrusted input
	// keys that map, so it needs a ceiling.
	RateLimitMaxKeys int

	// ProposalCooldown is how long a computed group proposal is reused before
	// the calendars behind it are refreshed again. Zero recomputes every time.
	ProposalCooldown time.Duration

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

		ClientIPHeaders: splitList(os.Getenv("CLIENT_IP_HEADER")),

		// Sized for the shape of real use rather than for a guess at a limit.
		// Answering a poll is a handful of writes spread over a minute or two,
		// and a household or office behind one address multiplies that by a
		// few people. Twenty at once with sixty a minute leaves that
		// comfortable while making automated abuse pointless.
		RateLimitBurst:     intEnv("RATE_LIMIT_BURST", 20),
		RateLimitPerMinute: floatEnv("RATE_LIMIT_PER_MINUTE", 60),
		RateLimitMaxKeys:   intEnv("RATE_LIMIT_MAX_KEYS", 20000),

		ProposalCooldown: duration("PROPOSAL_COOLDOWN", 30*time.Second),
		ShutdownTimeout:  15 * time.Second,
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

func intEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func floatEnv(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return fallback
	}
	return f
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
