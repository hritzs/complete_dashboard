package trading

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"
)

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

// 2026-10-08 12:44: a cushion from earlier fills priced a CE lot at 116.05
// (LTP 145) -> broker RMS reject. Each lot is now limited at the tranche
// plan's own price for its leg; a best bid below it stops the tranche.
func TestSBPlanLimits(t *testing.T) {
	plan := DepthEntryPlan{CEQty: 2080, PEQty: 2080, CEWorst: 144.85, PEWorst: 147.90}
	lim, why := sbPlanLimits([]string{"CE", "PE"}, plan, map[string]float64{"CE": 144.95, "PE": 148.00})
	if why != "" || lim["CE"] != 144.85 || lim["PE"] != 147.90 {
		t.Fatalf("limits must be the plan's prices: %v %q", lim, why)
	}
	if _, why := sbPlanLimits([]string{"CE"}, plan, map[string]float64{"CE": 144.80, "PE": 148.00}); why == "" {
		t.Fatal("best bid below the planned price: stop and re-plan, never sell lower")
	}
	if _, why := sbPlanLimits([]string{"PE"}, DepthEntryPlan{CEWorst: 144.85}, map[string]float64{"PE": 148}); why == "" {
		t.Fatal("a leg with no planned price must not be sent")
	}
}

// L5 (completion lots, not held to the target): the lowest of the top 5 bids.
func TestSBRowL5(t *testing.T) {
	bids := func(p ...float64) *DepthBook {
		d := &DepthBook{}
		for _, x := range p {
			d.Bids = append(d.Bids, DepthLevel{Price: x, Qty: 650})
		}
		return d
	}
	row := OptionChainRow{CEDepth: bids(144.75, 144.70, 144.65, 144.60, 144.55, 144.00), PEDepth: bids(148.35, 148.30)}
	if l5 := sbRowL5(row); l5["CE"] != 144.55 || l5["PE"] != 148.30 {
		t.Fatalf("L5 = lowest of the top 5 bids (level 6 ignored; fewer levels -> deepest shown): %v", l5)
	}
}

// A rejection ends only its tranche; sbMaxRejects in a row stop building.
func TestSBRejectContinuesBuilding(t *testing.T) {
	r := newSBRunner(SBConfig{ID: "T"})
	r.phase = "BUILDING"
	for i := 1; i < sbMaxRejects; i++ {
		r.sbRejectLocked("CE order", "RMS")
		if r.phase != "BUILDING" {
			t.Fatalf("rejection %d must not stop the build", i)
		}
	}
	r.rejects = 0 // a fill in between resets the streak
	r.sbRejectLocked("CE order", "RMS")
	if r.phase != "BUILDING" {
		t.Fatal("streak reset by a fill: still building")
	}
	for i := 0; i < sbMaxRejects; i++ {
		r.sbRejectLocked("CE order", "margin")
	}
	if r.phase != "COMPLETE" {
		t.Fatalf("%d rejections in a row must stop building, phase %s", sbMaxRejects, r.phase)
	}
}

// Stop rule: no more entry orders of any kind; a later quantity raise does
// not resume; a handed-off rule can be stopped too (monitoring untouched).
func TestSBStopRuleNoMoreEntries(t *testing.T) {
	t.Setenv("SBUILD_RULES_PATH", filepath.Join(t.TempDir(), "sbuild_rules.json"))
	s := &Service{}
	r := newSBRunner(SBConfig{ID: "STOPT", Symbol: "NIFTY", TargetStraddle: 290, Straddles: 650})
	r.isLive, r.phase, r.lotSize, r.tradeUID = true, "BUILDING", 65, "SB-STOPT"
	sbEng.mu.Lock()
	if sbEng.runners == nil {
		sbEng.runners = map[string]*sbRunner{}
	}
	sbEng.runners["STOPT"] = r
	sbEng.order = append(sbEng.order, "STOPT")
	sbEng.mu.Unlock()
	defer sbEng.remove("STOPT")
	if err := s.StopStraddleBuild("STOPT"); err != nil {
		t.Fatal(err)
	}
	if r.phase != "COMPLETE" || !r.noEntries {
		t.Fatalf("phase %s noEntries %v", r.phase, r.noEntries)
	}
	if _, _, err := s.SaveSBRule(SBConfig{ID: "STOPT", Symbol: "NIFTY", TargetStraddle: 290, Straddles: 1300}); err != nil {
		t.Fatal(err)
	}
	if r.phase != "COMPLETE" {
		t.Fatalf("a stopped rule must not resume on a raised quantity: phase %s", r.phase)
	}
	r.phase, r.noEntries = sbPhaseHandedOff, false
	if err := s.StopStraddleBuild("STOPT"); err != nil || !r.noEntries || r.phase != sbPhaseHandedOff {
		t.Fatalf("handed-off rule: err %v noEntries %v phase %s", err, r.noEntries, r.phase)
	}
}
