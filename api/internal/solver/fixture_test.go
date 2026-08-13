package solver

import (
	"math"
	"testing"
	"time"
)

// The six-person fixture from the build plan. Every score below is computed by
// hand in the comments rather than captured from a run, so a change in the
// scoring model fails here loudly instead of quietly re-blessing itself.
//
// Config is the default: alpha 0.7, optional weight 0.5, unsociable penalty
// 0.15 per person capped at 0.45, sociable hours [08:00, 21:00).

// June 10 2026 is a Wednesday well clear of either DST transition, so every
// local hour below maps to exactly one instant and the arithmetic stays about
// scoring rather than timezones.
func slotAt(hour int) time.Time {
	return time.Date(2026, 6, 10, hour, 0, 0, 0, ny)
}

func sixPeople() []Participant {
	return []Participant{
		{ID: "ana", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "ben", Name: "Ben", Role: RoleRequired, Loc: ny},
		{ID: "cara", Name: "Cara", Role: RoleRequired, Loc: ny},
		{ID: "dev", Name: "Dev", Role: RoleOptional, Loc: ny},
		{ID: "eve", Name: "Eve", Role: RoleOptional, Loc: ny},
		{ID: "fin", Name: "Fin", Role: RoleOptional, Loc: ny},
	}
}

var (
	early = slotAt(7)  // everyone free, but unsociable for all six
	nine  = slotAt(9)  // the genuine best
	elev  = slotAt(11) // strong, but costs Dev
	two   = slotAt(14) // strands Ana on if_needed
	four  = slotAt(16) // Ana cannot make it at all
)

func sixResponses() Responses {
	return Responses{
		// 07:00 -- unanimous preferred. Sociable-hours penalty is the only
		// thing standing between this and a perfect score.
		"ana":  {early: TierPreferred, nine: TierPreferred, elev: TierOK, two: TierIfNeeded, four: TierNo},
		"ben":  {early: TierPreferred, nine: TierOK, elev: TierPreferred, two: TierOK, four: TierPreferred},
		"cara": {early: TierPreferred, nine: TierPreferred, elev: TierOK, two: TierOK, four: TierPreferred},
		"dev":  {early: TierPreferred, nine: TierOK, elev: TierNo, two: TierPreferred, four: TierPreferred},
		"eve":  {early: TierPreferred, nine: TierPreferred, elev: TierOK, two: TierPreferred, four: TierPreferred},
		"fin":  {early: TierPreferred, nine: TierIfNeeded, elev: TierOK, two: TierPreferred, four: TierPreferred},
	}
}

func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

// TestSixPersonFixtureRanking is the phase 3 definition of done: a mixed-role,
// mixed-tier fixture whose whole ranking is verified against hand arithmetic.
func TestSixPersonFixtureRanking(t *testing.T) {
	ps, rs := sixPeople(), sixResponses()
	got := Rank([]time.Time{four, early, elev, two, nine}, ps, rs, DefaultConfig())

	// --- expected order, worked through -----------------------------------
	//
	// 09:00  required 1.0, 0.7, 1.0 | optional 0.7, 1.0, 0.3
	//        weights 3(1.0) + 3(0.5) = 4.5
	//        weighted = 2.7 + 0.5(2.0) = 3.7        mean = 0.822222
	//        minRequired = 0.7
	//        base = 0.7(0.822222) + 0.3(0.7) = 0.785556   no penalty
	//
	// 14:00  required 0.3, 0.7, 0.7 | optional 1.0, 1.0, 1.0
	//        weighted = 1.7 + 1.5 = 3.2             mean = 0.711111
	//        minRequired = 0.3 -- Ana is stranded, and the blend says so
	//        base = 0.7(0.711111) + 0.3(0.3) = 0.587778
	//
	// 07:00  everyone preferred: mean 1.0, minRequired 1.0, base 1.0
	//        six people outside sociable hours -> 0.90, capped at 0.45
	//        score = 0.55
	//
	// 11:00  required 0.7, 1.0, 0.7 | optional 0.7, 0.7, and Dev says no
	//        Dev's refusal carries its own zero rather than vanishing, so the
	//        weight stays 3(1.0) + 3(0.5) = 4.5
	//        weighted = 2.4 + 0.5(0 + 0.7 + 0.7) = 3.1   mean = 0.688889
	//        base = 0.7(0.688889) + 0.3(0.7) = 0.692222
	//        Excluding Dev costs score directly, which is why score can lead
	//        the ordering without rewarding exclusion.
	//
	// 16:00  Ana is required and said no -> eliminated, sorts last.
	want := []struct {
		start      time.Time
		label      string
		score      float64
		penalty    float64
		coverage   int
		eliminated bool
	}{
		{nine, "09:00", 0.7855555555555556, 0, 6, false},
		{elev, "11:00", 0.6922222222222222, 0, 5, false},
		{two, "14:00", 0.5877777777777777, 0, 6, false},
		{early, "07:00", 0.55, 0.45, 6, false},
		{four, "16:00", 0, 0, 5, true},
	}

	if len(got) != len(want) {
		t.Fatalf("ranked %d slots, want %d", len(got), len(want))
	}

	for i, w := range want {
		g := got[i]
		if !g.Start.Equal(w.start) {
			t.Fatalf("position %d = %s, want %s (%s)",
				i, g.Start.In(ny).Format("15:04"), w.start.In(ny).Format("15:04"), w.label)
		}
		if g.Eliminated != w.eliminated {
			t.Errorf("%s Eliminated = %v, want %v", w.label, g.Eliminated, w.eliminated)
		}
		if g.Coverage != w.coverage {
			t.Errorf("%s Coverage = %d, want %d", w.label, g.Coverage, w.coverage)
		}
		if !w.eliminated {
			if !closeTo(g.Score, w.score) {
				t.Errorf("%s Score = %.15f, want %.15f", w.label, g.Score, w.score)
			}
			if !closeTo(g.Penalty, w.penalty) {
				t.Errorf("%s Penalty = %.15f, want %.15f", w.label, g.Penalty, w.penalty)
			}
		}
	}
}

