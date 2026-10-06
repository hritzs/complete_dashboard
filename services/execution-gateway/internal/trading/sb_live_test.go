package trading

import (
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"testing"
	"time"
)

func TestSBLotLimit_StrictlyAboveTarget(t *testing.T) {
	// First CE lot, PE has nothing yet (its planned floor 94.25 stands in):
	// bound 170 - 94.25 = 75.75 -> lowest tick strictly above = 75.80,
	// but never below the depth floor 76.00.
	if got := sbLotLimit(170, 0, 0, 94.25, 65, 76.00); got != 76.00 {
		t.Fatalf("limit %v, want 76.00 (floor)", got)
	}
	if got := sbLotLimit(170, 0, 0, 94.25, 65, 70.00); got != 75.80 {
		t.Fatalf("limit %v, want 75.80", got)
	}
	// Exactly on a tick: 170 - 94.30 = 75.70 -> must be 75.75, never 75.70 (equal).
	if got := sbLotLimit(170, 0, 0, 94.30, 65, 0); got != 75.75 {
		t.Fatalf("limit %v, want 75.75 (strictly above, not equal)", got)
	}
	// Second CE lot after one at 77.00, PE avg 92.50: new avg must be > 77.50,
	// so this lot > 78.00 -> 78.05.
	if got := sbLotLimit(170, 65, 77.00, 92.50, 65, 0); got != 78.05 {
		t.Fatalf("limit %v, want 78.05", got)
	}
}

// Every lot fills EXACTLY at its limit (worst case for an IOC sell): once
// both legs have fills, avg CE + avg PE stays strictly above the target.
func TestSBLotLimit_InvariantHolds(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 300; trial++ {
		target := 150 + rng.Float64()*100
		lot := int64(65)
		var ceQ, peQ int64
		var ceAvg, peAvg float64
		ceFloor := math.Round((target/2-5+rng.Float64()*10)*20) / 20
		peFloor := math.Round((target/2-5+rng.Float64()*10)*20) / 20
		for i := 0; i < 20; i++ {
			leg := "CE"
			if rng.Intn(2) == 1 {
				leg = "PE"
			}
			if leg == "CE" {
				other := peAvg
				if peQ == 0 {
					other = peFloor
				}
				px := sbLotLimit(target, ceQ, ceAvg, other, lot, ceFloor)
				ceAvg = (ceAvg*float64(ceQ) + px*float64(lot)) / float64(ceQ+lot)
				ceQ += lot
			} else {
				other := ceAvg
				if ceQ == 0 {
					other = ceFloor
				}
				px := sbLotLimit(target, peQ, peAvg, other, lot, peFloor)
				peAvg = (peAvg*float64(peQ) + px*float64(lot)) / float64(peQ+lot)
				peQ += lot
			}
			if ceQ > 0 && peQ > 0 && !(ceAvg+peAvg > target+1e-9) {
				t.Fatalf("trial %d step %d: avg CE %.4f + PE %.4f = %.4f not above target %.4f", trial, i, ceAvg, peAvg, ceAvg+peAvg, target)
			}
		}
	}
}

func TestSBFlattenOrders_BuysFirst(t *testing.T) {
	v := PMSView{Legs: []PMSLeg{
		{Token: 1, Strike: 22500, OptionType: "CE", Qty: 65},   // long (a hedge) -> SELL
		{Token: 2, Strike: 22500, OptionType: "PE", Qty: -130}, // short -> BUY
		{Token: 3, Strike: 22550, OptionType: "CE", Qty: 0},
	}}
	o := sbFlattenOrders(v)
	if len(o) != 2 || o[0].Side != "BUY" || o[0].Qty != 130 || o[1].Side != "SELL" || o[1].Qty != 65 {
		t.Fatalf("orders %+v", o)
	}
}

func TestPMS_BuildAverages(t *testing.T) {
	p := NewPMS()
	p.ApplyFill(PMSFill{Token: 1, OptionType: "CE", Side: "SELL", Qty: 65, Price: 76.20, Role: "BUILD"})
	p.ApplyFill(PMSFill{Token: 1, OptionType: "CE", Side: "SELL", Qty: 65, Price: 76.86, Role: "BUILD"})
	p.ApplyFill(PMSFill{Token: 2, OptionType: "PE", Side: "SELL", Qty: 130, Price: 94.25, Role: "BUILD"})
	p.ApplyFill(PMSFill{Token: 2, OptionType: "PE", Side: "BUY", Qty: 65, Price: 99, Role: "HEDGE"}) // not build
	ce, pe := p.BuildAverages()
	if math.Abs(ce-76.53) > 1e-9 || pe != 94.25 {
		t.Fatalf("avg %v / %v", ce, pe)
	}
	if v := p.View(nil); math.Abs(v.BuildStraddle-170.78) > 1e-9 {
		t.Fatalf("build straddle %v want 170.78", v.BuildStraddle)
	}
}

