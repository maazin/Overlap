// Package solver ranks candidate meeting slots and reports whether the outcome
// is already decided despite outstanding responses.
//
// Everything here is pure: no database access, no clock reads, no globals. Time
// enters only through the values you pass in. That makes the whole package
// testable from literal fixtures, which matters because this is the part of
// Overlap most likely to be wrong in ways nobody notices.
package solver

import (
	"math"
	"sort"
	"time"
)

// Tier is how a participant feels about a slot.
type Tier int8

const (
	TierNo Tier = iota
	TierIfNeeded
	TierOK
	TierPreferred
)

// Value maps a tier onto the [0,1] scale used by scoring.
func (t Tier) Value() float64 {
	switch t {
	case TierPreferred:
		return 1.0
	case TierOK:
		return 0.7
	case TierIfNeeded:
		return 0.3
	default:
		return 0.0
	}
}

func (t Tier) String() string {
	switch t {
	case TierPreferred:
		return "preferred"
	case TierOK:
		return "ok"
	case TierIfNeeded:
		return "if_needed"
	default:
		return "no"
	}
}

// Role decides whether a participant can veto a slot.
type Role uint8

const (
	RoleOptional Role = iota
	RoleRequired
)

// Participant is one person invited to the event. Loc is used to evaluate
// whether a slot lands at an unsociable hour for them; a nil Loc is treated
// as UTC.
type Participant struct {
	ID   string
	Name string
	Role Role
	Loc  *time.Location
}

func (p Participant) location() *time.Location {
	if p.Loc == nil {
		return time.UTC
	}
	return p.Loc
}

// Responses maps participant ID to that person's tier per slot start. A missing
// participant, or a missing slot within a present participant, means unknown.
// Unknown is not the same as TierNo and must never be conflated with it.
type Responses map[string]map[time.Time]Tier

func (r Responses) get(pid string, slot time.Time) (Tier, bool) {
	bySlot, ok := r[pid]
	if !ok {
		return TierNo, false
	}
	t, ok := bySlot[slot]
	return t, ok
}

// Config holds the tunable parameters of the scoring model.
type Config struct {
	// Alpha blends group mean against worst-off required participant.
	// 1.0 is pure utilitarian, 0.0 is pure Rawlsian. 0.7 is the default:
	// mostly optimize group happiness, but refuse to strand a required
	// attendee on if_needed when an alternative exists.
	Alpha float64

	// OptionalWeight is how much an optional participant contributes to the
	// mean relative to a required one.
	OptionalWeight float64

	// UnsociablePenalty is subtracted per participant for whom the slot falls
	// outside [DayStartHour, DayEndHour) in their local time, capped at
	// UnsociableCap in total.
	UnsociablePenalty float64
	UnsociableCap     float64
	DayStartHour      int
	DayEndHour        int

	// HistoryEpsilon is how close two composite scores must be before a
	// group's past decisions are allowed to separate them.
	//
	// History is applied as a tiebreak, never as a score term. That is a
	// structural guarantee, not a tuned one: a slot whose score differs by
	// more than HistoryEpsilon can never be overturned by history no matter
	// how strong the pattern. The product may not appear to overrule what
	// people actually entered.
	HistoryEpsilon float64
}

// DefaultConfig returns the parameters described in the PRD.
func DefaultConfig() Config {
	return Config{
		Alpha:             0.7,
		OptionalWeight:    0.5,
		UnsociablePenalty: 0.15,
		UnsociableCap:     0.45,
		DayStartHour:      8,
		DayEndHour:        21,
		HistoryEpsilon:    0.05,
	}
}

// SlotScore is the evaluation of a single candidate slot.
type SlotScore struct {
	Start time.Time

	// Eliminated is true when a required participant said no. Such a slot can
	// never be chosen, whatever its other properties.
	Eliminated   bool
	EliminatedBy []string // names of required participants who said no

	// Excludes lists optional participants who said no. The slot survives but
	// the UI must show who it costs.
	Excludes []string

	// Unknown lists participants who have not answered for this slot.
	Unknown []string

	Coverage int // participants who can attend (tier > no, or unknown excluded)
	Total    int

	Score   float64 // composite, post-penalty; meaningless if Eliminated
	Penalty float64
}

// ScoreSlot evaluates one slot against the current responses.
//
// Unknown responses are excluded from the mean and the minimum rather than
// being assumed. A slot whose required participants have all answered is scored
// on real data; a slot with outstanding required answers is scored on what is
// known so far and flagged through Unknown.
func ScoreSlot(start time.Time, ps []Participant, rs Responses, cfg Config) SlotScore {
	return scoreSlot(start, ps, cfg, rs.get)
}

