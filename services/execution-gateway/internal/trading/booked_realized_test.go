package trading

import (
	"math"
	"testing"
)

func bx(leg string, tok int64, side string, qty int64, px float64, phase string) OrderExecution {
	return OrderExecution{Leg: leg, token: tok, Side: side, FilledQty: qty, AvgPrice: px, phase: phase}
}

func bookedTotal(execs []OrderExecution) float64 {
	return totalRealizedPnL(computeLegPnL(execs))
}

// A hedge buying back / re-selling on the SAME strike books nothing.
func TestBooked_HedgeOnSameStrikeBooksNothing(t *testing.T) {
	execs := []OrderExecution{
		bx("CE", 1, "SELL", 130, 150, "BUILD"),
		bx("PE", 2, "SELL", 130, 140, "BUILD"),
		bx("CE", 1, "BUY", 65, 140, "HEDGE"),  // hedge reduces the short CE
		bx("CE", 1, "SELL", 65, 145, "HEDGE"), // and adds back
	}
	if got := bookedTotal(execs); got != 0 {
		t.Fatalf("hedges booked %v -- must be 0 until an exit", got)
	}
}

// A hedge leg on another strike closed by another hedge: unbooked too.
func TestBooked_HedgeLegClosedByHedgeIsUnbooked(t *testing.T) {
	execs := []OrderExecution{
		bx("CE", 1, "SELL", 130, 150, "BUILD"),
		bx("PE", 2, "SELL", 130, 140, "BUILD"),
		bx("CE", 3, "BUY", 65, 100, "HEDGE"),
		bx("CE", 3, "SELL", 65, 120, "HEDGE"), // +1300 on the hedge leg
	}
	if got := bookedTotal(execs); got != 0 {
		t.Fatalf("booked %v before any exit", got)
	}
	// Complete square-off books everything: straddle + the hedge's 1300.
	execs = append(execs,
		bx("CE", 1, "BUY", 130, 145, "SQF"),
		bx("PE", 2, "BUY", 130, 135, "SQF"))
	want := 130*(150-145) + 130*(140-135) + 65*(120-100.0)
	if got := bookedTotal(execs); math.Abs(got-want) > 0.01 {
		t.Fatalf("complete exit booked %v want %v", got, want)
	}
}

// PSQF of half the position books half (pro rata), the rest stays unbooked.
func TestBooked_PartialBooksProRata(t *testing.T) {
	execs := []OrderExecution{
		bx("CE", 1, "SELL", 130, 150, "BUILD"),
		bx("PE", 2, "SELL", 130, 140, "BUILD"),
		bx("CE", 1, "BUY", 65, 140, "PSQF"),
		bx("PE", 2, "BUY", 65, 130, "PSQF"),
	}
	want := 65*(150-140) + 65*(140-130.0)
	if got := bookedTotal(execs); math.Abs(got-want) > 0.01 {
		t.Fatalf("PSQF booked %v want %v", got, want)
	}
}

// Same-strike hedge then exit: books against the position's average cost.
func TestBooked_ExitAfterSameStrikeHedgeUsesAverageCost(t *testing.T) {
	execs := []OrderExecution{
		bx("CE", 1, "SELL", 130, 150, "BUILD"),
		bx("CE", 1, "BUY", 65, 140, "HEDGE"), // position 65 short, cash 19500-9100=10400 -> avg 160
		bx("CE", 1, "BUY", 65, 145, "SQF"),   // books 10400 - 9425 = 975
	}
	if got := bookedTotal(execs); math.Abs(got-975) > 0.01 {
		t.Fatalf("booked %v want 975 (whole leg incl. the hedge, booked only at the exit)", got)
	}
}
