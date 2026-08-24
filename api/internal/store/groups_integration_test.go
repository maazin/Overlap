package store

import (
	"errors"
	"testing"
)

func mustDecidedEventWithParticipants(t *testing.T, st *Store, names ...string) (Event, []Participant, []string) {
	t.Helper()
	ev := mustEvent(t, st)

	var ps []Participant
	var tokens []string
	for _, n := range names {
		p, tok := mustJoin(t, st, ev.ID, n)
		ps = append(ps, p)
		tokens = append(tokens, tok)
	}

	exp, err := ev.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	decided, err := st.Decide(t.Context(), ev.ID, exp.Starts[0])
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return decided, ps, tokens
}

// TestGraduationCarriesRolesAndTimezones is the phase 9 DoD: names, timezones
// and roles are seeded from the event without anyone retyping them.
func TestGraduationCarriesRolesAndTimezones(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)

	ana, _ := mustJoin(t, st, ev.ID, "Ana")

	_, _, benErr := st.JoinEvent(t.Context(), ev.ID, NewParticipant{
		DisplayName: "Ben", TZ: "Europe/London", Role: RoleRequired,
	})
	if benErr != nil {
		t.Fatalf("JoinEvent(Ben): %v", benErr)
	}

	exp, err := ev.Slots()
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	decided, err := st.Decide(t.Context(), ev.ID, exp.Starts[0])
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	out, err := st.Graduate(t.Context(), decided.ID, "Team sync crew", ana.ID)
	if err != nil {
		t.Fatalf("Graduate: %v", err)
	}
	if out.Group.Slug == "" {
		t.Fatal("graduated group has no slug")
	}
	if out.CallerToken == "" {
		t.Fatal("graduating participant must get a token back")
	}

	members, err := st.GroupMembers(t.Context(), out.Group.ID)
	if err != nil {
		t.Fatalf("GroupMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}

	byName := map[string]GroupMember{}
	for _, m := range members {
		byName[m.DisplayName] = m
	}

	if got := byName["Ben"]; got.TZ != "Europe/London" || got.DefaultRole != RoleRequired {
		t.Errorf("Ben carried over as %+v, want Europe/London required", got)
	}
	if got := byName["Ana"]; !got.Claimed {
		t.Error("the graduating caller's own seat must already be claimed")
	}
	if got := byName["Ben"]; got.Claimed {
		t.Error("a member who did not graduate the event must start unclaimed")
	}
}

func TestGraduateRequiresParticipants(t *testing.T) {
	st := testStore(t)
	ev := mustEvent(t, st)

	if _, err := st.Graduate(t.Context(), ev.ID, "Empty", "does-not-exist"); err == nil {
		t.Fatal("an event with nobody on it cannot graduate")
	}
}

// TestClaimIsIdempotentAcrossDevices is the "clear storage, reopen the group
// link, reclaim by name" flow from the phase 9 DoD.
func TestClaimIsIdempotentAcrossDevices(t *testing.T) {
	st := testStore(t)
	decided, ps, _ := mustDecidedEventWithParticipants(t, st, "Ana", "Ben")

	out, err := st.Graduate(t.Context(), decided.ID, "Crew", ps[0].ID)
	if err != nil {
		t.Fatalf("Graduate: %v", err)
	}

	members, err := st.GroupMembers(t.Context(), out.Group.ID)
	if err != nil {
		t.Fatalf("GroupMembers: %v", err)
	}
	var ben GroupMember
	for _, m := range members {
		if m.DisplayName == "Ben" {
			ben = m
		}
	}
	if ben.ID == "" {
		t.Fatal("Ben was not seeded")
	}

	claimed1, token1, err := st.ClaimGroupMember(t.Context(), out.Group.ID, ben.ID)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if token1 == "" {
		t.Fatal("claim must return a token")
	}

	// Simulate clearing storage and reclaiming on a new device: same member
	// id, a fresh token.
	claimed2, token2, err := st.ClaimGroupMember(t.Context(), out.Group.ID, ben.ID)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimed2.ID != claimed1.ID {
		t.Fatal("reclaiming must return the same member, not a new one")
	}
	if token2 == token1 {
		t.Fatal("a reclaim should mint a fresh token rather than reusing the old one")
	}

	if _, err := st.GroupMemberByToken(t.Context(), out.Group.ID, token1); !errors.Is(err, ErrNotFound) {
		t.Fatal("the old token must stop working once a new one is claimed")
	}
	if _, err := st.GroupMemberByToken(t.Context(), out.Group.ID, token2); err != nil {
		t.Fatalf("the new token must resolve: %v", err)
	}
}

// TestClaimIsScopedToItsGroup mirrors the cross-event participant token test:
// a member id from one group must not resolve through another group's claim
// endpoint.
func TestClaimIsScopedToItsGroup(t *testing.T) {
	st := testStore(t)
	decided, ps, _ := mustDecidedEventWithParticipants(t, st, "Ana")
	a, err := st.Graduate(t.Context(), decided.ID, "Group A", ps[0].ID)
	if err != nil {
		t.Fatalf("Graduate A: %v", err)
	}

	decidedB, psB, _ := mustDecidedEventWithParticipants(t, st, "Cara")
	b, err := st.Graduate(t.Context(), decidedB.ID, "Group B", psB[0].ID)
	if err != nil {
		t.Fatalf("Graduate B: %v", err)
	}

	membersA, err := st.GroupMembers(t.Context(), a.Group.ID)
	if err != nil {
		t.Fatalf("GroupMembers: %v", err)
	}

	if _, _, err := st.ClaimGroupMember(t.Context(), b.Group.ID, membersA[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claiming group A's member through group B's link returned %v, want ErrNotFound", err)
	}
}

// TestGroupEventPrePopulatesParticipants is the "create a second event with
// zero re-entry" DoD, exercised through ClaimEventSeat: a member's name,
// timezone and role come from their group membership, not from anything typed
// into the new event.
func TestGroupEventPrePopulatesParticipants(t *testing.T) {
	st := testStore(t)
	decided, ps, _ := mustDecidedEventWithParticipants(t, st, "Ana", "Ben")

	graduated, err := st.Graduate(t.Context(), decided.ID, "Crew", ps[0].ID)
	if err != nil {
		t.Fatalf("Graduate: %v", err)
	}
	members, err := st.GroupMembers(t.Context(), graduated.Group.ID)
	if err != nil {
		t.Fatalf("GroupMembers: %v", err)
	}
	var ben GroupMember
	for _, m := range members {
		if m.DisplayName == "Ben" {
			ben = m
		}
	}

	in := sampleEvent()
	in.GroupID = graduated.Group.ID
	created, err := st.CreateEvent(t.Context(), in)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if created.Event.GroupID == nil || *created.Event.GroupID != graduated.Group.ID {
		t.Fatalf("event GroupID = %v, want %s", created.Event.GroupID, graduated.Group.ID)
	}

	// Ben has never opened this specific event before; claiming a seat must
	// still produce his real name, zone and role with nothing re-entered.
	seat, token, err := st.ClaimEventSeat(t.Context(), created.Event.ID, ben.ID)
	if err != nil {
		t.Fatalf("ClaimEventSeat: %v", err)
	}
	if token == "" {
		t.Fatal("claiming a seat must return a token")
	}
	if seat.DisplayName != "Ben" || seat.TZ != ben.TZ || seat.Role != ben.DefaultRole {
		t.Fatalf("seat = %+v, want name/tz/role copied from the group membership", seat)
	}

	// Claiming again from a different device must return the same seat, not a
	// duplicate participant.
	seat2, token2, err := st.ClaimEventSeat(t.Context(), created.Event.ID, ben.ID)
	if err != nil {
		t.Fatalf("second ClaimEventSeat: %v", err)
	}
	if seat2.ID != seat.ID {
		t.Fatal("claiming twice must return the same seat, not create a second one")
	}
	if token2 == token {
		t.Fatal("a repeat claim should still rotate the token to the new device")
	}

	all, err := st.Participants(t.Context(), created.Event.ID)
	if err != nil {
		t.Fatalf("Participants: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d participants for Ben, want exactly 1: %+v", len(all), all)
	}
}

// TestGroupDecisionHistoryAccumulatesAcrossEvents is what feeds the phase 9
// tiebreak: every event a group decides must show up in its history with a
// real DecidedAt, since decay has nothing to decay from without one.
func TestGroupDecisionHistoryAccumulatesAcrossEvents(t *testing.T) {
	st := testStore(t)
	decided, ps, _ := mustDecidedEventWithParticipants(t, st, "Ana")
	graduated, err := st.Graduate(t.Context(), decided.ID, "Crew", ps[0].ID)
	if err != nil {
		t.Fatalf("Graduate: %v", err)
	}

	// The graduating event is already one decision. Decide two more under the
	// same group. sampleEvent always describes the same window, so every one
	// of these legitimately decides to the identical instant -- the count is
	// what this test is actually checking, not distinctness of the slot.
	want := *decided.DecidedSlotStart
	for range 2 {
		in := sampleEvent()
		in.GroupID = graduated.Group.ID
		created, err := st.CreateEvent(t.Context(), in)
		if err != nil {
			t.Fatalf("CreateEvent: %v", err)
		}
		exp, err := created.Event.Slots()
		if err != nil {
			t.Fatalf("Slots: %v", err)
		}
		if _, err := st.Decide(t.Context(), created.Event.ID, exp.Starts[0]); err != nil {
			t.Fatalf("Decide: %v", err)
		}
	}

	hist, err := st.GroupDecisionHistory(t.Context(), graduated.Group.ID)
	if err != nil {
		t.Fatalf("GroupDecisionHistory: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("got %d decisions, want 3: %+v", len(hist), hist)
	}
	for _, d := range hist {
		if !d.Slot.Equal(want) {
			t.Errorf("slot = %s, want %s", d.Slot, want)
		}
		if d.DecidedAt.IsZero() {
			t.Error("DecidedAt must be recorded, or history decay has nothing to decay from")
		}
	}
}

// TestUndecidedEventIsExcludedFromHistory: an open event has not been settled
// on anything, and must not contribute a phantom preference.
func TestUndecidedEventIsExcludedFromHistory(t *testing.T) {
	st := testStore(t)
	decided, ps, _ := mustDecidedEventWithParticipants(t, st, "Ana")
	graduated, err := st.Graduate(t.Context(), decided.ID, "Crew", ps[0].ID)
	if err != nil {
		t.Fatalf("Graduate: %v", err)
	}

	in := sampleEvent()
	in.GroupID = graduated.Group.ID
	if _, err := st.CreateEvent(t.Context(), in); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	hist, err := st.GroupDecisionHistory(t.Context(), graduated.Group.ID)
	if err != nil {
		t.Fatalf("GroupDecisionHistory: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("got %d decisions, want only the graduating event's own", len(hist))
	}
}

func TestUnclaimedMemberCannotAuthenticate(t *testing.T) {
	st := testStore(t)
	decided, ps, _ := mustDecidedEventWithParticipants(t, st, "Ana")
	graduated, err := st.Graduate(t.Context(), decided.ID, "Crew", ps[0].ID)
	if err != nil {
		t.Fatalf("Graduate: %v", err)
	}

	// No token was ever set for an unclaimed member, so there is nothing to
	// authenticate with, and there must be no way to guess one.
	if _, err := st.GroupMemberByToken(t.Context(), graduated.Group.ID, ""); err == nil {
		t.Fatal("an empty token must never resolve")
	}
}