// scoreSlot is ScoreSlot's real body, taking the lookup as a function rather
// than reading rs directly.
//
// That indirection is what lets the dominance bound computations (boundAll,
// and the per-participant pin in relevantNonResponders) evaluate a slot
// against a hypothetical completion of the unknowns without first building a
// filled copy of the whole response matrix. Rank's ordinary path passes
// rs.get and behaves exactly as before; ScoreSlot's public signature and
// behaviour are unchanged, so every test written against it stays a valid
// regression guard for this refactor.
func scoreSlot(start time.Time, ps []Participant, cfg Config, get func(pid string, at time.Time) (Tier, bool)) SlotScore {
	s := SlotScore{Start: start, Total: len(ps)}

	var (
		weighted, weights float64
		minRequired       = 1.0
		haveRequired      bool
		penalized         int
	)

	for _, p := range ps {
		tier, answered := get(p.ID, start)

		if !answered {
			s.Unknown = append(s.Unknown, p.Name)
			continue
		}

		if tier == TierNo {
			if p.Role == RoleRequired {
				s.Eliminated = true
				s.EliminatedBy = append(s.EliminatedBy, p.Name)
				continue
			}

			// An optional refusal scores zero rather than dropping out of the
			// mean. Dropping it would make excluding somebody *raise* the
			// score: removing a 0.3 from the average lifts it, so the ranking
			// would actively prefer the slot that leaves out whoever liked it
			// least. Counting the refusal as a zero it has to carry is what
			// makes excluding a person cost something.
			s.Excludes = append(s.Excludes, p.Name)
			weights += cfg.OptionalWeight
			continue
		}

		s.Coverage++

		w := cfg.OptionalWeight
		if p.Role == RoleRequired {
			w = 1.0
			haveRequired = true
			if v := tier.Value(); v < minRequired {
				minRequired = v
			}
		}
		weighted += tier.Value() * w
		weights += w

		if unsociable(start, p.location(), cfg) {
			penalized++
		}
	}

	if s.Eliminated || weights == 0 {
		return s
	}

	mean := weighted / weights
	if !haveRequired {
		minRequired = mean // no required responders: the floor term is vacuous
	}

	base := cfg.Alpha*mean + (1-cfg.Alpha)*minRequired

	s.Penalty = float64(penalized) * cfg.UnsociablePenalty
	if s.Penalty > cfg.UnsociableCap {
		s.Penalty = cfg.UnsociableCap
	}

	s.Score = base - s.Penalty
	if s.Score < 0 {
		s.Score = 0
	}
	return s
}

// unsociable reports whether start falls outside working hours in loc.
func unsociable(start time.Time, loc *time.Location, cfg Config) bool {
	h := start.In(loc).Hour()
	return h < cfg.DayStartHour || h >= cfg.DayEndHour
}

// HistoryAffinity reports in [0,1] how well a candidate slot matches a group's
// past decisions. It is keyed on pattern rather than instant, because no two
// events ever share the same absolute slots.
type HistoryAffinity func(slot time.Time) float64

// Rank scores every slot and orders them best first. Eliminated slots are
// retained but sort last, because the UI needs to explain why they died.
func Rank(slots []time.Time, ps []Participant, rs Responses, cfg Config) []SlotScore {
	return RankWithHistory(slots, ps, rs, cfg, nil)
}

// leaderOf finds the current front-runner without ranking and sorting every
// slot, the way Rank does. Analyze only ever needs the single best slot's
// start time, not the full ordered list and its per-slot Excludes/Unknown
// slices Rank builds and immediately discards but for one element.
//
// slots is iterated in the order the caller supplies it, which is always
// chronological (it comes straight from the event's window expansion), and
// strict-greater-only updates the leader on ties, so the earliest slot wins a
// tie exactly as Rank's own start-time tiebreak does -- without history, since
// Analyze has never taken a HistoryAffinity and this preserves that.
func leaderOf(slots []time.Time, ps []Participant, rs Responses, cfg Config) (time.Time, bool) {
	var best SlotScore
	found := false
	for _, s := range slots {
		sc := ScoreSlot(s, ps, rs, cfg)
		if sc.Eliminated {
			continue
		}
		if !found || sc.Score > best.Score {
			best = sc
			found = true
		}
	}
	return best.Start, found
}

