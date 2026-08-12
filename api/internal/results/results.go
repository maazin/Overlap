// Package results assembles stored event data into the solver's inputs and
// turns its output into the shape the API reports.
//
// It exists so the solver never has to know what a database row looks like and
// the handlers never have to know how scoring works. The one genuinely subtle
// rule in the whole product lives here: what silence means.
package results

import (
	"time"

	"github.com/maazinshaikh/overlap/api/internal/solver"
	"github.com/maazinshaikh/overlap/api/internal/store"
	"github.com/maazinshaikh/overlap/api/internal/tz"
)

// Input is everything the solver needs, derived from stored rows.
type Input struct {
	Slots        []time.Time
	Participants []solver.Participant
	Responses    solver.Responses
}

// Build converts stored participants and responses into solver inputs.
//
// The rule that matters: a participant who has submitted is treated as having
// answered *every* slot. Storage only holds the tiers they actually expressed,
// so the gaps are filled with "no" here. Someone who has not submitted is left
// genuinely unknown.
//
// Conflating the two would break the product in both directions. If a
// responder's unmentioned slots read as unknown, they would look like they are
// still deciding forever and nothing could ever be settled. If a
// non-responder's silence read as "no", the tool would confidently rule out
// times on behalf of someone who has not spoken.
func Build(ps []store.Participant, rs map[string][]store.Response, starts []time.Time) Input {
	in := Input{
		Slots:        starts,
		Participants: make([]solver.Participant, 0, len(ps)),
		Responses:    make(solver.Responses, len(ps)),
	}

	// Slots keyed by instant. Stored timestamps are canonicalised to UTC while
	// the expansion carries the organizer's zone, so the same moment arrives
	// wearing two different offsets and must not become two different keys.
	canonical := make(map[int64]time.Time, len(starts))
	for _, s := range starts {
		canonical[s.UnixNano()] = s
	}

	for _, p := range ps {
		// A zone that no longer resolves must not take down the whole result;
		// UTC only affects the sociable-hours penalty for that one person.
		loc, err := tz.Load(p.TZ)
		if err != nil {
			loc = time.UTC
		}

		role := solver.RoleOptional
		if p.Role == store.RoleRequired {
			role = solver.RoleRequired
		}

		in.Participants = append(in.Participants, solver.Participant{
			ID:   p.ID,
			Name: p.DisplayName,
			Role: role,
			Loc:  loc,
		})

		if !p.Responded() {
			continue
		}

		bySlot := make(map[time.Time]solver.Tier, len(starts))
		for _, s := range starts {
			bySlot[s] = solver.TierNo
		}
		for _, r := range rs[p.ID] {
			// A response against an instant the window no longer produces is
			// dropped rather than scored. Editing an event's hours can orphan
			// old rows, and a tier nobody can see would silently sway ranking.
			if s, ok := canonical[r.SlotStart.UnixNano()]; ok {
				bySlot[s] = r.Tier
			}
		}
		in.Responses[p.ID] = bySlot
	}

	return in
}

// RespondedCount reports how many participants have submitted.
func RespondedCount(ps []store.Participant) int {
	n := 0
	for _, p := range ps {
		if p.Responded() {
			n++
		}
	}
	return n
}
