package store

import (
	"errors"
	"testing"

	"github.com/maazinshaikh/overlap/api/internal/solver"
)

func mustEvent(t *testing.T, st *Store) Event {
	t.Helper()
	res, err := st.CreateEvent(t.Context(), sampleEvent())
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	return res.Event
}

func mustJoin(t *testing.T, st *Store, eventID, name string) (Participant, string) {
	t.Helper()
	p, token, err := st.JoinEvent(t.Context(), eventID, NewParticipant{
		DisplayName: name,
		TZ:          "America/New_York",
	})
	if err != nil {
		t.Fatalf("JoinEvent(%s): %v", name, err)
	}
	return p, token
}

func TestJoinAndResolveByToken(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)

	p, token, err := st.JoinEvent(t.Context(), ev.ID, NewParticipant{
		DisplayName: "Ana",
		TZ:          "Europe/London",
		Role:        RoleRequired,
	})
	if err != nil {
		t.Fatalf("JoinEvent: %v", err)
	}
	if p.Role != RoleRequired || p.TZ != "Europe/London" {
		t.Fatalf("stored %+v, want required/Europe-London", p)
	}
	if p.Responded() {
		t.Fatal("a fresh participant has not responded")
	}
	if token == "" {
		t.Fatal("join must return a token")
	}

	got, err := st.ParticipantByToken(t.Context(), ev.ID, token)
	if err != nil {
		t.Fatalf("ParticipantByToken: %v", err)
	}
	if got.ID != p.ID {
		t.Fatalf("resolved %s, want %s", got.ID, p.ID)
	}
}

// TestTokenIsNotStoredInTheClear is the reason the column is a digest. Anyone
// who reads the table must not come away able to impersonate a participant.
func TestTokenIsNotStoredInTheClear(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)
	_, token := mustJoin(t, st, ev.ID, "Ana")

	var stored []byte
	err := st.pool.QueryRow(t.Context(),
		`select token_hash from participants where event_id = $1`, mustUUID(t, ev.ID),
	).Scan(&stored)
	if err != nil {
		t.Fatalf("read token_hash: %v", err)
	}

	if string(stored) == token {
		t.Fatal("the raw token is in the database")
	}
	if len(stored) != 32 {
		t.Fatalf("token_hash is %d bytes, want a 32-byte SHA-256 digest", len(stored))
	}
}

// TestTokenIsScopedToItsEvent is the cross-event rejection case. A token minted
// for one event must be meaningless against another, even though both rows sit
// in the same table.
func TestTokenIsScopedToItsEvent(t *testing.T) {
	st := testStore(t)

	a := mustEvent(t, st)
	b := mustEvent(t, st)
	_, tokenA := mustJoin(t, st, a.ID, "Ana")

	if _, err := st.ParticipantByToken(t.Context(), a.ID, tokenA); err != nil {
		t.Fatalf("token must work on its own event: %v", err)
	}

	_, err := st.ParticipantByToken(t.Context(), b.ID, tokenA)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-event lookup returned %v, want ErrNotFound", err)
	}
}

func TestUnknownTokenIsNotFound(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)
	mustJoin(t, st, ev.ID, "Ana")

	_, err := st.ParticipantByToken(t.Context(), ev.ID, "not-a-real-token")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestCreateEventMintsOrganizer(t *testing.T) {
	st := testStore(t)

	in := sampleEvent()
	in.Organizer = &NewParticipant{DisplayName: "Maazin", TZ: "America/New_York"}

	res, err := st.CreateEvent(t.Context(), in)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if res.Organizer == nil || res.OrganizerToken == "" {
		t.Fatal("supplying an organizer must return one, with a token")
	}
	if !res.Organizer.IsOrganizer {
		t.Error("the organizer must be flagged as such")
	}
	// An optional organizer would let the event be settled without the person
	// who called the meeting.
	if res.Organizer.Role != RoleRequired {
		t.Errorf("organizer role = %q, want %q", res.Organizer.Role, RoleRequired)
	}

	got, err := st.ParticipantByToken(t.Context(), res.Event.ID, res.OrganizerToken)
	if err != nil {
		t.Fatalf("organizer token does not resolve: %v", err)
	}
	if got.ID != res.Organizer.ID {
		t.Fatal("organizer token resolves to the wrong participant")
	}
}

func TestCreateEventWithoutOrganizerIsAllowed(t *testing.T) {
	st := testStore(t)

	res, err := st.CreateEvent(t.Context(), sampleEvent())
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if res.Organizer != nil || res.OrganizerToken != "" {
		t.Fatal("no organizer was asked for, so none should be created")
	}

	ps, err := st.Participants(t.Context(), res.Event.ID)
	if err != nil {
		t.Fatalf("Participants: %v", err)
	}
	if len(ps) != 0 {
		t.Fatalf("want an empty event, got %d participants", len(ps))
	}
}

