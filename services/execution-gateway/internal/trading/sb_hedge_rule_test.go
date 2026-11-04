package trading

import (
	"math"
	"testing"
)

func TestSBDefaults_StraddleThreeIVThirtyEightHalfHedge(t *testing.T) {
	c := sbDefaults(SBConfig{})
	if c.StraddleDiv != 3 || c.HedgeDiv != 38 || c.HedgePct != 50 {
		t.Fatalf("new rule: straddle / %g, IV / %g, hedge %g%%", c.StraddleDiv, c.HedgeDiv, c.HedgePct)
	}
}

func TestSBDefaults_SavedRules(t *testing.T) {
	// Saved before Hedge % existed, on the old defaults: moves over.
	c := sbDefaults(SBConfig{StraddleDiv: 4, HedgeDiv: 57})
	if c.StraddleDiv != 3 || c.HedgeDiv != 38 || c.HedgePct != 50 {
		t.Fatalf("old rule not moved: %g %g %g", c.StraddleDiv, c.HedgeDiv, c.HedgePct)
	}
	// Saved before Hedge % existed with its own divisors: those stay.
	c = sbDefaults(SBConfig{StraddleDiv: 5, HedgeDiv: 40})
	if c.StraddleDiv != 5 || c.HedgeDiv != 40 || c.HedgePct != 50 {
		t.Fatalf("own divisors changed: %g %g %g", c.StraddleDiv, c.HedgeDiv, c.HedgePct)
	}
	// Set after the change: stays, 4 / 57 included, on every later load.
	c = sbDefaults(sbDefaults(SBConfig{StraddleDiv: 4, HedgeDiv: 57, HedgePct: 60}))
	if c.StraddleDiv != 4 || c.HedgeDiv != 57 || c.HedgePct != 60 {
		t.Fatalf("set values changed: %g %g %g", c.StraddleDiv, c.HedgeDiv, c.HedgePct)
	}
	if c = sbDefaults(SBConfig{HedgePct: 150}); c.HedgePct != 100 {
		t.Fatalf("hedge %% above 100: %g", c.HedgePct)
	}
}

func TestHedgePct_HalfOfNetDeltaInWholeLots(t *testing.T) {
	r := hedgeRuleNew.withHedgePct(50)
	d := decideHedge(hedgeDecisionInput{PointsOut: 150, PointsAllowed: 100, Spot: 24000, NetDelta: -1000, LotSize: 65, TradeLots: 64,
		Frac: r.Frac, LeaveBand: r.LeaveBand})
	if !d.Hedge || d.Lots != 7 { // 500 / 65 = 7.69, rounded down
		t.Fatalf("hedge %v, %d lots", d.Hedge, d.Lots)
	}
	if got := r.minuteFrac(150, 100); got != 0.5 {
		t.Fatalf("share %g", got)
	}
	if !hedgeRuleOld.withHedgePct(50).Intra {
		t.Fatal("the intra-minute line must stay on with old tables driving")
	}
	if d := decideHedge(hedgeDecisionInput{PointsOut: 99, PointsAllowed: 100, Spot: 24000, NetDelta: -1000, LotSize: 65, TradeLots: 64,
		Frac: r.Frac, LeaveBand: r.LeaveBand}); d.Hedge {
		t.Fatal("no hedge inside the band")
	}
}

// A trade without Hedge % -- every LUT trade -- sizes exactly as before.
func TestHedgePct_ZeroLeavesTheLUTRuleUnchanged(t *testing.T) {
	for _, rule := range []hedgeRule{hedgeRuleOld, hedgeRuleNew} {
		r := rule.withHedgePct(0)
		if r != rule {
			t.Fatalf("%s rule changed: %+v", rule.Key, r)
		}
		for _, out := range []float64{105, 150, 300} {
			got := decideHedge(hedgeDecisionInput{PointsOut: out, PointsAllowed: 100, Spot: 24000, NetDelta: -2000, LotSize: 65, TradeLots: 192,
				Frac: r.Frac, LeaveBand: r.LeaveBand})
			was := decideHedge(hedgeDecisionInput{PointsOut: out, PointsAllowed: 100, Spot: 24000, NetDelta: -2000, LotSize: 65, TradeLots: 192,
				LeaveBand: rule.LeaveBand})
			if got != was {
				t.Fatalf("%s out %g: %+v, was %+v", rule.Key, out, got, was)
			}
			if a, b := r.minuteFrac(out, 100), rule.minuteFrac(out, 100); math.Abs(a-b) > 1e-12 {
				t.Fatalf("%s out %g: share %g, was %g", rule.Key, out, a, b)
			}
		}
	}
	// The sim that drives a LUT trade carries no Hedge %.
	c := simDriveConfigOf(LUTBuildRun{TradeUID: "T", Minute: "09:18"}, 23800, "new")
	if c.HedgePct != 0 || c.HedgeRule != "new" || paperDefaults(c).StraddleDiv != 4 || paperDefaults(c).HedgeDiv != 57 {
		t.Fatalf("LUT sim config changed: %+v", c)
	}
}

