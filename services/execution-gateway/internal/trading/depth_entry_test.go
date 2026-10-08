package trading

import (
	"math"
	"testing"
)

func lv(pairs ...float64) []DepthLevel {
	var out []DepthLevel
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, DepthLevel{Price: pairs[i], Qty: int64(pairs[i+1])})
	}
	return out
}

func baseEntry() DepthEntryInput {
	return DepthEntryInput{
		// CE fillable 1500, PE fillable 1000 at target 278 (the plan's example).
		CEBids:         lv(144.20, 500, 144.00, 500, 143.90, 500),
		PEBids:         lv(134.40, 400, 134.30, 300, 134.10, 300),
		CEDelta:        0.53,
		PEDelta:        -0.47,
		TargetStraddle: 278,
		LotSize:        65,
		Participation:  0.5,
		MaxLevels:      5,
		RemainingQty:   7020,
	}
}

// Plan sections 4-9: fillable 1500/1000 -> 50% 750/500 -> floor 715/455 ->
// PE anchor 455 -> CE 390 -> residual +7.15 -> one price test passes.
func TestPlanDepthEntry_PlanExample(t *testing.T) {
	p := PlanDepthEntry(baseEntry())
	if !p.Authorized {
		t.Fatalf("not authorized: %s\n%s", p.Reason, p)
	}
	if p.CEFillable != 1500 || p.PEFillable != 1000 {
		t.Fatalf("fillable %d/%d, want 1500/1000", p.CEFillable, p.PEFillable)
	}
	if p.CECap != 715 || p.PECap != 455 || p.Anchor != "PE" {
		t.Fatalf("caps %d/%d anchor %s, want 715/455 PE", p.CECap, p.PECap, p.Anchor)
	}
	if p.CEQty != 390 || p.PEQty != 455 {
		t.Fatalf("pair %d/%d, want 390 CE / 455 PE", p.CEQty, p.PEQty)
	}
	if math.Abs(p.ResidualDelta-7.15) > 1e-6 {
		t.Fatalf("residual %v, want +7.15", p.ResidualDelta)
	}
	if !(p.Weighted > 278) || p.CEWorst != 144.20 || p.PEWorst != 134.30 {
		t.Fatalf("weighted %.4f worst %.2f/%.2f", p.Weighted, p.CEWorst, p.PEWorst)
	}
}

// Section 8: remaining 500 < 845 -> the anchor steps down until it fits.
func TestPlanDepthEntry_FitsRemaining(t *testing.T) {
	in := baseEntry()
	in.RemainingQty = 500
	p := PlanDepthEntry(in)
	if !p.Authorized || p.CEQty+p.PEQty > 500 {
		t.Fatalf("got %d/%d authorized=%v", p.CEQty, p.PEQty, p.Authorized)
	}
	if p.PEQty != 260 || p.CEQty != 195 {
		t.Fatalf("pair %d/%d, want 195 CE / 260 PE", p.CEQty, p.PEQty)
	}
}

// The full size's VWAP misses the target: the size steps down until the
// pair's VWAP is above target (fill only as deep as the VWAP allows); each
// leg's limit is the deepest bid that size reaches.
func TestPlanDepthEntry_PriceTestStepsSizeDown(t *testing.T) {
	in := baseEntry()
	in.CEBids = lv(144.20, 100, 144.00, 1000)
	in.PEBids = lv(134.40, 100, 134.10, 1000)
	in.TargetStraddle = 278.25
	p := PlanDepthEntry(in)
	if !p.Authorized || p.CEQty != 260 || p.PEQty != 325 {
		t.Fatalf("want the largest pair whose VWAP clears the target (CE 260 / PE 325): %s", p)
	}
	if p.Weighted <= 278.25 || p.CEWorst != 144.00 || p.PEWorst != 134.10 {
		t.Fatalf("VWAP %.4f, limits CE %.2f / PE %.2f (want the 2nd bids)", p.Weighted, p.CEWorst, p.PEWorst)
	}
	// even one lot each is below target: nothing is sent
	in.CEBids = lv(144.20, 10, 144.00, 1000)
	in.PEBids = lv(134.40, 10, 134.10, 1000)
	if p := PlanDepthEntry(in); p.Authorized {
		t.Fatalf("no size clears the target, must wait: %s", p)
	}
}

