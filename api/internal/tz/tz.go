// Package tz resolves IANA timezone names to locations.
//
// Zone names arrive from the browser via Intl.DateTimeFormat().resolvedOptions()
// and are therefore untrusted input, so Load is deliberately stricter than
// time.LoadLocation.
package tz

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrUnknownZone means the name is not a usable IANA zone.
var ErrUnknownZone = errors.New("tz: unknown timezone")

// cache memoises resolved locations. time.LoadLocation reads and parses the
// zoneinfo database on every call, and every request that renders slots needs a
// location. Locations are immutable once loaded, so sharing them is safe.
var cache sync.Map // string -> *time.Location

// Load resolves an IANA zone name.
//
// Two names that time.LoadLocation accepts are rejected here. "Local" resolves
// to whatever zone the server happens to run in, which would silently make
// output depend on deployment rather than on the organizer. The empty string
// resolves to UTC, which turns a missing field into a plausible-looking wrong
// answer instead of an error. Both are bugs waiting to happen, so neither is
// allowed in from a request.
func Load(name string) (*time.Location, error) {
	switch name {
	case "":
		return nil, fmt.Errorf("%w: empty", ErrUnknownZone)
	case "Local":
		return nil, fmt.Errorf("%w: %q depends on the server, not the organizer", ErrUnknownZone, name)
	}

	if v, ok := cache.Load(name); ok {
		return v.(*time.Location), nil
	}

	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownZone, name)
	}

	cache.Store(name, loc)
	return loc, nil
}

// Valid reports whether name is a zone Load would accept.
func Valid(name string) bool {
	_, err := Load(name)
	return err == nil
}

// Available verifies that a zone database is reachable at all.
//
// A binary with no zoneinfo does not fail loudly: time.LoadLocation returns an
// error for every name, so every event creation rejects its timezone and the
// product looks broken for a reason nothing points at. Calling this at startup
// turns that into one clear message before the server accepts traffic. The
// scratch container is exactly where this goes wrong, which is why cmd/api
// imports time/tzdata to carry the database in the binary.
func Available() error {
	for _, canary := range []string{"America/New_York", "Europe/London", "Australia/Sydney"} {
		if _, err := Load(canary); err != nil {
			return fmt.Errorf("tz: no usable timezone database: %w", err)
		}
	}
	return nil
}
