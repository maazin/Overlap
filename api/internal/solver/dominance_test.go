package solver

import (
	"testing"
	"time"
)

// The phase 7 fixtures. Each one is a situation the product claims to handle,
// written as the scenario rather than as a unit of the implementation.

// TestDecideNowWhenFourOfSixHaveAnswered is the headline case: two people are
// still outstanding, and it does not matter what either says.
//
//	lo(good) assumes Eve and Fin both refuse:
//	  weights 3(1.0) + 3(0.5) = 4.5
//	  weighted 3.0 + 0.5(1.0 + 0 + 0) = 3.5   mean 0.777778   minRequired 1.0
//	  base 0.7(0.777778) + 0.3(1.0) = 0.844444
//	hi(bad) assumes both are delighted:
//	  weighted 0.9 + 0.5(0.3 + 1.0 + 1.0) = 2.05   mean 0.455556   minRequired 0.3
//	  base 0.7(0.455556) + 0.3(0.3) = 0.408889
//
// The worst good can do still clears the best bad can do, so the answer is
// already in.
func TestDecideNowWhenFourOfSixHaveAnswered(t *testing.T) {
	good, bad := slotAt(10), slotAt(15)
	ps := sixPeople() // ana/ben/cara required, dev/eve/fin optional

	rs := Responses{
		"ana":  {good: TierPreferred, bad: TierIfNeeded},
		"ben":  {good: TierPreferred, bad: TierIfNeeded},
		"cara": {good: TierPreferred, bad: TierIfNeeded},
		"dev":  {good: TierPreferred, bad: TierIfNeeded},
		// Eve and Fin have not answered.
	}

	d := Analyze([]time.Time{good, bad}, ps, rs, DefaultConfig())

	if len(d.BlockingRequired) != 0 {
		t.Fatalf("BlockingRequired = %v, want empty; every required person answered", d.BlockingRequired)
	}
	if !d.Decidable {
		t.Fatal("expected decidable: nothing Eve or Fin can say changes the order")
	}
	if !d.Leader.Equal(good) {
		t.Fatalf("Leader = %s, want 10:00", d.Leader.In(ny).Format("15:04"))
	}
	if len(d.Relevant) != 0 {
		t.Fatalf("Relevant = %v, want empty; nobody is worth chasing", d.Relevant)
	}

	// The dominance verdict and the ranked list must agree about who is
	// winning, or the page would offer to lock a slot that is not on top.
	if top := Rank([]time.Time{good, bad}, ps, rs, DefaultConfig())[0]; !top.Start.Equal(d.Leader) {
		t.Fatalf("Rank leads with %s but Analyze names %s",
			top.Start.In(ny).Format("15:04"), d.Leader.In(ny).Format("15:04"))
	}
}

// TestWaitingOnOneRequiredPerson is the message the product exists to be able
// to write. While a required person is silent they could still veto anything,
// so nothing is decidable and the UI must be able to name them.
//
// It is also the "only one of three matters" case: Sam, Dev and Eve are all
// outstanding, and only Sam's answer can move anything.
func TestWaitingOnOneRequiredPerson(t *testing.T) {
	good, bad := slotAt(10), slotAt(15)
	ps := []Participant{
		{ID: "ana", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "ben", Name: "Ben", Role: RoleRequired, Loc: ny},
		{ID: "sam", Name: "Sam", Role: RoleRequired, Loc: ny},
		{ID: "cara", Name: "Cara", Role: RoleOptional, Loc: ny},
		{ID: "dev", Name: "Dev", Role: RoleOptional, Loc: ny},
		{ID: "eve", Name: "Eve", Role: RoleOptional, Loc: ny},
	}

	rs := Responses{
		"ana":  {good: TierPreferred, bad: TierIfNeeded},
		"ben":  {good: TierPreferred, bad: TierIfNeeded},
		"cara": {good: TierPreferred, bad: TierIfNeeded},
		// Sam is required and silent. Dev and Eve are optional and silent.
	}

	d := Analyze([]time.Time{good, bad}, ps, rs, DefaultConfig())

	if d.Decidable {
		t.Fatal("nothing can be decided while a required person has not answered")
	}
	if len(d.BlockingRequired) != 1 || d.BlockingRequired[0] != "Sam" {
		t.Fatalf("BlockingRequired = %v, want [Sam]", d.BlockingRequired)
	}
	if len(d.Relevant) != 1 || d.Relevant[0] != "Sam" {
		t.Fatalf("Relevant = %v, want [Sam] only; Dev and Eve cannot change the outcome", d.Relevant)
	}

	// Even undecided, there is a front runner to show.
	if !d.Leader.Equal(good) {
		t.Fatalf("Leader = %s, want the current front runner at 10:00",
			d.Leader.In(ny).Format("15:04"))
	}
}

// TestTwoRequiredSilentNamesBoth checks the message stays truthful when more
// than one person is holding things up.
func TestTwoRequiredSilentNamesBoth(t *testing.T) {
	good, bad := slotAt(10), slotAt(15)
	ps := []Participant{
		{ID: "ana", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "sam", Name: "Sam", Role: RoleRequired, Loc: ny},
		{ID: "raj", Name: "Raj", Role: RoleRequired, Loc: ny},
	}
	rs := Responses{"ana": {good: TierPreferred, bad: TierIfNeeded}}

	d := Analyze([]time.Time{good, bad}, ps, rs, DefaultConfig())

	if len(d.BlockingRequired) != 2 {
		t.Fatalf("BlockingRequired = %v, want both silent required people", d.BlockingRequired)
	}
	seen := map[string]bool{}
	for _, n := range d.BlockingRequired {
		seen[n] = true
	}
	if !seen["Sam"] || !seen["Raj"] {
		t.Fatalf("BlockingRequired = %v, want Sam and Raj", d.BlockingRequired)
	}
}

