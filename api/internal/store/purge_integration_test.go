package store

import (
	"context"
	"testing"
)

// TestPurgeExpiredRemovesEventAndItsRows pins the promise the README makes:
// when a link expires the data goes away, including everything hanging off it.
func TestPurgeExpiredRemovesEventAndItsRows(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()

	expired, err := st.CreateEvent(ctx, sampleEvent())
	if err != nil {
		t.Fatalf("create expired event: %v", err)
	}
	live, err := st.CreateEvent(ctx, sampleEvent())
	if err != nil {
		t.Fatalf("create live event: %v", err)
	}

	p, _, err := st.JoinEvent(ctx, expired.Event.ID, NewParticipant{
		DisplayName: "Ana", TZ: "America/New_York", Role: "required",
	})
	if err != nil {
		t.Fatalf("join: %v", err)
	}

	// Reaching past the store to age the row on purpose. There is no setter
	// for expires_at, because nothing in the product may move an expiry.
	if _, err := st.pool.Exec(ctx,
		"update events set expires_at = now() - interval '1 day' where id = $1",
		expired.Event.ID); err != nil {
		t.Fatalf("age event: %v", err)
	}

	n, err := st.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n < 1 {
		t.Fatalf("purge deleted %d events, want at least the expired one", n)
	}

	if _, err := st.EventBySlug(ctx, expired.Event.Slug); err == nil {
		t.Error("expired event is still readable after the purge")
	}
	if _, err := st.EventBySlug(ctx, live.Event.Slug); err != nil {
		t.Errorf("unexpired event was swept too: %v", err)
	}

	// The cascade is the part worth pinning. An event row disappearing while
	// its participants and their busy intervals stay behind would leave
	// exactly the data the expiry promise says is gone.
	var participants int
	if err := st.pool.QueryRow(ctx,
		"select count(*) from participants where id = $1", p.ID).Scan(&participants); err != nil {
		t.Fatalf("count participants: %v", err)
	}
	if participants != 0 {
		t.Errorf("participant rows survived the purge: %d", participants)
	}
}

// TestPurgeExpiredLeavesLiveEventsAlone is the other half. A sweep that
// deletes nothing is harmless; a sweep that deletes a live event destroys a
// poll people are still answering.
func TestPurgeExpiredLeavesLiveEventsAlone(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	fresh, err := st.CreateEvent(ctx, sampleEvent())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := st.PurgeExpired(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := st.EventBySlug(ctx, fresh.Event.Slug); err != nil {
		t.Fatalf("a freshly created event was purged: %v", err)
	}
}
