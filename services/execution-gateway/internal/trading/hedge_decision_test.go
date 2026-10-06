package trading

import (
	"context"
	"testing"
)

// signedLegGreeksAndPNL folds hedge legs (which can be long OR short, on a
// different strike) into the trade's totals. A sign error here would make
// a hedge look like it ADDS delta instead of offsetting it, so pin both
// directions and its agreement with shortLegGreeks for the short case.
func TestSignedLegGreeksAndPNL_SignsMatchShortLegGreeks(t *testing.T) {
	near := func(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }

	// Short straddle via shortLegGreeks (unsigned short quantities)...
	ce := &OptionChainRow{CEDelta: 0.53, CEGamma: 0.0012, CETheta: -10, CEVega: 8}
	pe := &OptionChainRow{PEDelta: -0.47, PEGamma: 0.0012, PETheta: -9, PEVega: 8}
	wantD, wantG, wantT, wantV := shortLegGreeks(ce, pe, 65, 65)

	// ...must equal the same two legs as SIGNED short quantities (-65).
	cd, cg, ct, cv, _ := signedLegGreeksAndPNL(ce.CEDelta, ce.CEGamma, ce.CETheta, ce.CEVega, 0, 0, -65)
	pd, pg, pt, pv, _ := signedLegGreeksAndPNL(pe.PEDelta, pe.PEGamma, pe.PETheta, pe.PEVega, 0, 0, -65)
	if !near(cd+pd, wantD) || !near(cg+pg, wantG) || !near(ct+pt, wantT) || !near(cv+pv, wantV) {
		t.Fatalf("signed short legs = d%v g%v t%v v%v, want shortLegGreeks d%v g%v t%v v%v",
			cd+pd, cg+pg, ct+pt, cv+pv, wantD, wantG, wantT, wantV)
	}

	// A synthetic-long hedge (BUY CE +65, SELL PE -65) must ADD positive
	// delta -- that's what offsets a short straddle's negative delta.
	hd, _, _, _, _ := signedLegGreeksAndPNL(0.5, 0, 0, 0, 0, 0, 65)
	hpd, _, _, _, _ := signedLegGreeksAndPNL(-0.5, 0, 0, 0, 0, 0, -65)
	if hd <= 0 || hpd <= 0 {
		t.Fatalf("synthetic long hedge delta CE=%v PE=%v, want both positive", hd, hpd)
	}

	// PnL: short profits when price falls, long profits when price rises.
	if _, _, _, _, pnl := signedLegGreeksAndPNL(0, 0, 0, 0, 90, 100, -65); !near(pnl, 650) {
		t.Fatalf("short leg entry 100 -> ltp 90 pnl = %v, want +650", pnl)
	}
	if _, _, _, _, pnl := signedLegGreeksAndPNL(0, 0, 0, 0, 110, 100, 65); !near(pnl, 650) {
		t.Fatalf("long leg entry 100 -> ltp 110 pnl = %v, want +650", pnl)
	}
}

func TestShortLegGreeks_SignsAndHedgeDirection(t *testing.T) {
	// Spot just above the strike: call ITM (delta ~+0.53), put OTM (~-0.47).
	ce := &OptionChainRow{CEDelta: 0.53, CEGamma: 0.0012, CETheta: -10, CEVega: 8}
	pe := &OptionChainRow{PEDelta: -0.47, PEGamma: 0.0012, PETheta: -9, PEVega: 8}

	delta, gamma, theta, vega := shortLegGreeks(ce, pe, 65, 65)

	// Short 65 CE + short 65 PE: position delta = -65*0.53 + 65*0.47 = -3.9.
	if delta > -3.8 || delta < -4.0 {
		t.Fatalf("delta = %v, want about -3.9 (a short straddle with the call ITM is net SHORT delta)", delta)
	}
	if gamma >= 0 {
		t.Fatalf("gamma = %v, want negative for a short book", gamma)
	}
	if theta <= 0 {
		t.Fatalf("theta = %v, want positive: a short book earns decay", theta)
	}
	if vega >= 0 {
		t.Fatalf("vega = %v, want negative for a short book", vega)
	}

	// The whole point of the sign: net short delta must be hedged by BUYING
	// a synthetic (buy CE, sell PE), never by selling more.
	ceSide, peSide, ok := hedgeSidesFromSignedDelta(-65 * 0.1)
	if !ok || ceSide != "BUY" || peSide != "SELL" {
		t.Fatalf("negative delta hedge = %s/%s ok=%v, want BUY/SELL", ceSide, peSide, ok)
	}
}

