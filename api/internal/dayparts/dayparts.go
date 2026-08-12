// Package dayparts maps absolute slots onto the coarse day-part grid the input
// model is built around, and expands a coarse selection back into per-slot
// tiers.
//
// The grid is always evaluated in the *responder's* zone, not the organizer's.
// Two people answering the same event see different days and different day
// parts for the same instants, and each is answering about their own morning.
// Getting this wrong is invisible until someone in another country responds.
//
// Like slots and solver, this package is pure.
package dayparts

import (
	"fmt"
	"time"

	"github.com/maazinshaikh/overlap/api/internal/slots"
	"github.com/maazinshaikh/overlap/api/internal/solver"
)

// Block is one of the three coarse buckets a day is divided into.
type Block string

const (
	Morning   Block = "morning"
	Afternoon Block = "afternoon"
	Evening   Block = "evening"
)

// Boundaries between the blocks, as local wall-clock hours.
//
// The web client mirrors these constants to draw the grid. They must agree: if
// the client shows a slot under Afternoon and the server files it under
// Morning, a tap lands on a different set of slots than the one the responder
// saw. web/src/lib/dayparts.ts is the other copy.
const (
	AfternoonStartHour = 12
	EveningStartHour   = 17
)

// Valid reports whether b is a known block.
func (b Block) Valid() bool {
	switch b {
	case Morning, Afternoon, Evening:
		return true
	}
	return false
}

// ParseBlock converts the wire representation.
func ParseBlock(s string) (Block, error) {
	b := Block(s)
	if !b.Valid() {
		return "", fmt.Errorf("dayparts: unknown block %q", s)
	}
	return b, nil
}

// Cell is a coordinate on the coarse grid: one local date, one block.
type Cell struct {
	Date  slots.Date
	Block Block
}

// Of returns the cell an instant falls into when viewed from loc.
func Of(t time.Time, loc *time.Location) Cell {
	l := t.In(loc)
	return Cell{
		Date:  slots.Date{Year: l.Year(), Month: l.Month(), Day: l.Day()},
		Block: blockForHour(l.Hour()),
	}
}

func blockForHour(h int) Block {
	switch {
	case h < AfternoonStartHour:
		return Morning
	case h < EveningStartHour:
		return Afternoon
	default:
		return Evening
	}
}

// Selection is one coarse tap: a tier applied to a whole day part.
type Selection struct {
	Cell Cell
	Tier solver.Tier
}

// Group buckets slots into cells, preserving order within each cell. The web
// client uses the same grouping to lay out the grid.
func Group(starts []time.Time, loc *time.Location) map[Cell][]time.Time {
	out := make(map[Cell][]time.Time)
	for _, t := range starts {
		c := Of(t, loc)
		out[c] = append(out[c], t)
	}
	return out
}

// Expand turns coarse selections into a tier for every slot they cover.
//
// Slots in no selected cell are absent from the result rather than present with
// tier "no". The caller decides what absence means; conflating "not selected"
// with "explicitly refused" at this layer would throw away the distinction
// before anyone can use it.
//
// A later selection for the same cell wins, so a client that sends a cell twice
// gets the last value rather than an arbitrary one.
func Expand(starts []time.Time, loc *time.Location, sel []Selection) map[time.Time]solver.Tier {
	if len(sel) == 0 {
		return map[time.Time]solver.Tier{}
	}

	byCell := make(map[Cell]solver.Tier, len(sel))
	for _, s := range sel {
		byCell[s.Cell] = s.Tier
	}

	out := make(map[time.Time]solver.Tier)
	for _, t := range starts {
		if tier, ok := byCell[Of(t, loc)]; ok {
			out[t] = tier
		}
	}
	return out
}
