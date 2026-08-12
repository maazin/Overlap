package tz

import (
	"errors"
	"testing"
)

func TestLoadAcceptsRealZones(t *testing.T) {
	for _, name := range []string{"America/New_York", "Europe/London", "Asia/Kolkata", "UTC"} {
		if _, err := Load(name); err != nil {
			t.Fatalf("Load(%q) = %v, want success", name, err)
		}
	}
}

// TestLoadRejectsServerDependentNames is the point of this package. Both of
// these are accepted by time.LoadLocation and both would make an event's
// meaning depend on the machine serving it.
func TestLoadRejectsServerDependentNames(t *testing.T) {
	for _, name := range []string{"", "Local"} {
		if _, err := Load(name); !errors.Is(err, ErrUnknownZone) {
			t.Fatalf("Load(%q) = %v, want ErrUnknownZone", name, err)
		}
	}
}

func TestLoadRejectsNonsense(t *testing.T) {
	for _, name := range []string{"Mars/Olympus", "../../etc/passwd", "Not A Zone", "America/"} {
		if _, err := Load(name); !errors.Is(err, ErrUnknownZone) {
			t.Fatalf("Load(%q) = %v, want ErrUnknownZone", name, err)
		}
	}
}

// Casing is deliberately not asserted. time.LoadLocation resolves through the
// host's zoneinfo directory when there is one, so "america/new_york" is
// accepted on a case-insensitive filesystem such as macOS and rejected on
// Linux. Pinning either behaviour would encode the developer's laptop into the
// test suite. It does not matter in practice: zone names reach the API from
// Intl.DateTimeFormat().resolvedOptions().timeZone, which is always canonical.

func TestAvailableSucceedsWithARealDatabase(t *testing.T) {
	if err := Available(); err != nil {
		t.Fatalf("Available() = %v; the test binary should have zoneinfo", err)
	}
}

// TestLoadIsCached checks the memoisation returns the identical pointer, since
// every request that renders slots resolves a zone.
func TestLoadIsCached(t *testing.T) {
	a, err := Load("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("repeated Load should return the cached location")
	}
}

func TestLoadIsConcurrencySafe(t *testing.T) {
	done := make(chan struct{})
	for range 32 {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, err := Load("Europe/London"); err != nil {
				t.Error(err)
			}
		}()
	}
	for range 32 {
		<-done
	}
}
