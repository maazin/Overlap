package dayparts

import (
	"testing"
	"time"

	"github.com/maazin/Overlap/api/internal/slots"
	"github.com/maazin/Overlap/api/internal/solver"
)

var (
	ny, _  = time.LoadLocation("America/New_York")
	ldn, _ = time.LoadLocation("Europe/London")
	tky, _ = time.LoadLocation("Asia/Tokyo")
)

func utc(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, time.UTC)
}

func TestBlockBoundaries(t *testing.T) {
	for _, tc := range []struct {
		hour int
		want Block
	}{
		{0, Morning}, {8, Morning}, {11, Morning},
		{12, Afternoon}, {16, Afternoon},
		{17, Evening}, {23, Evening},
	} {
		if got := blockForHour(tc.hour); got != tc.want {
			t.Errorf("hour %d -> %s, want %s", tc.hour, got, tc.want)
		}
	}
}

// TestCellIsViewerRelative is the property the whole package exists for. One
// instant is a different day part -- and on this example a different date --
// depending on who is looking.
func TestCellIsViewerRelative(t *testing.T) {
	instant := utc(2026, time.March, 10, 23) // 23:00 UTC

	nyCell := Of(instant, ny)   // 19:00 on the 10th
	tkyCell := Of(instant, tky) // 08:00 on the 11th

	if nyCell.Block != Evening {
		t.Errorf("New York block = %s, want evening", nyCell.Block)
	}
	if tkyCell.Block != Morning {
		t.Errorf("Tokyo block = %s, want morning", tkyCell.Block)
	}
	if nyCell.Date.Day != 10 || tkyCell.Date.Day != 11 {
		t.Errorf("dates = %s / %s, want the 10th and the 11th", nyCell.Date, tkyCell.Date)
	}
}

func TestExpandAppliesTierToWholeCell(t *testing.T) {
	// Three morning slots and one afternoon slot, New York local. March 10 is
	// past the spring transition, so New York is on EDT and the offset is -4.
	starts := []time.Time{
		utc(2026, time.March, 10, 13), // 09:00 EDT
		utc(2026, time.March, 10, 14), // 10:00 EDT
		utc(2026, time.March, 10, 15), // 11:00 EDT
		utc(2026, time.March, 10, 18), // 14:00 EDT
	}

	got := Expand(starts, ny, []Selection{{
		Cell: Cell{Date: slots.Date{Year: 2026, Month: time.March, Day: 10}, Block: Morning},
		Tier: solver.TierOK,
	}})

	if len(got) != 3 {
		t.Fatalf("expanded %d slots, want the 3 in the morning: %v", len(got), got)
	}
	for _, want := range starts[:3] {
		if got[want] != solver.TierOK {
			t.Errorf("%s = %v, want ok", want.Format(time.RFC3339), got[want])
		}
	}
	if _, ok := got[starts[3]]; ok {
		t.Error("the afternoon slot must not be touched by a morning selection")
	}
}

// TestExpandOmitsUnselected pins the decision that this layer does not invent a
// "no". Absence is left for the caller to interpret, because conflating "not
// selected" with "refused" here would destroy the distinction before the solver
// can use it.
func TestExpandOmitsUnselected(t *testing.T) {
	starts := []time.Time{utc(2026, time.March, 10, 14), utc(2026, time.March, 10, 18)}

	got := Expand(starts, ny, nil)
	if len(got) != 0 {
		t.Fatalf("no selections should expand to nothing, got %v", got)
	}
}

// TestExpandUsesResponderZone is the cross-timezone bug this design prevents:
// the same coarse tap covers different instants for two people.
func TestExpandUsesResponderZone(t *testing.T) {
	starts := []time.Time{
		utc(2026, time.March, 10, 14), // 10:00 NY, 14:00 London
		utc(2026, time.March, 10, 18), // 14:00 NY, 18:00 London
	}
	cell := func(b Block) Cell {
		return Cell{Date: slots.Date{Year: 2026, Month: time.March, Day: 10}, Block: b}
	}

	nyMorning := Expand(starts, ny, []Selection{{Cell: cell(Morning), Tier: solver.TierOK}})
	if len(nyMorning) != 1 || nyMorning[starts[0]] != solver.TierOK {
		t.Fatalf("New York morning should cover only the 14:00Z slot, got %v", nyMorning)
	}

	ldnMorning := Expand(starts, ldn, []Selection{{Cell: cell(Morning), Tier: solver.TierOK}})
	if len(ldnMorning) != 0 {
		t.Fatalf("neither slot is a London morning, got %v", ldnMorning)
	}

	ldnEvening := Expand(starts, ldn, []Selection{{Cell: cell(Evening), Tier: solver.TierOK}})
	if len(ldnEvening) != 1 || ldnEvening[starts[1]] != solver.TierOK {
		t.Fatalf("18:00 London is an evening, got %v", ldnEvening)
	}
}

func TestExpandLastSelectionWins(t *testing.T) {
	starts := []time.Time{utc(2026, time.March, 10, 14)}
	c := Cell{Date: slots.Date{Year: 2026, Month: time.March, Day: 10}, Block: Morning}

	got := Expand(starts, ny, []Selection{
		{Cell: c, Tier: solver.TierPreferred},
		{Cell: c, Tier: solver.TierIfNeeded},
	})
	if got[starts[0]] != solver.TierIfNeeded {
		t.Fatalf("got %v, want the later selection to win", got[starts[0]])
	}
}

// TestGroupAcrossSpringForward checks the grid stays coherent on a day that is
// only 23 hours long: every slot still lands in exactly one cell, and the local
// dates do not slide.
func TestGroupAcrossSpringForward(t *testing.T) {
	// 2026-03-08 is the US spring-forward day. 09:00 local is 13:00Z after the
	// transition, and these are the 09:00, 12:00 and 18:00 local slots.
	starts := []time.Time{
		utc(2026, time.March, 8, 13), // 09:00 EDT, morning
		utc(2026, time.March, 8, 16), // 12:00 EDT, afternoon
		utc(2026, time.March, 8, 22), // 18:00 EDT, evening
	}

	got := Group(starts, ny)
	if len(got) != 3 {
		t.Fatalf("want three distinct cells, got %d: %v", len(got), got)
	}
	for c, ts := range got {
		if c.Date.Day != 8 {
			t.Errorf("cell %v landed on day %d, want the 8th", c, c.Date.Day)
		}
		if len(ts) != 1 {
			t.Errorf("cell %v holds %d slots, want 1", c, len(ts))
		}
	}
}

func TestParseBlockRejectsJunk(t *testing.T) {
	if _, err := ParseBlock("lunchtime"); err == nil {
		t.Fatal("want an error for an unknown block")
	}
	for _, b := range []Block{Morning, Afternoon, Evening} {
		got, err := ParseBlock(string(b))
		if err != nil || got != b {
			t.Errorf("ParseBlock(%q) = %v, %v", b, got, err)
		}
	}
}