// TestFixtureNamesWhoItCosts checks the reporting the UI depends on: a slot
// that survives must still say who it excludes, and a dead one must say who
// killed it.
func TestFixtureNamesWhoItCosts(t *testing.T) {
	ps, rs := sixPeople(), sixResponses()
	cfg := DefaultConfig()

	if got := ScoreSlot(elev, ps, rs, cfg); len(got.Excludes) != 1 || got.Excludes[0] != "Dev" {
		t.Errorf("11:00 Excludes = %v, want [Dev]", got.Excludes)
	}
	if got := ScoreSlot(four, ps, rs, cfg); len(got.EliminatedBy) != 1 || got.EliminatedBy[0] != "Ana" {
		t.Errorf("16:00 EliminatedBy = %v, want [Ana]", got.EliminatedBy)
	}
}

// TestUnsociableDemotesAnOtherwisePerfectSlot isolates the penalty from the
// rest of the fixture: 07:00 is the only slot everyone marked preferred, and it
// still must not lead.
func TestUnsociableDemotesAnOtherwisePerfectSlot(t *testing.T) {
	ps, rs := sixPeople(), sixResponses()

	got := Rank([]time.Time{early, nine}, ps, rs, DefaultConfig())
	if !got[0].Start.Equal(nine) {
		t.Fatalf("07:00 outranked 09:00 despite the penalty")
	}
	if got[1].Penalty != 0.45 {
		t.Fatalf("07:00 Penalty = %v, want the 0.45 cap", got[1].Penalty)
	}

	// Without the penalty the unanimous-preferred slot is genuinely the best,
	// which is what makes the penalty the thing doing the work above.
	noPenalty := DefaultConfig()
	noPenalty.UnsociablePenalty = 0
	if flipped := Rank([]time.Time{early, nine}, ps, rs, noPenalty); !flipped[0].Start.Equal(early) {
		t.Fatalf("with no penalty 07:00 should lead, got %v", flipped[0].Start.In(ny).Format("15:04"))
	}
}

// TestExcludingSomeoneCostsScore is the invariant that lets composite score
// lead the ranking safely.
//
// If an optional refusal simply dropped out of the weighted mean, excluding
// whoever liked a slot least would *raise* its score, and ranking on score
// would prefer the slot that leaves someone out. Because the refusal carries a
// zero instead, the inclusive slot scores higher on its own merits and no
// special ordering rule is needed to protect it.
func TestExcludingSomeoneCostsScore(t *testing.T) {
	ps := []Participant{
		{ID: "ana", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "dev", Name: "Dev", Role: RoleOptional, Loc: ny},
	}
	inclusive, exclusive := slotAt(10), slotAt(15)

	// Dev merely tolerates the inclusive slot and refuses the other. Ana is
	// equally happy with both, so Dev is the only difference between them.
	rs := Responses{
		"ana": {inclusive: TierPreferred, exclusive: TierPreferred},
		"dev": {inclusive: TierIfNeeded, exclusive: TierNo},
	}

	a := ScoreSlot(inclusive, ps, rs, DefaultConfig())
	b := ScoreSlot(exclusive, ps, rs, DefaultConfig())
	if a.Score <= b.Score {
		t.Fatalf("including Dev at if_needed (%.4f) must beat excluding them (%.4f)", a.Score, b.Score)
	}

	got := Rank([]time.Time{exclusive, inclusive}, ps, rs, DefaultConfig())
	if !got[0].Start.Equal(inclusive) {
		t.Fatalf("ranking chose the slot that excludes Dev over the one that includes them")
	}
}
