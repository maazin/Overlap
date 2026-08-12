package solver

import (
	"testing"
	"time"
)

var (
	ny, _  = time.LoadLocation("America/New_York")
	ldn, _ = time.LoadLocation("Europe/London")
)

func at(hour int) time.Time {
	return time.Date(2026, 11, 5, hour, 0, 0, 0, ny)
}

func people() []Participant {
	return []Participant{
		{ID: "a", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "b", Name: "Ben", Role: RoleRequired, Loc: ny},
		{ID: "c", Name: "Cara", Role: RoleOptional, Loc: ny},
	}
}

func resp(m map[string]map[time.Time]Tier) Responses { return Responses(m) }

// --- scoring -----------------------------------------------------------------

func TestRequiredNoEliminates(t *testing.T) {
	slot := at(10)
	got := ScoreSlot(slot, people(), resp(map[string]map[time.Time]Tier{
		"a": {slot: TierNo},
		"b": {slot: TierPreferred},
		"c": {slot: TierPreferred},
	}), DefaultConfig())

	if !got.Eliminated {
		t.Fatalf("required no must eliminate the slot")
	}
	if len(got.EliminatedBy) != 1 || got.EliminatedBy[0] != "Ana" {
		t.Fatalf("EliminatedBy = %v, want [Ana]", got.EliminatedBy)
	}
}

func TestOptionalNoDoesNotEliminate(t *testing.T) {
	slot := at(10)
	got := ScoreSlot(slot, people(), resp(map[string]map[time.Time]Tier{
		"a": {slot: TierPreferred},
		"b": {slot: TierPreferred},
		"c": {slot: TierNo},
	}), DefaultConfig())

	if got.Eliminated {
		t.Fatalf("optional no must not eliminate")
	}
	if len(got.Excludes) != 1 || got.Excludes[0] != "Cara" {
		t.Fatalf("Excludes = %v, want [Cara]", got.Excludes)
	}
	if got.Coverage != 2 {
		t.Fatalf("Coverage = %d, want 2", got.Coverage)
	}
}

func TestUnknownIsNotNo(t *testing.T) {
	slot := at(10)
	got := ScoreSlot(slot, people(), resp(map[string]map[time.Time]Tier{
		"a": {slot: TierPreferred},
		// Ben and Cara have not answered.
	}), DefaultConfig())

	if got.Eliminated {
		t.Fatalf("unknown answers must never eliminate a slot")
	}
	if len(got.Unknown) != 2 {
		t.Fatalf("Unknown = %v, want 2 entries", got.Unknown)
	}
	if got.Score == 0 {
		t.Fatalf("slot with one preferred answer should score above zero")
	}
}

// TestAlphaChangesWinner constructs the case the blend exists to handle: one
// slot is better on average, the other protects the worst-off required person.
func TestAlphaChangesWinner(t *testing.T) {
	popular, fair := at(10), at(14)
	ps := people()

	rs := resp(map[string]map[time.Time]Tier{
		"a": {popular: TierPreferred, fair: TierOK},
		"b": {popular: TierIfNeeded, fair: TierOK},
		"c": {popular: TierPreferred, fair: TierOK},
	})

	utilitarian := DefaultConfig()
	utilitarian.Alpha = 1.0
	rawlsian := DefaultConfig()
	rawlsian.Alpha = 0.0

	if got := Rank([]time.Time{popular, fair}, ps, rs, utilitarian); !got[0].Start.Equal(popular) {
		t.Fatalf("alpha=1 should favour the higher mean, got %v", got[0].Start)
	}
	if got := Rank([]time.Time{popular, fair}, ps, rs, rawlsian); !got[0].Start.Equal(fair) {
		t.Fatalf("alpha=0 should favour the protected floor, got %v", got[0].Start)
	}
}

func TestUnsociableHourPenalty(t *testing.T) {
	early, normal := at(6), at(14)
	ps := people()
	rs := resp(map[string]map[time.Time]Tier{
		"a": {early: TierPreferred, normal: TierPreferred},
		"b": {early: TierPreferred, normal: TierPreferred},
		"c": {early: TierPreferred, normal: TierPreferred},
	})

	got := Rank([]time.Time{early, normal}, ps, rs, DefaultConfig())
	if !got[0].Start.Equal(normal) {
		t.Fatalf("6am should be demoted below 2pm at equal tiers")
	}
	if got[1].Penalty == 0 {
		t.Fatalf("6am slot should carry a penalty")
	}
}