// RankWithHistory is Rank with a group's scheduling habits as a tiebreak.
//
// The ordering of the comparisons is the whole design. History is consulted
// only when two slots are within cfg.HistoryEpsilon of each other, so it can
// separate a near-tie but can never overturn a preference the participants
// actually expressed. Adding history as a bonus to Score instead would make
// that guarantee a matter of parameter tuning; here it is structural.
func RankWithHistory(slots []time.Time, ps []Participant, rs Responses, cfg Config, hist HistoryAffinity) []SlotScore {
	out := make([]SlotScore, 0, len(slots))
	for _, s := range slots {
		out = append(out, ScoreSlot(s, ps, rs, cfg))
	}

	// Ordering, in the sequence the build plan specifies: a slot no required
	// participant has vetoed, then composite score, then optional coverage,
	// then the earliest option.
	//
	// Score leads because an optional refusal now costs score directly, so the
	// composite already accounts for who a slot leaves out. Coverage stays as a
	// tiebreak for genuinely close calls, ahead of history so that habit is
	// consulted last and can never outrank a slot more people can attend.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Eliminated != b.Eliminated {
			return !a.Eliminated
		}

		diff := a.Score - b.Score
		if diff > cfg.HistoryEpsilon || diff < -cfg.HistoryEpsilon {
			return diff > 0
		}

		if a.Coverage != b.Coverage {
			return a.Coverage > b.Coverage
		}

		if hist != nil {
			if ha, hb := hist(a.Start), hist(b.Start); ha != hb {
				return ha > hb
			}
		}

		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Start.Before(b.Start)
	})
	return out
}

// Decision is a slot a group previously settled on.
type Decision struct {
	Slot      time.Time
	DecidedAt time.Time
}

// AffinityFromDecisions builds a HistoryAffinity from a group's past decisions,
// bucketed by weekday and local hour and weighted so recent choices count for
// more.
//
// The decay is not optional. Without it a group ossifies on whatever time it
// picked a year ago, and preference drift — someone's Thursday afternoon class
// starting, a teammate changing timezone — gets silently suppressed by the very
// history the feature is meant to be helpful with.
func AffinityFromDecisions(ds []Decision, loc *time.Location, halfLife time.Duration, now time.Time) HistoryAffinity {
	if len(ds) == 0 || halfLife <= 0 {
		return nil
	}
	if loc == nil {
		loc = time.UTC
	}

	type bucket struct {
		day  time.Weekday
		hour int
	}
	key := func(t time.Time) bucket {
		l := t.In(loc)
		return bucket{l.Weekday(), l.Hour()}
	}

	weights := make(map[bucket]float64, len(ds))
	var max float64
	for _, d := range ds {
		age := now.Sub(d.DecidedAt)
		if age < 0 {
			age = 0
		}
		w := math.Pow(0.5, age.Hours()/halfLife.Hours())
		b := key(d.Slot)
		weights[b] += w
		if weights[b] > max {
			max = weights[b]
		}
	}
	if max == 0 {
		return nil
	}

	return func(slot time.Time) float64 {
		return weights[key(slot)] / max
	}
}

// Dominance answers the question that makes group scheduling miserable: can we
// stop waiting?
type Dominance struct {
	// Decidable is true when one slot beats every other slot's best case, so no
	// outstanding response can change the winner.
	Decidable bool

	// Leader is the winning slot when Decidable, otherwise the current front
	// runner on the data so far.
	Leader time.Time

	// BlockingRequired names required participants who have not fully
	// responded. While this is non-empty nothing is ever decidable, because any
	// of them could still veto the leader.
	BlockingRequired []string

	// Relevant names non-responders whose answer could still change which slot
	// wins. Non-responders absent from this list should not be chased.
	Relevant []string
}

// Analyze computes the dominance verdict.
//
// The bound is computed per slot rather than per person, because a participant
// may answer differently for different slots. The pessimistic bound for slot A
// therefore assumes every unknown gives A their worst answer, while the
// optimistic bound for slot B assumes every unknown gives B their best. A slot
// is decidable when its pessimistic bound clears every rival's optimistic one.
func Analyze(slots []time.Time, ps []Participant, rs Responses, cfg Config) Dominance {
	var d Dominance

	if leader, ok := leaderOf(slots, ps, rs, cfg); ok {
		d.Leader = leader
	}

	// A required participant with any unanswered slot can still veto anything.
	for _, p := range ps {
		if p.Role != RoleRequired {
			continue
		}
		for _, s := range slots {
			if _, ok := rs.get(p.ID, s); !ok {
				d.BlockingRequired = append(d.BlockingRequired, p.Name)
				break
			}
		}
	}

	lo := boundAll(slots, ps, rs, cfg, TierNo)
	hi := boundAll(slots, ps, rs, cfg, TierPreferred)

	for i, s := range slots {
		if lo[s].Eliminated {
			continue
		}
		best := true
		for j, other := range slots {
			if i == j {
				continue
			}
			if hi[other].Eliminated {
				continue
			}
			if hi[other].Score > lo[s].Score {
				best = false
				break
			}
		}
		if best {
			d.Decidable = true
			d.Leader = s
			break
		}
	}

	if !d.Decidable {
		d.Relevant = relevantNonResponders(slots, ps, rs, cfg)
	}

	return d
}