// Sections 14-17: after a partial fill left the position at -84.5 delta,
// the next tranche's partner corrects it (built from the real position).
func TestPlanDepthEntry_CorrectsPositionDelta(t *testing.T) {
	in := baseEntry()
	in.PositionDelta = -84.5
	p := PlanDepthEntry(in)
	if !p.Authorized || p.PEQty != 455 || p.CEQty != 195 {
		t.Fatalf("got %d CE / %d PE, want 195 / 455: %s", p.CEQty, p.PEQty, p.Reason)
	}
	if math.Abs(p.ResidualDelta) >= math.Abs(in.PositionDelta) {
		t.Fatalf("residual %v did not improve on %v", p.ResidualDelta, in.PositionDelta)
	}
}

// Never one-sided: if neutralising needs only one leg, no tranche.
func TestPlanDepthEntry_NeverOneSided(t *testing.T) {
	in := baseEntry()
	in.PositionDelta = -400
	if p := PlanDepthEntry(in); p.Authorized {
		t.Fatalf("one-sided correction must not be authorized: %d/%d", p.CEQty, p.PEQty)
	}
}

func TestPlanDepthEntry_Gates(t *testing.T) {
	in := baseEntry()
	in.TargetStraddle = 279 // best bids 144.20 + 134.40 = 278.60
	if p := PlanDepthEntry(in); p.Authorized || p.CEFillable != 0 {
		t.Fatalf("gate must stop before depth: %+v", p)
	}
	in = baseEntry()
	in.RemainingQty = 60 // below one lot
	if p := PlanDepthEntry(in); p.Authorized {
		t.Fatal("remaining below one lot must stop (build complete)")
	}
	in = baseEntry()
	in.PEBids = nil
	if p := PlanDepthEntry(in); p.Authorized {
		t.Fatal("missing depth must stop")
	}
}

// Levels stop counting at the first one that breaks the target.
func TestFillableAgainst_StopsAtFirstBadLevel(t *testing.T) {
	q, used := fillableAgainst(lv(144.2, 100, 143.0, 200, 144.5, 300), 134.4, 278, 5)
	if q != 100 || used != 1 {
		t.Fatalf("got %d over %d levels, want 100 over 1", q, used)
	}
}

func TestTrancheLotSequence_InterleavedAndSpread(t *testing.T) {
	seq := TrancheLotSequence(6, 7)
	if len(seq) != 13 {
		t.Fatalf("len %d", len(seq))
	}
	ce, pe, maxGap := 0, 0, 0
	for _, s := range seq {
		if s == "CE" {
			ce++
		} else {
			pe++
		}
		if d := ce - pe; d > maxGap || -d > maxGap {
			if d < 0 {
				d = -d
			}
			maxGap = d
		}
	}
	if ce != 6 || pe != 7 {
		t.Fatalf("counts %d/%d", ce, pe)
	}
	if maxGap > 1 {
		t.Fatalf("legs drifted %d lots apart mid-tranche: %v", maxGap, seq)
	}
	// Uneven: 2 CE + 6 PE -> CE lots spread, never more than ~3 PE in a row.
	seq = TrancheLotSequence(2, 6)
	run, worst := 0, 0
	for _, s := range seq {
		if s == "PE" {
			run++
		} else {
			run = 0
		}
		if run > worst {
			worst = run
		}
	}
	if worst > 3 {
		t.Fatalf("PE bunched (%d in a row): %v", worst, seq)
	}
}

// 6500 straddles = 13000 contracts; tail of 2 lots goes where delta falls.
func TestPlanDepthEntry_TailCutsNetDelta(t *testing.T) {
	in := baseEntry()
	in.RemainingQty = 130 // 2 lots left of the 2 x straddles total
	in.PositionDelta = 40 // net long delta: selling CE (-0.53 each) reduces it
	p := PlanDepthEntry(in)
	if !p.Authorized || !p.Tail {
		t.Fatalf("tail not authorized: %s\n%s", p.Reason, p)
	}
	// Options: 130 CE -> 40-68.9=-28.9; 65+65 -> 40-34.45+30.55=36.1; 130 PE -> 101.1.
	if p.CEQty != 130 || p.PEQty != 0 {
		t.Fatalf("got CE %d PE %d, want 130 CE (|delta| 28.9 is lowest)", p.CEQty, p.PEQty)
	}
	if p.CEQty+p.PEQty != in.RemainingQty {
		t.Fatal("tail must fill exactly the remaining quantity")
	}
	// Short delta: 1 lot left -> PE.
	in.RemainingQty, in.PositionDelta = 65, -20
	p = PlanDepthEntry(in)
	if !p.Authorized || p.PEQty != 65 || p.CEQty != 0 {
		t.Fatalf("got CE %d PE %d, want 65 PE", p.CEQty, p.PEQty)
	}
}