// TestPenaltyIsPerViewerTimezone guards the bug where local hour is computed in
// the server's zone rather than each participant's.
func TestPenaltyIsPerViewerTimezone(t *testing.T) {
	slot := at(15) // 3pm New York, 8pm London
	ps := []Participant{
		{ID: "a", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "l", Name: "Liam", Role: RoleRequired, Loc: ldn},
	}
	cfg := DefaultConfig()

	if unsociable(slot, ny, cfg) {
		t.Fatalf("3pm New York should be sociable")
	}
	if unsociable(slot, ldn, cfg) {
		t.Fatalf("8pm London should still be sociable under a 21:00 cutoff")
	}

	late := at(17) // 10pm London
	if unsociable(late, ny, cfg) {
		t.Fatalf("5pm New York should be sociable")
	}
	if !unsociable(late, ldn, cfg) {
		t.Fatalf("10pm London should be unsociable")
	}
	_ = ps
}

// --- dominance ---------------------------------------------------------------

func TestDecidableWhenLeaderDominates(t *testing.T) {
	good, bad := at(10), at(16)
	ps := []Participant{
		{ID: "a", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "b", Name: "Ben", Role: RoleRequired, Loc: ny},
		{ID: "c", Name: "Cara", Role: RoleOptional, Loc: ny},
	}

	// Both required people answered. Cara is outstanding but optional, and the
	// gap is wide enough that nothing she says can flip it.
	rs := resp(map[string]map[time.Time]Tier{
		"a": {good: TierPreferred, bad: TierIfNeeded},
		"b": {good: TierPreferred, bad: TierIfNeeded},
	})

	d := Analyze([]time.Time{good, bad}, ps, rs, DefaultConfig())
	if len(d.BlockingRequired) != 0 {
		t.Fatalf("BlockingRequired = %v, want empty", d.BlockingRequired)
	}
	if !d.Decidable {
		t.Fatalf("expected decidable")
	}
	if !d.Leader.Equal(good) {
		t.Fatalf("Leader = %v, want %v", d.Leader, good)
	}
}

// TestRequiredNonResponderBlocksEverything encodes the consequence that falls
// out of the maths: while a required person is silent they could veto anything,
// so nothing is decidable and the UI should name them.
func TestRequiredNonResponderBlocksEverything(t *testing.T) {
	good, bad := at(10), at(16)
	ps := people()

	rs := resp(map[string]map[time.Time]Tier{
		"a": {good: TierPreferred, bad: TierIfNeeded},
		"c": {good: TierPreferred, bad: TierIfNeeded},
		// Ben is required and silent.
	})

	d := Analyze([]time.Time{good, bad}, ps, rs, DefaultConfig())
	if d.Decidable {
		t.Fatalf("must not be decidable while a required person is silent")
	}
	if len(d.BlockingRequired) != 1 || d.BlockingRequired[0] != "Ben" {
		t.Fatalf("BlockingRequired = %v, want [Ben]", d.BlockingRequired)
	}
}

// TestIrrelevantNonResponderIsNotChased is the feature that turns "nudge
// everyone" into "nudge the one person who matters".
func TestIrrelevantNonResponderIsNotChased(t *testing.T) {
	good, bad := at(10), at(16)
	ps := []Participant{
		{ID: "a", Name: "Ana", Role: RoleRequired, Loc: ny},
		{ID: "b", Name: "Ben", Role: RoleRequired, Loc: ny},
		{ID: "c", Name: "Cara", Role: RoleOptional, Loc: ny},
		{ID: "d", Name: "Dev", Role: RoleOptional, Loc: ny},
	}

	// Ben has vetoed the late slot, so only one slot survives no matter what
	// Cara and Dev say. Neither is worth chasing.
	rs := resp(map[string]map[time.Time]Tier{
		"a": {good: TierPreferred, bad: TierPreferred},
		"b": {good: TierPreferred, bad: TierNo},
	})

	d := Analyze([]time.Time{good, bad}, ps, rs, DefaultConfig())
	if !d.Decidable {
		t.Fatalf("one surviving slot should be decidable")
	}
	if len(d.Relevant) != 0 {
		t.Fatalf("Relevant = %v, want empty", d.Relevant)
	}
}

func TestRankPutsEliminatedLast(t *testing.T) {
	dead, alive := at(10), at(14)
	ps := people()
	rs := resp(map[string]map[time.Time]Tier{
		"a": {dead: TierNo, alive: TierIfNeeded},
		"b": {dead: TierPreferred, alive: TierIfNeeded},
		"c": {dead: TierPreferred, alive: TierIfNeeded},
	})

	got := Rank([]time.Time{dead, alive}, ps, rs, DefaultConfig())
	if got[0].Eliminated {
		t.Fatalf("eliminated slot must not rank first even with better tiers")
	}
	if !got[1].Eliminated {
		t.Fatalf("expected the eliminated slot last")
	}
}