func TestSBHandOff_CarriesTheRulesHedge(t *testing.T) {
	s, r := handoffRunner(t)
	r.cfg.StraddleDiv, r.cfg.HedgeDiv, r.cfg.HedgePct = 3, 38, 50
	r.pms.ApplyFill(PMSFill{Token: 44620, Strike: 22700, OptionType: "CE", Side: "SELL", Qty: 130, Price: 168.3, Role: "BUILD"})
	r.pms.ApplyFill(PMSFill{Token: 44621, Strike: 22700, OptionType: "PE", Side: "SELL", Qty: 130, Price: 146.8, Role: "BUILD"})
	if !s.sbHandOffLocked(r) {
		t.Fatal("expected hand-off")
	}
	defer stopRuntime(s, "SB-T-1")
	tr, _ := s.Store.LoadTrade("SB-T-1")
	if tr.Config.StraddleDiv != 3 || tr.Config.HedgeDiv != 38 || tr.Config.HedgePct != 50 {
		t.Fatalf("hedge not carried: %g %g %g", tr.Config.StraddleDiv, tr.Config.HedgeDiv, tr.Config.HedgePct)
	}
}

func TestApplyTradeHedgePct(t *testing.T) {
	tr := StoredTrade{TradeUID: "SB-RMW1LEQFV1-261104093431"}
	if err := applyTradeHedgePct(&tr, 50); err != nil || tr.Config.HedgePct != 50 {
		t.Fatalf("open Straddle Build trade: %v, %g", err, tr.Config.HedgePct)
	}
	if err := applyTradeHedgePct(&tr, 101); err == nil || tr.Config.HedgePct != 50 {
		t.Fatalf("above 100 must be refused: %v, %g", err, tr.Config.HedgePct)
	}
	if err := applyTradeHedgePct(&tr, 0); err != nil || tr.Config.HedgePct != 0 {
		t.Fatalf("0 goes back to the standard rule: %v, %g", err, tr.Config.HedgePct)
	}

	simDrive.mu.Lock()
	was := simDrive.d
	simDrive.d = &simDriver{TradeUID: "TRD_LUT"}
	simDrive.mu.Unlock()
	defer func() {
		simDrive.mu.Lock()
		simDrive.d = was
		simDrive.mu.Unlock()
	}()
	lut := StoredTrade{TradeUID: "TRD_LUT"}
	if err := applyTradeHedgePct(&lut, 50); err == nil || lut.Config.HedgePct != 0 {
		t.Fatalf("a sim-driven LUT trade must refuse Hedge %%: %v, %g", err, lut.Config.HedgePct)
	}
}

// With Hedge % on an open trade: 50 % of the delta at minute end, the
// complete delta on the intra-minute line, both inside the same allowed.
func TestHedgePct_MinuteHalfIntraComplete(t *testing.T) {
	straddle, spot, iv := 390.0, 23900.0, 0.16
	allowed := math.Min(straddle/3, spot*iv/38) // 130 vs 100.6
	if math.Abs(allowed-100.63) > 0.01 {
		t.Fatalf("allowed %.2f", allowed)
	}
	r := hedgeRuleNew.withHedgePct(50)
	in := hedgeDecisionInput{PointsOut: 110, PointsAllowed: allowed, Spot: spot, NetDelta: -1300, LotSize: 65, TradeLots: 77}
	minute := in
	minute.Frac, minute.LeaveBand = r.Frac, r.LeaveBand
	if d := decideHedge(minute); !d.Hedge || d.Lots != 10 { // 650 of 1,300
		t.Fatalf("minute end: %+v", d)
	}
	intra := in
	intra.PointsOut, intra.Mult, intra.Frac = 125, hedgeIntraMult, hedgeIntraFrac
	if d := decideHedge(intra); !d.Hedge || d.Lots != 20 { // all 1,300
		t.Fatalf("intra-minute: %+v", d)
	}
	intra.PointsOut = 118 // below 1.2 x allowed = 120.76
	if d := decideHedge(intra); d.Hedge {
		t.Fatalf("intra-minute below the line: %+v", d)
	}
}
