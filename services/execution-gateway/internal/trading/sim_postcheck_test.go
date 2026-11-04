package trading

import (
	"math"
	"strings"
	"testing"
)

func TestSimPostCheckCompare(t *testing.T) {
	// 27 Oct 09:23: sim hedged to -0.039 per straddle at 22,717.15; 12,480 units.
	// Two seconds later live reads -480 at 22,716.30 with book gamma -43.
	want, diff, ok := simPostCheckCompare(-480, -43, 22716.30, -0.039, 22717.15, 12480, 65)
	if math.Abs(want-(-0.039*12480+(-43)*(22716.30-22717.15))) > 1e-6 {
		t.Fatalf("want %.2f", want)
	}
	if !ok || math.Abs(diff) > 65 {
		t.Fatalf("expected a match, diff %.1f", diff)
	}
	// A 23-lot shortfall (1,495) must be flagged.
	if _, _, ok := simPostCheckCompare(-2000, -96, 22718.55, -0.039, 22718.55, 12480, 65); ok {
		t.Fatal("a 23-lot hedge shortfall must be a mismatch")
	}
}

// 4 Nov 09:27: the simulation's one-lot book after its hedge, 64 lots live.
func TestSimPostCheck_SimDeltaIsPerLot(t *testing.T) {
	check := func(liveDelta float64) string {
		store := NewMemoryStore()
		tr := newTestSquareOffTrade("TRD_POSTCHECK")
		tr.Lots, tr.LotSize = 64, 65
		store.SaveTrade(tr)
		store.SaveSnapshot(TradeSnapshot{TradeUID: tr.TradeUID, NetDelta: liveDelta, NetGamma: -6.58, Underlying: 23895.75})
		s := &Service{Store: store}
		out := captureLog(t, func() {
			s.simPostCheck(tr, "RISK_BREACH 09:27", PaperSimHedge{DeltaAfter: -1.7311, Future: 23893.8})
		})
		// Only this trade's lines: another test's delayed check may log meanwhile.
		var own []string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, tr.TradeUID) {
				own = append(own, l)
			}
		}
		return strings.Join(own, "\n")
	}
	if out := check(-156); !strings.Contains(out, " MATCH") || strings.Contains(out, "SIM_MISMATCH") {
		t.Fatalf("a 0.5-lot gap must match:\n%s", out)
	}
	if out := check(-900); !strings.Contains(out, "SIM_MISMATCH") || !strings.Contains(out, "buy 12 synthetic lot(s)") {
		t.Fatalf("a real 12-lot gap must still alert:\n%s", out)
	}
}
