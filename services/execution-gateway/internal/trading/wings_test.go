package trading

import (
	"testing"
	"time"
)

func wingTestChain() OptionChainSnapshot {
	var rows []OptionChainRow
	for k := 22000.0; k <= 24000; k += 50 {
		rows = append(rows, OptionChainRow{Strike: k, CEToken: int64(k) * 10, PEToken: int64(k)*10 + 1})
	}
	return OptionChainSnapshot{Chain: rows, ATM: 23000}
}

func TestStrikeStepFromChain(t *testing.T) {
	if got := strikeStepFromChain(wingTestChain()); got != 50 {
		t.Fatalf("step = %v, want 50", got)
	}
}

// The user's worked example: 23000 at 2% -> 460 away -> PE 22540 -> 22550,
// CE 23460 -> 23450.
func TestWingTargetStrike_UserExample(t *testing.T) {
	if got := wingTargetStrike(23000, 2, 50, "PE"); got != 22550 {
		t.Fatalf("PE wing = %v, want 22550", got)
	}
	if got := wingTargetStrike(23000, 2, 50, "CE"); got != 23450 {
		t.Fatalf("CE wing = %v, want 23450", got)
	}
	// A tiny % that rounds back onto the strike still goes one step OTM.
	if got := wingTargetStrike(23000, 0.05, 50, "CE"); got != 23050 {
		t.Fatalf("tiny CE wing = %v, want 23050", got)
	}
	if got := wingTargetStrike(23000, 0.05, 50, "PE"); got != 22950 {
		t.Fatalf("tiny PE wing = %v, want 22950", got)
	}
}

func TestFindWingRow_SkipsNonWingTokensFurtherOTM(t *testing.T) {
	chain := wingTestChain()
	row, err := findWingRow(chain, 23450, "CE", nil)
	if err != nil || row.Strike != 23450 {
		t.Fatalf("got %+v err=%v, want 23450", row, err)
	}
	// 23450 CE is a hedge leg of this trade -> never net a wing into it.
	row, err = findWingRow(chain, 23450, "CE", map[int64]bool{234500: true})
	if err != nil || row.Strike != 23500 {
		t.Fatalf("got %+v err=%v, want 23500 (one step further OTM)", row, err)
	}
	row, err = findWingRow(chain, 22550, "PE", map[int64]bool{225501: true})
	if err != nil || row.Strike != 22500 {
		t.Fatalf("got %+v err=%v, want 22500 (PE further OTM is lower)", row, err)
	}
	// Target beyond the listed chain -> error, nothing guessed.
	if _, err := findWingRow(chain, 24500, "CE", nil); err == nil {
		t.Fatal("want error when no strike at/beyond target is listed")
	}
}

func TestAllocateWingSells_LIFOAndNeverOversell(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	holdings := []wingHolding{
		{Token: 1, OptionType: "CE", Strike: 23450, Qty: 2485, LastBuyAt: t0},                    // build
		{Token: 2, OptionType: "CE", Strike: 23550, Qty: 130, LastBuyAt: t0.Add(time.Hour)},      // hedge 1
		{Token: 3, OptionType: "PE", Strike: 22550, Qty: 2420, LastBuyAt: t0.Add(2 * time.Hour)}, // other type
	}

	plan, short := allocateWingSells(holdings, "CE", 200)
	if short != 0 || len(plan) != 2 || plan[0].Holding.Token != 2 || plan[0].Qty != 130 || plan[1].Holding.Token != 1 || plan[1].Qty != 70 {
		t.Fatalf("plan=%+v short=%d, want newest (token 2) 130 then token 1 70", plan, short)
	}

	plan, short = allocateWingSells(holdings, "CE", 5000)
	var total int64
	for _, p := range plan {
		total += p.Qty
	}
	if total != 2615 || short != 5000-2615 {
		t.Fatalf("sold %d shortfall %d, want capped at held 2615", total, short)
	}

	if plan, short := allocateWingSells(holdings, "CE", 0); len(plan) != 0 || short != 0 {
		t.Fatalf("zero qty -> plan %+v short %d", plan, short)
	}
}

// Hedge example from the spec: hedge BUYs x CE and SELLs x PE -> CE short
// shrinks (sell x CE wing), PE short grows (buy x PE wing).
func TestShortChangeFromFill(t *testing.T) {
	if got := shortChangeFromFill("BUY", 130); got != -130 {
		t.Fatalf("BUY 130 -> %d, want -130", got)
	}
	if got := shortChangeFromFill("SELL", 130); got != 130 {
		t.Fatalf("SELL 130 -> %d, want +130", got)
	}
}

func TestComputeLegPnL_IgnoresWingOrders(t *testing.T) {
	execs := []OrderExecution{
		{Leg: "CE", Side: "SELL", FilledQty: 65, AvgPrice: 100, phase: "BUILD", token: 10},
		{Leg: "CE", Side: "BUY", FilledQty: 65, AvgPrice: 90, phase: "SQF", token: 10},
		{Leg: "CE", Side: "BUY", FilledQty: 65, AvgPrice: 5, phase: "WING", token: 20},
		{Leg: "CE", Side: "SELL", FilledQty: 65, AvgPrice: 1, phase: "WING", token: 20},
	}
	if got := totalRealizedPnL(computeLegPnL(execs)); got != 650 {
		t.Fatalf("realized = %v, want 650 (wing -260 excluded)", got)
	}
	if k := classifyExecutionKind("WING", ""); k != "WING" {
		t.Fatalf("kind = %s, want WING", k)
	}
}

func TestBuildRiskConfig_WingPct(t *testing.T) {
	now := time.Now()
	if err := (&BuildRiskConfig{WingPct: -1}).Validate(now); err == nil {
		t.Fatal("negative wing_pct must be rejected")
	}
	if err := (&BuildRiskConfig{WingPct: 25}).Validate(now); err == nil {
		t.Fatal("wing_pct above limit must be rejected")
	}
	var cfg MonitorConfig
	if err := applyBuildRiskConfig(&cfg, &BuildRiskConfig{WingPct: 2}, now); err != nil || cfg.WingPct != 2 {
		t.Fatalf("WingPct=%v err=%v, want 2", cfg.WingPct, err)
	}
}

func TestComputeWingLegPnL_SeparateFromTrade(t *testing.T) {
	execs := []OrderExecution{
		{Leg: "CE", Side: "SELL", FilledQty: 65, AvgPrice: 100, phase: "BUILD", token: 10},
		{Leg: "CE", Side: "BUY", FilledQty: 65, AvgPrice: 90, phase: "SQF", token: 10},
		{Leg: "CE", Side: "BUY", FilledQty: 130, AvgPrice: 17.80, phase: "WING", token: 20},
		{Leg: "CE", Side: "SELL", FilledQty: 130, AvgPrice: 19.30, phase: "WING", token: 20},
	}
	if got := totalRealizedPnL(computeWingLegPnL(execs)); got != 195 {
		t.Fatalf("wing realized = %v, want 195", got)
	}
	if got := totalRealizedPnL(computeLegPnL(execs)); got != 650 {
		t.Fatalf("trade realized = %v, want 650 (wings excluded)", got)
	}
	if got := pnlPerStraddle(-731.25, 2, 65); got != -5.63 {
		t.Fatalf("pnl/straddle = %v, want -5.63", got)
	}
	if got := pnlPerStraddle(100, 0, 65); got != 0 {
		t.Fatalf("pnl/straddle with no size = %v, want 0", got)
	}
}
