package trading

import (
	"context"
	"testing"
)

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
		{"delta below one lot cannot be improved", with(func(i *hedgeDecisionInput) { i.NetDelta = -30 }), false, 0, "DELTA_BELOW_ONE_LOT"},
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