func TestEmptyResponsesDoNotPanic(t *testing.T) {
	got := Rank([]time.Time{at(10)}, people(), Responses{}, DefaultConfig())
	if len(got) != 1 {
		t.Fatalf("want one result")
	}
	if got[0].Eliminated {
		t.Fatalf("no responses at all must not eliminate anything")
	}
}

// --- history tiebreak --------------------------------------------------------

// TestHistoryBreaksATie is the intended use: two slots the group feels
// identically about, separated by what they've picked before.
func TestHistoryBreaksATie(t *testing.T) {
	// Same weekday and hour pattern for both; only history distinguishes them.
	a := time.Date(2026, 11, 5, 15, 0, 0, 0, ny) // Thursday 3pm
	b := time.Date(2026, 11, 6, 15, 0, 0, 0, ny) // Friday 3pm
	ps := people()
	rs := resp(map[string]map[time.Time]Tier{
		"a": {a: TierOK, b: TierOK},
		"b": {a: TierOK, b: TierOK},
		"c": {a: TierOK, b: TierOK},
	})

	now := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	hist := AffinityFromDecisions([]Decision{
		{Slot: time.Date(2026, 10, 29, 15, 0, 0, 0, ny), DecidedAt: now.Add(-72 * time.Hour)}, // a Thursday 3pm
	}, ny, 30*24*time.Hour, now)

	got := RankWithHistory([]time.Time{b, a}, ps, rs, DefaultConfig(), hist)
	if !got[0].Start.Equal(a) {
		t.Fatalf("history should break the tie toward Thursday, got %v", got[0].Start)
	}
}

// TestHistoryCannotOverturnStatedPreference is the invariant. However strong
// the pattern, a slot people actually preferred must win.
func TestHistoryCannotOverturnStatedPreference(t *testing.T) {
	habitual := time.Date(2026, 11, 5, 15, 0, 0, 0, ny) // Thursday 3pm, the usual
	better := time.Date(2026, 11, 6, 15, 0, 0, 0, ny)   // Friday 3pm, what people want
	ps := people()
	rs := resp(map[string]map[time.Time]Tier{
		"a": {habitual: TierIfNeeded, better: TierPreferred},
		"b": {habitual: TierIfNeeded, better: TierPreferred},
		"c": {habitual: TierIfNeeded, better: TierPreferred},
	})

	now := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	var ds []Decision
	for i := 1; i <= 20; i++ { // twenty consecutive Thursdays
		ds = append(ds, Decision{
			Slot:      time.Date(2026, 10, 29, 15, 0, 0, 0, ny),
			DecidedAt: now.Add(-time.Duration(i) * 24 * time.Hour),
		})
	}
	hist := AffinityFromDecisions(ds, ny, 30*24*time.Hour, now)

	got := RankWithHistory([]time.Time{habitual, better}, ps, rs, DefaultConfig(), hist)
	if !got[0].Start.Equal(better) {
		t.Fatalf("history overturned a stated preference: got %v", got[0].Start)
	}
}

// TestHistoryDecays guards against a group ossifying on a time it picked long
// ago after preferences have drifted.
func TestHistoryDecays(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	recent := time.Date(2026, 10, 30, 10, 0, 0, 0, ny)  // Friday 10am
	ancient := time.Date(2026, 10, 29, 15, 0, 0, 0, ny) // Thursday 3pm

	hist := AffinityFromDecisions([]Decision{
		{Slot: recent, DecidedAt: now.Add(-24 * time.Hour)},
		{Slot: ancient, DecidedAt: now.Add(-365 * 24 * time.Hour)},
	}, ny, 30*24*time.Hour, now)

	rNew := hist(time.Date(2026, 11, 13, 10, 0, 0, 0, ny)) // a future Friday 10am
	rOld := hist(time.Date(2026, 11, 12, 15, 0, 0, 0, ny)) // a future Thursday 3pm
	if rNew <= rOld {
		t.Fatalf("recent decision should outweigh a year-old one: %v vs %v", rNew, rOld)
	}
}

func TestNoHistoryIsNil(t *testing.T) {
	if AffinityFromDecisions(nil, ny, time.Hour, time.Now()) != nil {
		t.Fatalf("no decisions should yield a nil affinity")
	}
}