func TestSaveAndReadResponses(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)
	p, _ := mustJoin(t, st, ev.ID, "Ana")

	exp, err := ev.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}

	want := []Response{
		{SlotStart: exp.Starts[0], Tier: solver.TierPreferred, Source: SourceManual},
		{SlotStart: exp.Starts[1], Tier: solver.TierIfNeeded, Source: SourceCoarse},
	}
	if err := st.SaveResponses(t.Context(), p.ID, want); err != nil {
		t.Fatalf("SaveResponses: %v", err)
	}

	got, err := st.ResponsesForParticipant(t.Context(), p.ID)
	if err != nil {
		t.Fatalf("ResponsesForParticipant: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d responses, want 2", len(got))
	}
	for i, w := range want {
		if !got[i].SlotStart.Equal(w.SlotStart) {
			t.Errorf("[%d] start = %s, want %s", i, got[i].SlotStart, w.SlotStart)
		}
		if got[i].Tier != w.Tier {
			t.Errorf("[%d] tier = %v, want %v", i, got[i].Tier, w.Tier)
		}
		// Source must survive the round trip: a calendar-derived tier has to
		// stay distinguishable from a stated one.
		if got[i].Source != w.Source {
			t.Errorf("[%d] source = %q, want %q", i, got[i].Source, w.Source)
		}
	}

	// Saving marks the person as having answered, which is what separates a
	// deliberate wall of "no" from silence.
	ps, err := st.Participants(t.Context(), ev.ID)
	if err != nil {
		t.Fatalf("Participants: %v", err)
	}
	for _, q := range ps {
		if q.ID == p.ID && !q.Responded() {
			t.Error("participant is not marked as responded after saving")
		}
	}
}

// TestSaveResponsesReplaces is the reason the write is a replacement rather
// than a merge: someone must be able to withdraw a slot they previously
// offered, and a merge makes that impossible to express.
func TestSaveResponsesReplaces(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)
	p, _ := mustJoin(t, st, ev.ID, "Ana")

	exp, err := ev.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}

	first := []Response{
		{SlotStart: exp.Starts[0], Tier: solver.TierPreferred},
		{SlotStart: exp.Starts[1], Tier: solver.TierPreferred},
		{SlotStart: exp.Starts[2], Tier: solver.TierPreferred},
	}
	if err := st.SaveResponses(t.Context(), p.ID, first); err != nil {
		t.Fatalf("first save: %v", err)
	}

	second := []Response{{SlotStart: exp.Starts[0], Tier: solver.TierOK}}
	if err := st.SaveResponses(t.Context(), p.ID, second); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, err := st.ResponsesForParticipant(t.Context(), p.ID)
	if err != nil {
		t.Fatalf("ResponsesForParticipant: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d responses, want the 1 from the second save: %+v", len(got), got)
	}
	if got[0].Tier != solver.TierOK {
		t.Fatalf("tier = %v, want the resubmitted ok", got[0].Tier)
	}
}

// TestEmptySaveStillMarksResponded is "nothing works for me". No rows, but the
// person has definitely answered.
func TestEmptySaveStillMarksResponded(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)
	p, _ := mustJoin(t, st, ev.ID, "Ana")

	if err := st.SaveResponses(t.Context(), p.ID, nil); err != nil {
		t.Fatalf("SaveResponses: %v", err)
	}

	ps, err := st.Participants(t.Context(), ev.ID)
	if err != nil {
		t.Fatalf("Participants: %v", err)
	}
	if len(ps) != 1 || !ps[0].Responded() {
		t.Fatalf("an empty submission must still count as a response: %+v", ps)
	}
}

func TestResponsesForEventGroupsByParticipant(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)
	ana, _ := mustJoin(t, st, ev.ID, "Ana")
	ben, _ := mustJoin(t, st, ev.ID, "Ben")

	exp, err := ev.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}

	if err := st.SaveResponses(t.Context(), ana.ID, []Response{
		{SlotStart: exp.Starts[0], Tier: solver.TierPreferred},
	}); err != nil {
		t.Fatalf("save Ana: %v", err)
	}
	if err := st.SaveResponses(t.Context(), ben.ID, []Response{
		{SlotStart: exp.Starts[0], Tier: solver.TierNo},
		{SlotStart: exp.Starts[1], Tier: solver.TierOK},
	}); err != nil {
		t.Fatalf("save Ben: %v", err)
	}

	got, err := st.ResponsesForEvent(t.Context(), ev.ID)
	if err != nil {
		t.Fatalf("ResponsesForEvent: %v", err)
	}
	if len(got[ana.ID]) != 1 || len(got[ben.ID]) != 2 {
		t.Fatalf("grouping wrong: Ana %d, Ben %d", len(got[ana.ID]), len(got[ben.ID]))
	}
}

// TestDeletingAnEventTakesItsResponses guards the cascade. An expired event must
// not leave availability data behind.
func TestDeletingAnEventTakesItsResponses(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)
	p, _ := mustJoin(t, st, ev.ID, "Ana")

	exp, err := ev.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if err := st.SaveResponses(t.Context(), p.ID, []Response{
		{SlotStart: exp.Starts[0], Tier: solver.TierOK},
	}); err != nil {
		t.Fatalf("SaveResponses: %v", err)
	}

	if _, err := st.pool.Exec(t.Context(),
		`delete from events where id = $1`, mustUUID(t, ev.ID)); err != nil {
		t.Fatalf("delete event: %v", err)
	}

	var n int
	if err := st.pool.QueryRow(t.Context(),
		`select count(*) from responses where participant_id = $1`, mustUUID(t, p.ID),
	).Scan(&n); err != nil {
		t.Fatalf("count responses: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d responses survived the event", n)
	}
}

// --- helpers -----------------------------------------------------------------

func mustUUID(t *testing.T, s string) any {
	t.Helper()
	u, err := parseUUID(s)
	if err != nil {
		t.Fatalf("parseUUID(%q): %v", s, err)
	}
	return u
}
