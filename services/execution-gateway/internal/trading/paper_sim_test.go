package trading

import (
	"fmt"
	"math"
	"testing"
)

// mkMinutes builds n minutes from 09:16 with the future following path(i);
// the ATM row's prices come from Black-Scholes at a fixed IV (as "ltp").
func mkMinutes(n int, path func(i int) float64) []simMinute {
	var out []simMinute
	for i := 0; i < n; i++ {
		total := 9*60 + 16 + i
		hm := total/60*100 + total%60
		F := path(i)
		K := math.Round(F/50) * 50
		dte := 1.0 - float64(i)/385
		T := dte / 365
		ce, _, _ := simBlack76(true, F, K, T, 0.14)
		pe, _, _ := simBlack76(false, F, K, T, 0.14)
		out = append(out, simMinute{HHMM: hm, Time: fmt.Sprintf("%02d:%02d", hm/100, hm%100), Future: F, ATM: K, IV: 0.14, DTE: dte,
			TPBps: 23, Answer: "YES", lutCE: ce, lutPE: pe, lutK: K, hasLUT: true})
	}
	return out
}

func TestPaperSim_EntryAndTimeExit(t *testing.T) {
	mins := mkMinutes(30, func(i int) float64 { return 22600 })
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:17", Lots: 1, ExitTime: "09:40"}, 65, nil)
	if r.EntryTime != "09:17" || r.Strike != 22600 || r.Qty != 65 {
		t.Fatalf("entry %s strike %v qty %d", r.EntryTime, r.Strike, r.Qty)
	}
	if math.Abs(r.EntryStraddle-(mins[1].lutCE+mins[1].lutPE)) > 1e-9 || r.EntrySource != "ltp" {
		t.Fatalf("entry straddle %v src %s", r.EntryStraddle, r.EntrySource)
	}
	if r.Status != "EXITED" || r.ExitReason != "TIME" || r.ExitTime != "09:40" {
		t.Fatalf("status %s %s %s", r.Status, r.ExitReason, r.ExitTime)
	}
	if r.PnL <= 0 { // flat market, short straddle earns the decay
		t.Fatalf("flat market pnl %v should be > 0", r.PnL)
	}
}

func TestPaperSim_SLOnBigMove(t *testing.T) {
	mins := mkMinutes(40, func(i int) float64 { return 22600 + float64(i)*12 })
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1, Hedge: false}, 65, nil)
	if r.Status != "EXITED" || r.ExitReason != "SL" {
		t.Fatalf("want SL exit, got %s %s pnl/straddle %.2f (SL %.2f)", r.Status, r.ExitReason, r.PnLPerStraddle, r.SLPoints)
	}
	if r.Reconstructed == 0 {
		t.Fatal("strike left the ATM: those minutes must be marked as BS-reconstructed")
	}
}

func TestPaperSim_HedgesWhenDeltaRuns(t *testing.T) {
	mins := mkMinutes(60, func(i int) float64 { return 22600 + float64(i)*2.5 })
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 4, Hedge: true, SLBps: 200}, 65, nil)
	if len(r.Hedges) == 0 {
		t.Fatalf("expected at least one hedge; last point %+v", r.Series[len(r.Series)-1])
	}
	h := r.Hedges[0]
	if math.Abs(h.DeltaAfter) >= math.Abs(h.DeltaBefore) {
		t.Fatalf("hedge did not reduce delta: %+v", h)
	}
}

func TestPaperSim_NoData(t *testing.T) {
	r := RunPaperSim(nil, PaperSimConfig{Entry: "09:16"}, 65, nil)
	if r.Status != "NO DATA" || r.Error == "" {
		t.Fatalf("%+v", r)
	}
}

// Own first-tick ATM LTP beats an imported row for the same strike; an
// imported row beats Black-Scholes for a strike that was not ATM.
func TestSimPrice_SourceOrder(t *testing.T) {
	m := simMinute{HHMM: 918, Time: "09:18", Future: 22592.9, ATM: 22600, IV: 0.14, DTE: 0.992,
		hasLUT: true, lutK: 22600, lutCE: 62.50, lutPE: 68.70,
		chain: map[float64]lutChainRow{
			22600: {K: 22600, CE: 62.55, PE: 67.45, Src: "minute-straddle-sim"},
			22650: {K: 22650, CE: 40.10, PE: 96.00, Src: "minute-straddle-sim"},
		}}
	if px, _, _, src := simPrice(m, 22600, "CE"); src != "ltp" || px != 62.50 {
		t.Fatalf("ATM: %v from %s, want 62.50 from ltp", px, src)
	}
	if px, d, g, src := simPrice(m, 22650, "PE"); src != "import" || px != 96.00 || d >= 0 || g <= 0 {
		t.Fatalf("non-ATM: %v d %v g %v from %s, want 96.00 from import with greeks", px, d, g, src)
	}
	if _, _, _, src := simPrice(m, 22700, "CE"); src != "bs" {
		t.Fatalf("unrecorded strike from %s, want bs", src)
	}
}