func TestDecideHedge(t *testing.T) {
	base := hedgeDecisionInput{
		PointsOut: 40, PointsAllowed: 34, Spot: 23400,
		NetDelta: -130, LotSize: 65, TradeLots: 5, MinThresholdBps: 8,
	}
	with := func(f func(*hedgeDecisionInput)) hedgeDecisionInput {
		in := base
		f(&in)
		return in
	}

	cases := []struct {
		name       string
		in         hedgeDecisionInput
		wantHedge  bool
		wantLots   int64
		wantAction string
	}{
		{"within allowance", with(func(i *hedgeDecisionInput) { i.PointsOut = 17 }), false, 0, "OK"},
		{"crossed hedges the whole delta in lots", base, true, 2, "HEDGE_TRIGGERED"},
		{"bps floor lifts a low allowance", with(func(i *hedgeDecisionInput) { i.PointsAllowed = 10; i.PointsOut = 15 }), false, 0, "BELOW_MIN_THRESHOLD"},
		{"past the lifted floor hedges", with(func(i *hedgeDecisionInput) { i.PointsAllowed = 10; i.PointsOut = 20 }), true, 2, "HEDGE_TRIGGERED"},
		{"floor disabled at 0 bps", with(func(i *hedgeDecisionInput) { i.PointsAllowed = 10; i.PointsOut = 15; i.MinThresholdBps = 0 }), true, 2, "HEDGE_TRIGGERED"},
		// Nearest-lot sizing (lot 65): a hedge must never leave |delta|
		// larger than it found it, so sizing rounds DOWN to the lower lot.
		{"delta under one lot is skipped", with(func(i *hedgeDecisionInput) { i.NetDelta = -40 }), false, 0, "DELTA_BELOW_ONE_LOT"},
		{"exactly one lot hedges one", with(func(i *hedgeDecisionInput) { i.NetDelta = -65 }), true, 1, "HEDGE_TRIGGERED"},
		{"1.98 lots rounds down to one", with(func(i *hedgeDecisionInput) { i.NetDelta = -129 }), true, 1, "HEDGE_TRIGGERED"},
		{"2.78 lots rounds down to two (live 2026-09-28: +180.57)", with(func(i *hedgeDecisionInput) { i.NetDelta = 180.57; i.TradeLots = 15 }), true, 2, "HEDGE_TRIGGERED"},
		{"exactly zero delta is skipped", with(func(i *hedgeDecisionInput) { i.NetDelta = 0 }), false, 0, "DELTA_BELOW_ONE_LOT"},
		{"capped at the trade's own lots", with(func(i *hedgeDecisionInput) { i.NetDelta = 650; i.TradeLots = 3 }), true, 3, "HEDGE_TRIGGERED"},
		{"no allowance available", with(func(i *hedgeDecisionInput) { i.PointsAllowed = 0 }), false, 0, "NO_ALLOWANCE_AVAILABLE"},
		{"no spot means no floor", with(func(i *hedgeDecisionInput) { i.Spot = 0; i.PointsAllowed = 10; i.PointsOut = 15 }), true, 2, "HEDGE_TRIGGERED"},
		{"forced test hedges one lot on tiny delta", with(func(i *hedgeDecisionInput) {
			i.ForceOneLotTest = true
			i.ForceRegardless = true
			i.NetDelta = -3
			i.PointsOut = 5
			i.TestPointsFloor = 1
		}), true, 1, "HEDGE_TRIGGERED"},
		{"forced test respects its own floor", with(func(i *hedgeDecisionInput) {
			i.ForceOneLotTest = true
			i.ForceRegardless = true
			i.PointsOut = 0.5
			i.TestPointsFloor = 1
		}), false, 0, "BELOW_TEST_FLOOR"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideHedge(tc.in)
			if got.Hedge != tc.wantHedge || got.Lots != tc.wantLots || got.Action != tc.wantAction {
				t.Fatalf("got hedge=%v lots=%d action=%s, want hedge=%v lots=%d action=%s",
					got.Hedge, got.Lots, got.Action, tc.wantHedge, tc.wantLots, tc.wantAction)
			}
		})
	}
}

func TestDecideHedge_FloorIsBpsOfLiveSpot(t *testing.T) {
	in := hedgeDecisionInput{PointsOut: 1, PointsAllowed: 1, Spot: 23400, MinThresholdBps: 8}
	if got := decideHedge(in).Floor; !almostEqual(got, 18.72) {
		t.Fatalf("floor at 23400 = %v, want 18.72", got)
	}
	in.Spot = 24400
	if got := decideHedge(in).Floor; !almostEqual(got, 19.52) {
		t.Fatalf("floor at 24400 = %v, want 19.52 -- the old hardcoded 19.5 was this, frozen", got)
	}
}

func TestManualHedgeLots_SizesAndBooksMultipleLots(t *testing.T) {
	tr := newTestSquareOffTrade("TRD_HEDGE_TWO_LOTS")
	tr.CEQty, tr.PEQty, tr.Lots = 195, 195, 3
	svc, store, exec := newHedgeTestService(tr, -200)

	if err := svc.ManualHedgeLots(context.Background(), tr.TradeUID, 2); err != nil {
		t.Fatalf("ManualHedgeLots: %v", err)
	}
	for _, in := range exec.submitted {
		if in.Quantity != 130 {
			t.Fatalf("hedge leg %s quantity %d, want 130 (2 lots x 65)", in.LegType, in.Quantity)
		}
	}
	got, _ := store.LoadTrade(tr.TradeUID)
	// negative delta: BUY 130 CE (195 -> 65), SELL 130 PE (195 -> 325)
	if got.CEQty != 65 || got.PEQty != 325 {
		t.Fatalf("CE/PE = %d/%d, want 65/325", got.CEQty, got.PEQty)
	}

	if err := svc.ManualHedgeLots(context.Background(), tr.TradeUID, 0); err == nil {
		t.Fatal("want error for zero lots")
	}
}