func TestSBRounds_PairsTogetherThenExtras(t *testing.T) {
	got := fmt.Sprint(sbRounds(1, 2))
	if got != "[[CE PE] [PE]]" {
		t.Fatalf("rounds %s", got)
	}
	if fmt.Sprint(sbRounds(2, 2)) != "[[CE PE] [CE PE]]" {
		t.Fatal("2+2 must be two pairs")
	}
}

func TestSBPairLimits_StrictlyAboveTarget(t *testing.T) {
	// fresh build: bids 43.05 + 37.30 = 80.35 vs target 80
	x, y, ok := sbPairLimits(80, 0, 0, 0, 0, 65, 43.05, 37.30)
	if !ok || x > 43.05 || y > 37.30 || x+y <= 80+1e-9 {
		t.Fatalf("x=%.2f y=%.2f ok=%v", x, y, ok)
	}
	// at the target exactly: no pair
	if _, _, ok := sbPairLimits(80, 0, 0, 0, 0, 65, 43.00, 37.00); ok {
		t.Fatal("80.00 is not strictly above 80")
	}
	// existing build CE 65@43.05 + PE 65@37.05 (80.10): new pair must keep avg sum > 80
	x, y, ok = sbPairLimits(80, 65, 43.05, 65, 37.05, 65, 42.5, 37.6)
	if !ok {
		t.Fatal("expected a pair")
	}
	if (43.05+x)/2+(37.05+y)/2 <= 80 {
		t.Fatalf("avg sum %.4f not above 80", (43.05+x)/2+(37.05+y)/2)
	}
}

func TestSBCompletionLeg(t *testing.T) {
	// today's stuck case: CE 65, PE 130, delta +34.9 -> complete CE
	v := PMSView{BuildCE: 65, BuildPE: 130, NetDelta: 34.9}
	if leg, ok := sbCompletionLeg(v, 65, 65, 0.49, 0.51); !ok || leg != "CE" {
		t.Fatalf("leg=%s ok=%v", leg, ok)
	}
	// delta-neutral by design (unequal lots, ~0 delta): leave it
	if _, ok := sbCompletionLeg(PMSView{BuildCE: 65, BuildPE: 130, NetDelta: -1}, 130, 65, 0.6, 0.3); ok {
		t.Fatal("no completion when delta is already flat")
	}
	// balanced: nothing to complete
	if _, ok := sbCompletionLeg(PMSView{BuildCE: 130, BuildPE: 130, NetDelta: 20}, 130, 65, 0.5, 0.5); ok {
		t.Fatal("balanced build")
	}
}

// Regression: the paired send held r.mu while waiting for both orders, and
// every order takes r.mu itself -> the run froze (2026-10-06 14:50:58).
// With no broker factory each order returns at once (REJECTED); the tranche
// must still finish and release the runner.
func TestSBExecuteLive_PairDoesNotDeadlock(t *testing.T) {
	t.Setenv("SBUILD_RULES_PATH", filepath.Join(t.TempDir(), "sbuild_rules.json")) // run file goes to a temp dir
	s := &Service{}
	r := newSBRunner(SBConfig{ID: "T1", Symbol: "NIFTY", TargetStraddle: 100, Straddles: 65})
	r.isLive, r.phase, r.lotSize, r.tradeUID = true, "BUILDING", 65, "SB-T1"
	atm := OptionChainRow{Strike: 22700, CEToken: 1, PEToken: 2, CEBid: 60, PEBid: 60}
	plan := DepthEntryPlan{CEQty: 65, PEQty: 65, CEWorst: 55, PEWorst: 55}
	r.mu.Lock()
	s.sbExecuteLiveAsync(r, plan, atm)
	r.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r.mu.TryLock() { // TryLock: a deadlocked runner must fail the test, not hang it
			busy := r.busy
			r.mu.Unlock()
			if !busy {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("live tranche never finished: runner still busy (deadlock)")
}
