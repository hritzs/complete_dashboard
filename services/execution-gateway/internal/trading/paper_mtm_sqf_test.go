package trading

import (
	"math"
	"testing"
)

// withBook gives every minute a recorded chain row at its ATM with a
// bid/ask spread of `half` around the LTP on each side.
func withBook(mins []simMinute, half float64) []simMinute {
	for i := range mins {
		m := &mins[i]
		m.chain = map[float64]lutChainRow{m.lutK: {K: m.lutK, CE: m.lutCE, PE: m.lutPE,
			CEBid: m.lutCE - half, CEAsk: m.lutCE + half, PEBid: m.lutPE - half, PEAsk: m.lutPE + half}}
	}
	return mins
}

// Flat market: the short straddle decays; the square-off waits until the
// trade can be closed AT THE ASK with MTM >= the level, then closes it
// completely, ending at or above the level.
func TestMTMSqf_CompleteExitAboveLevel(t *testing.T) {
	mins := withBook(mkMinutes(200, func(int) float64 { return 22600 }), 0.5)
	const level = 600.0 // rupees, 2 lots x 65
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 2, HedgeMode: "off", SLBps: 500, TPBps: 500,
		MTMSqfOn: true, MTMSqfLevel: level, MTMSqfUnit: "rs"}, 65, nil)
	if r.Status != "EXITED" || r.ExitReason != "MTM_SQF" || r.MTMSqfLots != 2 {
		t.Fatalf("status %s %s lots %d", r.Status, r.ExitReason, r.MTMSqfLots)
	}
	if r.PnL < level {
		t.Fatalf("final MTM %.2f below the level %.2f", r.PnL, level)
	}
	n := len(r.Series)
	if prev := r.Series[n-2]; prev.ExecPnL >= level {
		t.Fatalf("should have fired a minute earlier: exec MTM %.2f at %s", prev.ExecPnL, prev.Time)
	}
	for _, l := range r.Legs {
		if l.Qty != 0 {
			t.Fatalf("leg still open: %+v", l)
		}
	}
}

// The trigger is the EXECUTABLE MTM (bought back at the ask), not the LTP
// MTM: a level the LTP MTM reaches but the ask-side MTM does not, must not
// fire.
func TestMTMSqf_UsesAskNotLTP(t *testing.T) {
	mins := withBook(mkMinutes(60, func(int) float64 { return 22600 }), 3)
	r0 := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1, HedgeMode: "off", ExitTime: "10:10", SLBps: 500, TPBps: 500}, 65, nil)
	ltpMTM := r0.Series[len(r0.Series)-2].PnL // last minute before the time exit
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1, HedgeMode: "off", ExitTime: "10:10", SLBps: 500, TPBps: 500,
		MTMSqfOn: true, MTMSqfLevel: ltpMTM - 1, MTMSqfUnit: "rs"}, 65, nil)
	if r.ExitReason == "MTM_SQF" {
		t.Fatalf("fired on LTP MTM %.2f; spread 3+3 per straddle x65 keeps the executable MTM below", ltpMTM)
	}
}

// Units convert to rupees for the trade's size at entry.
func TestMTMSqf_UnitsToRupees(t *testing.T) {
	c := PaperSimConfig{MTMSqfLevel: 10}
	for unit, want := range map[string]float64{"rs": 10, "pts": 10 * 130, "bps": 10 * 22600.0 / 10000 * 130} {
		c.MTMSqfUnit = unit
		if got := mtmSqfFloor(c, 22600, 130); math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s: %v want %v", unit, got, want)
		}
	}
}

// 50%: closes half the lots once, the rest runs on to its own exit.
func TestMTMSqf_PartialThenRuns(t *testing.T) {
	mins := withBook(mkMinutes(200, func(int) float64 { return 22600 }), 0.5)
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 4, HedgeMode: "off", SLBps: 500, TPBps: 500, ExitTime: "12:00",
		MTMSqfOn: true, MTMSqfLevel: 300, MTMSqfUnit: "rs", MTMSqfPct: 50}, 65, nil)
	if r.MTMSqfLots != 2 || r.ExitReason == "MTM_SQF" {
		t.Fatalf("lots %d exit %s: want 2 lots closed and the rest left running", r.MTMSqfLots, r.ExitReason)
	}
	if r.ExitReason != "TIME" || r.ExitTime != "12:00" {
		t.Fatalf("the remaining 2 lots should run to the 12:00 time exit, got %s at %s", r.ExitReason, r.ExitTime)
	}
	for _, p := range r.Series {
		if p.Time == r.MTMSqfTime && (p.Event == "" || p.HedgeCheck == "EXITED") {
			t.Fatalf("MTM minute %s: event %q check %s -- a partial must not end the trade", p.Time, p.Event, p.HedgeCheck)
		}
	}
}

// Negative level: "square off above -X" fires as soon as the trade can be
// closed at or above -X.
func TestMTMSqf_NegativeLevel(t *testing.T) {
	mins := withBook(mkMinutes(30, func(int) float64 { return 22600 }), 0.5)
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1, HedgeMode: "off", SLBps: 500, TPBps: 500,
		MTMSqfOn: true, MTMSqfLevel: -10000, MTMSqfUnit: "rs"}, 65, nil)
	if r.ExitReason != "MTM_SQF" || r.ExitTime != "09:17" || r.PnL < -10000 {
		t.Fatalf("exit %s at %s pnl %.2f", r.ExitReason, r.ExitTime, r.PnL)
	}
}

// Off by default: no field set, no MTM exit, no exec MTM shown.
func TestMTMSqf_OffByDefault(t *testing.T) {
	mins := withBook(mkMinutes(60, func(int) float64 { return 22600 }), 0.5)
	r := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1, HedgeMode: "off"}, 65, nil)
	if r.MTMSqfFloor != 0 || r.MTMSqfLots != 0 || r.Series[1].ExecPnL != 0 {
		t.Fatalf("rule fired while off: %+v", r)
	}
}