// Live valuation: an open position is valued on "now" and reported RUNNING
// with the option / hedge PnL split and all four greeks.
func TestPaperSim_LiveAndGreeks(t *testing.T) {
	mins := mkMinutes(10, func(i int) float64 { return 22600 })
	live := mins[len(mins)-1]
	live.Time = "09:25:30"
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 2}, 65, &live)
	if r.Status != "RUNNING" || r.Live == nil {
		t.Fatalf("status %s live %v", r.Status, r.Live)
	}
	if r.Live.NetTheta <= 0 || r.Live.NetVega >= 0 || r.Live.NetGamma >= 0 {
		t.Fatalf("short straddle greeks: theta %v (want >0), vega %v (<0), gamma %v (<0)", r.Live.NetTheta, r.Live.NetVega, r.Live.NetGamma)
	}
	if math.Abs(r.Live.PnL-(r.Live.OptionPnL+r.Live.HedgePnL)) > 1e-9 || r.Qty != 130 {
		t.Fatalf("pnl split %v != %v + %v, qty %d", r.Live.PnL, r.Live.OptionPnL, r.Live.HedgePnL, r.Qty)
	}
	if len(r.Live.Legs) != 2 || r.Series[0].Legs != nil {
		t.Fatal("legs only on live / result, not on every series point")
	}
}

// Synthetic hedge: on every minute-end breach the whole delta goes to 0
// with fractional future contracts (no lot rounding), even at 1 lot.
func TestPaperSim_SyntheticHedgeOneLot(t *testing.T) {
	mins := mkMinutes(60, func(i int) float64 { return 22600 + float64(i)*2 })
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1, HedgeMode: "synthetic", SLBps: 500}, 65, nil)
	if len(r.Hedges) == 0 {
		t.Fatal("expected synthetic hedges at 1 lot")
	}
	for _, h := range r.Hedges {
		if h.Mode != "synthetic" || math.Abs(h.DeltaAfter) > 1e-6 {
			t.Fatalf("hedge must neutralise delta: %+v", h)
		}
	}
	last := r.Series[len(r.Series)-1]
	if last.HedgeFut <= 0 || math.Abs(last.HedgeFutPerQty-last.HedgeFut/65) > 1e-12 {
		t.Fatalf("rising market, short straddle: hedge should be long futures, got %v (%v per qty)", last.HedgeFut, last.HedgeFutPerQty)
	}
	if math.Abs(last.PnL-(last.OptionPnL+last.HedgePnL)) > 1e-9 || last.HedgePnL <= 0 {
		t.Fatalf("hedge pnl %v should be > 0 in a rising market (total %v)", last.HedgePnL, last.PnL)
	}
}

// Synthetic mode builds delta-neutral: the entry point already carries the
// hedge, so its net delta is 0 and the first hedge event is ENTRY_NEUTRAL.
func TestPaperSim_DeltaNeutralBuild(t *testing.T) {
	mins := mkMinutes(5, func(i int) float64 { return 22585 }) // off-ATM: entry delta != 0
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1, HedgeMode: "synthetic"}, 1, nil)
	if len(r.Hedges) == 0 || r.Hedges[0].Kind != "ENTRY_NEUTRAL" || r.Hedges[0].Time != "09:16" {
		t.Fatalf("hedges %+v", r.Hedges)
	}
	if math.Abs(r.Series[0].NetDelta) > 1e-9 || r.Series[0].HedgeFut == 0 {
		t.Fatalf("entry point delta %v hedge %v", r.Series[0].NetDelta, r.Series[0].HedgeFut)
	}
}

// Next-expiry view: minutes come only from its own chain file; DTE from the
// chain's expiry on the 09:15-15:40 session, IV from the ATM, TP from them.
func TestLoadSimSet_NextExpiry(t *testing.T) {
	t.Setenv("LUT_DATA_DIR", t.TempDir())
	day := "2026-10-06"
	mk := func(hm int, F float64) *OptionChainSnapshot {
		T := (7 + 360.0/385 - float64(hm-940)/385) / 365
		ce, _, _ := simBlack76(true, F, 22650, T, 0.13)
		pe, _, _ := simBlack76(false, F, 22650, T, 0.13)
		return &OptionChainSnapshot{SyntheticFuture: F, ATM: 22650, Expiry: "13-OCT-26",
			Chain: []OptionChainRow{{Strike: 22650, CELtp: ce, PELtp: pe, CEIV: 13, PEIV: 13}}}
	}
	// futures 22660 / 22665 -> ATM 22650, the strike the chain carries
	lutSaveChainMinuteTo(day, "next", 940, mk(940, 22660))
	lutSaveChainMinuteTo(day, "next", 941, mk(941, 22665))
	mins := lutLoadSimSet(day, "next")
	if len(mins) != 2 || mins[0].Expiry != "13-OCT-26" || mins[0].hasLUT {
		t.Fatalf("next minutes %+v", mins)
	}
	// 06-OCT 09:40 -> 13-OCT: 7 days + (385-25)/385 of today's session = 7.935
	if math.Abs(mins[0].DTE-(7+360.0/385)) > 1e-6 || mins[0].IV <= 0 || mins[0].TPBps <= 0 {
		t.Fatalf("dte %v iv %v tp %v", mins[0].DTE, mins[0].IV, mins[0].TPBps)
	}
	if len(lutLoadSimSet(day, "")) != 0 {
		t.Fatal("next data must not leak into the current view")
	}
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:40", Lots: 1, HedgeMode: "synthetic"}, 1, nil)
	if r.Error != "" || r.Strike != 22650 {
		t.Fatalf("%+v", r)
	}
	if r2 := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1}, 1, nil); r2.Error == "" {
		t.Fatal("09:16 entry with data only from 09:40 must report no data")
	}
}