// TestOptionalSilenceStillAllowsADecision guards against the opposite failure:
// treating every outstanding response as a blocker would reproduce exactly the
// two-day dead zone the feature is meant to remove.
func TestOptionalSilenceStillAllowsADecision(t *testing.T) {
	good, bad := slotAt(10), slotAt(15)
	ps := []Participant{
		{ID: "ana", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "ben", Name: "Ben", Role: RoleRequired, Loc: ny},
		{ID: "cara", Name: "Cara", Role: RoleOptional, Loc: ny},
	}
	rs := Responses{
		"ana": {good: TierPreferred, bad: TierNo},
		"ben": {good: TierPreferred, bad: TierIfNeeded},
	}

	d := Analyze([]time.Time{good, bad}, ps, rs, DefaultConfig())
	if !d.Decidable {
		t.Fatal("an outstanding optional response must not block a decision")
	}
	if len(d.BlockingRequired) != 0 {
		t.Fatalf("BlockingRequired = %v, want empty", d.BlockingRequired)
	}
}

// TestCloseRaceNamesTheOnePersonWhoCanBreakIt is the honest negative: when an
// outstanding answer genuinely could flip the order, say so and name them.
//
// Ana narrowly prefers the first slot, and the margin is inside what one
// optional answer can move:
//
//	lo(a), Cara refusing      base 0.7(0.666667) + 0.3(1.0) = 0.766667
//	hi(b), Cara delighted     base 0.7(0.800000) + 0.3(0.7) = 0.770000
//
// The second slot's best case just clears the first slot's worst case, so
// nothing is settled and Cara is the reason.
//
// This is also the case that a naive relevance check misses. Pinning Cara to
// "no everywhere" and to "preferred everywhere" both leave the first slot
// winning, so comparing only those two would call her irrelevant. What she can
// actually do is refuse one slot and accept the other, and only the unsettled
// bound sees that.
func TestCloseRaceNamesTheOnePersonWhoCanBreakIt(t *testing.T) {
	a, b := slotAt(10), slotAt(11)
	ps := []Participant{
		{ID: "ana", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "cara", Name: "Cara", Role: RoleOptional, Loc: ny},
	}
	rs := Responses{"ana": {a: TierPreferred, b: TierOK}}

	d := Analyze([]time.Time{a, b}, ps, rs, DefaultConfig())
	if d.Decidable {
		t.Fatal("the race is inside what Cara's answer can move")
	}
	if len(d.Relevant) != 1 || d.Relevant[0] != "Cara" {
		t.Fatalf("Relevant = %v, want [Cara]", d.Relevant)
	}
}

// TestGenuineTieNamesNobody is the state that exists between "decided" and
// "waiting on someone", and it needs its own answer.
//
// Both slots are identical to everyone who has answered, so no outstanding
// reply can separate them. Reporting that as "waiting" while naming nobody to
// wait for would be a contradiction the user can see; the truthful message is
// that it is a tie and either will do.
func TestGenuineTieNamesNobody(t *testing.T) {
	a, b := slotAt(10), slotAt(11)
	ps := []Participant{
		{ID: "ana", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "cara", Name: "Cara", Role: RoleOptional, Loc: ny},
	}
	rs := Responses{"ana": {a: TierOK, b: TierOK}}

	d := Analyze([]time.Time{a, b}, ps, rs, DefaultConfig())
	if len(d.Relevant) != 0 {
		t.Fatalf("Relevant = %v, want empty; the slots are interchangeable", d.Relevant)
	}
	if len(d.BlockingRequired) != 0 {
		t.Fatalf("BlockingRequired = %v, want empty", d.BlockingRequired)
	}
	// Undecidable with nobody to chase is precisely a tie, and the caller needs
	// the leader in order to offer it.
	if d.Leader.IsZero() {
		t.Fatal("a tie still has a front runner to offer")
	}
}

// TestNobodyHasAnsweredIsNotDecidable is the empty-state guard.
func TestNobodyHasAnsweredIsNotDecidable(t *testing.T) {
	d := Analyze([]time.Time{slotAt(10), slotAt(11)}, sixPeople(), Responses{}, DefaultConfig())

	if d.Decidable {
		t.Fatal("an event nobody has answered cannot be decided")
	}
	if len(d.BlockingRequired) != 3 {
		t.Fatalf("BlockingRequired = %v, want all three required people", d.BlockingRequired)
	}
}

// TestAllSlotsDeadIsNotDecidable: when every slot is vetoed there is nothing to
// decide, and the caller must not be handed a leader that cannot happen.
func TestAllSlotsDeadIsNotDecidable(t *testing.T) {
	a, b := slotAt(10), slotAt(11)
	ps := []Participant{{ID: "ana", Name: "Ana", Role: RoleRequired, Loc: ny}}
	rs := Responses{"ana": {a: TierNo, b: TierNo}}

	d := Analyze([]time.Time{a, b}, ps, rs, DefaultConfig())
	if d.Decidable {
		t.Fatal("every slot is vetoed; nothing is decidable")
	}
	if !d.Leader.IsZero() {
		t.Fatalf("Leader = %v, want the zero time when no slot survives", d.Leader)
	}
}
