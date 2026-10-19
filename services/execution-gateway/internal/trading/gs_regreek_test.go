package trading

import (
	"math"
	"testing"
	"time"
)

// The backtest's greeks: one IV per strike, from the OTM leg, both legs at it.
func TestSimStrikeIVIsOTMLeg(t *testing.T) {
	F, K, T := 23121.55, 22950.0, (3+321.0/385)/365
	put, _ := lutImpliedVol(false, 51.70, F, K, T)
	if iv := simStrikeIV(F, K, T, 223.15, 51.70); math.Abs(iv-put) > 1e-9 {
		t.Fatalf("K < F: strike IV %.5f, want the put's %.5f", iv, put)
	}
	call, _ := lutImpliedVol(true, 40, 23121.55, 23300, T)
	if iv := simStrikeIV(F, 23300, T, 40, 220); math.Abs(iv-call) > 1e-9 {
		t.Fatalf("K > F: strike IV %.5f, want the call's %.5f", iv, call)
	}
}

// 16 Oct 10:19: the overlay re-prices 22950 at the GreekSoft closes and drops
// the feed's greeks; the sim then uses the OTM put's IV on both legs.
func TestGSOverlayRegreeksAtCloseOTMIV(t *testing.T) {
	mins := map[string]lutChainMinute{"10:19": {Time: "10:19", Future: 23121.55, ATM: 23100, Rows: []lutChainRow{
		{K: 22950, CE: 221.50, PE: 52.00, CEDelta: 0.7195, PEDelta: -0.2806, CEGamma: 0.001141, PEGamma: 0.001141, CEIV: 12.47, PEIV: 12.47},
		{K: 23300, CE: 40, PE: 220, CEDelta: 0.2, PEDelta: -0.8, CEGamma: 0.001, PEGamma: 0.001},
	}}}
	gsOverlayApplyWith(gsOverlay{"10:19": {"22950": {223.15, 51.70}}}, "", mins)
	r := mins["10:19"].Rows[0]
	if r.CE != 223.15 || r.PE != 51.70 || r.CEDelta != 0 || r.PEDelta != 0 || r.CEIV != 0 {
		t.Fatalf("overlaid strike keeps the feed's greeks: %+v", r)
	}
	if o := mins["10:19"].Rows[1]; o.CE != 40 || o.CEDelta != 0 {
		t.Fatalf("strike without a close keeps its feed price but drops the feed greeks: %+v", o)
	}
	m := simMinute{Future: 23121.55, DTE: 3 + 321.0/385, chain: map[float64]lutChainRow{22950: r}}
	_, dc, gc, _ := simPrice(m, 22950, "CE")
	_, dp, gp, _ := simPrice(m, 22950, "PE")
	// Both legs at the put's 12.55%: straddle delta -0.4418, out 49.5 > 48.34 at the 10:19 hedge held.
	sd := -(dc + dp)
	if math.Abs(sd-(-0.4418)) > 0.0005 || math.Abs(gc-gp) > 1e-12 {
		t.Fatalf("straddle delta %.4f (want -0.4418), gammas %.6f / %.6f should match", sd, gc, gp)
	}
	if out := math.Abs((sd + 0.32995) / (2 * gc)); out < 48.34 {
		t.Fatalf("points out %.2f should be over 48.34 (the 10:19 hedge)", out)
	}
}

func TestGSRegreekLiveOTMIV(t *testing.T) {
	b := time.Date(2026, 10, 16, 10, 19, 0, 0, lutIST())
	cc := &OptionChainSnapshot{Expiry: "19-OCT-26", SyntheticFuture: 23121.55, Chain: []OptionChainRow{
		{Strike: 22950, CEToken: 1, PEToken: 2, CELtp: 223.15, PELtp: 51.70, CEDelta: 0.7195, PEDelta: -0.2806, CEGamma: 0.001141, PEGamma: 0.001141},
		{Strike: 23300, CEToken: 3, PEToken: 4, CELtp: 40, PELtp: 220, CEDelta: 0.2, PEDelta: -0.8},
	}}
	gsRegreek(cc, b, map[int64]float64{1: 221.5, 2: 52.0})
	r := cc.Chain[0]
	if sd := -(r.CEDelta + r.PEDelta); math.Abs(sd-(-0.442)) > 0.002 || r.CEIV != r.PEIV || r.CEGamma != r.PEGamma {
		t.Fatalf("live regreek: straddle delta %.4f IV %.2f/%.2f", sd, r.CEIV, r.PEIV)
	}
	if o := cc.Chain[1]; o.CEDelta != 0.2 || o.PEDelta != -0.8 {
		t.Fatalf("untouched strike changed: %+v", o)
	}
}

// A strike GreekSoft has no candle for keeps its feed price, but its greeks
// are still recomputed from that price -- never the feed's stored ones.
func TestGSOverlayDropsGreeksEverywhere(t *testing.T) {
	mins := map[string]lutChainMinute{"12:43": {Time: "12:43", Future: 23050, ATM: 23050, Rows: []lutChainRow{
		{K: 22950, CE: 213.15, PE: 44.2, CEDelta: 0.7, PEDelta: -0.3, CEGamma: 0.001, PEGamma: 0.001, CEIV: 12.4, PEIV: 12.4}}}}
	gsOverlayApplyWith(gsOverlay{"12:43": {"23000": {171, 59}}}, "", mins)
	r := mins["12:43"].Rows[0]
	if r.CE != 213.15 || r.PE != 44.2 {
		t.Fatalf("no GreekSoft close: the feed price stays, got %+v", r)
	}
	if r.CEDelta != 0 || r.PEDelta != 0 || r.CEIV != 0 {
		t.Fatalf("the feed's stored greeks must be dropped: %+v", r)
	}
}
