package results

import (
	"testing"
	"time"

	"github.com/maazin/Overlap/api/internal/solver"
	"github.com/maazin/Overlap/api/internal/store"
)

func starts() []time.Time {
	ny, _ := time.LoadLocation("America/New_York")
	return []time.Time{
		time.Date(2026, 6, 10, 9, 0, 0, 0, ny),
		time.Date(2026, 6, 10, 10, 0, 0, 0, ny),
		time.Date(2026, 6, 10, 11, 0, 0, 0, ny),
	}
}

func responded(id, name, role string) store.Participant {
	at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	return store.Participant{ID: id, DisplayName: name, TZ: "America/New_York", Role: role, RespondedAt: &at}
}

func silent(id, name, role string) store.Participant {
	return store.Participant{ID: id, DisplayName: name, TZ: "America/New_York", Role: role}
}

// TestResponderSilenceIsNo is one half of the rule this package exists to
// enforce. Ana submitted and mentioned only the first slot, so the other two
// are refusals, not open questions.
func TestResponderSilenceIsNo(t *testing.T) {
	ss := starts()
	in := Build(
		[]store.Participant{responded("ana", "Ana", store.RoleRequired)},
		map[string][]store.Response{
			"ana": {{SlotStart: ss[0], Tier: solver.TierPreferred}},
		},
		ss,
	)

	got := in.Responses["ana"]
	if len(got) != 3 {
		t.Fatalf("a responder must have a tier for every slot, got %d", len(got))
	}
	if got[ss[0]] != solver.TierPreferred {
		t.Errorf("stated slot = %v, want preferred", got[ss[0]])
	}
	for _, s := range ss[1:] {
		if got[s] != solver.TierNo {
			t.Errorf("unmentioned slot %s = %v, want no", s.Format(time.Kitchen), got[s])
		}
	}
}

// TestNonResponderStaysUnknown is the other half. Ben has said nothing, and
// nothing may be invented on his behalf.
func TestNonResponderStaysUnknown(t *testing.T) {
	ss := starts()
	in := Build(
		[]store.Participant{silent("ben", "Ben", store.RoleRequired)},
		nil,
		ss,
	)

	if _, present := in.Responses["ben"]; present {
		t.Fatal("a participant who has not submitted must have no response map at all")
	}

	// And the solver must read that as unknown rather than as a veto.
	got := solver.ScoreSlot(ss[0], in.Participants, in.Responses, solver.DefaultConfig())
	if got.Eliminated {
		t.Fatal("silence from a required participant must not eliminate a slot")
	}
	if len(got.Unknown) != 1 || got.Unknown[0] != "Ben" {
		t.Fatalf("Unknown = %v, want [Ben]", got.Unknown)
	}
}

// TestSubmittedWallOfNoIsNotSilence is the distinction that makes phase 7
// possible: someone who answered "none of these work" has answered.
func TestSubmittedWallOfNoIsNotSilence(t *testing.T) {
	ss := starts()
	in := Build(
		[]store.Participant{responded("ana", "Ana", store.RoleRequired)},
		map[string][]store.Response{"ana": nil}, // submitted, stored nothing
		ss,
	)

	got := solver.ScoreSlot(ss[0], in.Participants, in.Responses, solver.DefaultConfig())
	if len(got.Unknown) != 0 {
		t.Fatalf("Unknown = %v, want empty; Ana has answered", got.Unknown)
	}
	if !got.Eliminated {
		t.Fatal("a required participant who refused everything must eliminate the slot")
	}
}

func TestRolesAndZonesCarryOver(t *testing.T) {
	ss := starts()
	ps := []store.Participant{
		responded("ana", "Ana", store.RoleRequired),
		responded("dev", "Dev", store.RoleOptional),
	}
	ps[1].TZ = "Asia/Tokyo"

	in := Build(ps, nil, ss)

	if in.Participants[0].Role != solver.RoleRequired {
		t.Error("required role did not carry over")
	}
	if in.Participants[1].Role != solver.RoleOptional {
		t.Error("optional role did not carry over")
	}
	if in.Participants[1].Loc.String() != "Asia/Tokyo" {
		t.Errorf("zone = %s, want Asia/Tokyo", in.Participants[1].Loc)
	}
}

// TestUnknownZoneFallsBackToUTC keeps one unresolvable zone from taking down
// the whole result. It only affects that person's sociable-hours penalty.
func TestUnknownZoneFallsBackToUTC(t *testing.T) {
	ss := starts()
	p := responded("ana", "Ana", store.RoleRequired)
	p.TZ = "Mars/Olympus_Mons"

	in := Build([]store.Participant{p}, nil, ss)
	if in.Participants[0].Loc != time.UTC {
		t.Fatalf("Loc = %v, want UTC", in.Participants[0].Loc)
	}
}

// TestOrphanedResponsesAreDropped covers an event whose hours were edited after
// people answered: a stored tier for an instant the window no longer produces
// must not quietly influence the ranking.
func TestOrphanedResponsesAreDropped(t *testing.T) {
	ss := starts()
	orphan := ss[0].Add(17 * time.Minute)

	in := Build(
		[]store.Participant{responded("ana", "Ana", store.RoleRequired)},
		map[string][]store.Response{
			"ana": {
				{SlotStart: ss[0], Tier: solver.TierPreferred},
				{SlotStart: orphan, Tier: solver.TierPreferred},
			},
		},
		ss,
	)

	if len(in.Responses["ana"]) != len(ss) {
		t.Fatalf("got %d tiers, want exactly the %d real slots", len(in.Responses["ana"]), len(ss))
	}
	if _, present := in.Responses["ana"][orphan]; present {
		t.Fatal("a response against a slot outside the window must be dropped")
	}
}

// TestMatchesAcrossEquivalentInstants: stored rows come back in UTC while the
// expansion carries the organizer's zone. The same moment must not become two
// different keys.
func TestMatchesAcrossEquivalentInstants(t *testing.T) {
	ss := starts()
	inUTC := ss[1].UTC()

	in := Build(
		[]store.Participant{responded("ana", "Ana", store.RoleRequired)},
		map[string][]store.Response{"ana": {{SlotStart: inUTC, Tier: solver.TierPreferred}}},
		ss,
	)

	if got := in.Responses["ana"][ss[1]]; got != solver.TierPreferred {
		t.Fatalf("tier = %v, want preferred; the UTC form did not match the zoned slot", got)
	}
}

func TestRespondedCount(t *testing.T) {
	got := RespondedCount([]store.Participant{
		responded("a", "Ana", store.RoleRequired),
		silent("b", "Ben", store.RoleRequired),
		responded("c", "Cara", store.RoleOptional),
	})
	if got != 2 {
		t.Fatalf("RespondedCount = %d, want 2", got)
	}
}