// boundAll scores every slot with every unknown response pinned to fill.
//
// This reads directly against rs through a closure rather than first building
// a filled copy of the whole response matrix. Materializing that copy was the
// actual cost here: for a mid-size poll, dominance analysis calls this dozens
// of times per request, and each of the old copies allocated a map per
// participant and inserted an entry per slot -- work an O(1) lookup closure
// does without allocating anything.
func boundAll(slots []time.Time, ps []Participant, rs Responses, cfg Config, fill Tier) map[time.Time]SlotScore {
	get := func(pid string, at time.Time) (Tier, bool) {
		if t, ok := rs.get(pid, at); ok {
			return t, true
		}
		return fill, true
	}
	out := make(map[time.Time]SlotScore, len(slots))
	for _, s := range slots {
		out[s] = scoreSlot(s, ps, cfg, get)
	}
	return out
}

// boundAllPinned is boundAll with one participant's still-unknown slots fixed
// to pin, used to ask "what if this specific person's uncertainty resolved
// one particular way". Everyone else's unknowns still resolve to fill, exactly
// as in boundAll. A slot the pinned participant has actually answered keeps
// their real answer -- the pin only stands in for what they have not said.
func boundAllPinned(
	slots []time.Time, ps []Participant, rs Responses, cfg Config, fill Tier, pinID string, pin Tier,
) map[time.Time]SlotScore {
	get := func(pid string, at time.Time) (Tier, bool) {
		if t, ok := rs.get(pid, at); ok {
			return t, true
		}
		if pid == pinID {
			return pin, true
		}
		return fill, true
	}
	out := make(map[time.Time]SlotScore, len(slots))
	for _, s := range slots {
		out[s] = scoreSlot(s, ps, cfg, get)
	}
	return out
}

// relevantNonResponders reports which outstanding participants can still move
// the outcome, so the UI can chase one person instead of nagging everybody.
//
// The test is whether a person's *uncertainty* widens the field. We compute the
// slots that could still win as things stand, then again with that person's
// answer settled -- once badly, once well. If settling them either way narrows
// the field, their answer is doing work and they are worth a nudge.
//
// Comparing the two settled cases against each other is not enough on its own,
// and getting that wrong produces a contradiction the user can see: a page that
// says nothing can be decided yet while naming nobody to wait for. Someone can
// change the order by refusing one slot and accepting another, and both settled
// cases give them the same answer everywhere, so that manoeuvre is invisible
// unless the unsettled case is in the comparison. The unsettled case is the one
// that bounds each slot independently.
func relevantNonResponders(slots []time.Time, ps []Participant, rs Responses, cfg Config) []string {
	var out []string

	asThingsStand := possibleWinners(slots, ps, rs, cfg)

	for _, p := range ps {
		missing := false
		for _, s := range slots {
			if _, ok := rs.get(p.ID, s); !ok {
				missing = true
				break
			}
		}
		if !missing {
			continue
		}

		worst := possibleWinnersPinned(slots, ps, rs, cfg, p.ID, TierNo)
		best := possibleWinnersPinned(slots, ps, rs, cfg, p.ID, TierPreferred)

		if !sameSet(worst, best) || !sameSet(asThingsStand, worst) || !sameSet(asThingsStand, best) {
			out = append(out, p.Name)
		}
	}
	return out
}

// possibleWinners returns every slot that could still finish first under some
// completion of the remaining unknowns.
func possibleWinners(slots []time.Time, ps []Participant, rs Responses, cfg Config) map[time.Time]bool {
	lo := boundAll(slots, ps, rs, cfg, TierNo)
	hi := boundAll(slots, ps, rs, cfg, TierPreferred)
	return winnersFrom(slots, lo, hi)
}

// possibleWinnersPinned is possibleWinners with one participant's remaining
// uncertainty fixed to a single hypothetical answer in both bounds, so the
// only thing left varying between lo and hi is everyone else's uncertainty.
func possibleWinnersPinned(
	slots []time.Time, ps []Participant, rs Responses, cfg Config, pinID string, pin Tier,
) map[time.Time]bool {
	lo := boundAllPinned(slots, ps, rs, cfg, TierNo, pinID, pin)
	hi := boundAllPinned(slots, ps, rs, cfg, TierPreferred, pinID, pin)
	return winnersFrom(slots, lo, hi)
}

// winnersFrom is the actual "could this still win" test shared by both bound
// computations above: a slot could still finish first if no rival's best case
// beats its own worst case.
func winnersFrom(slots []time.Time, lo, hi map[time.Time]SlotScore) map[time.Time]bool {
	winners := make(map[time.Time]bool)
	for i, s := range slots {
		if hi[s].Eliminated {
			continue
		}
		could := true
		for j, other := range slots {
			if i == j {
				continue
			}
			if lo[other].Eliminated {
				continue
			}
			if lo[other].Score > hi[s].Score {
				could = false
				break
			}
		}
		if could {
			winners[s] = true
		}
	}
	return winners
}

func sameSet(a, b map[time.Time]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
