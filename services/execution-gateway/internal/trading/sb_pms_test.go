package trading

import (
	"math"
	"testing"
)

func TestPMS_VerifiedFillsOnly_DeltaAndPnL(t *testing.T) {
	p := NewPMS()
	// Tranche at 22550: 1 lot CE + 1 lot PE sold.
	p.ApplyFill(PMSFill{Token: 1, Strike: 22550, OptionType: "CE", Side: "SELL", Qty: 65, Price: 100, Role: "BUILD"})
	p.ApplyFill(PMSFill{Token: 2, Strike: 22550, OptionType: "PE", Side: "SELL", Qty: 65, Price: 90, Role: "BUILD"})
	// Next tranche at a new ATM 22600: CE only filled so far.
	p.ApplyFill(PMSFill{Token: 3, Strike: 22600, OptionType: "CE", Side: "SELL", Qty: 65, Price: 80, Role: "BUILD"})
	chain := &OptionChainSnapshot{Chain: []OptionChainRow{
		{Strike: 22550, CEToken: 1, PEToken: 2, CEAsk: 95, PEAsk: 92, CEDelta: 0.5, PEDelta: -0.5, CEGamma: 0.001, PEGamma: 0.001},
		{Strike: 22600, CEToken: 3, PEToken: 4, CEAsk: 78, CEDelta: 0.45, PEDelta: -0.55},
	}}
	v := p.View(chain)
	if v.BuildCE != 130 || v.BuildPE != 65 || v.FilledStraddles != 97.5 {
		t.Fatalf("build %d/%d filled %v", v.BuildCE, v.BuildPE, v.FilledStraddles)
	}
	// delta = -65*0.5 + -65*(-0.5) + -65*0.45 = -29.25
	if math.Abs(v.NetDelta+29.25) > 1e-9 {
		t.Fatalf("net delta %v, want -29.25", v.NetDelta)
	}
	// pnl = (100-95)*65 + (90-92)*65 + (80-78)*65 = 325 -130 +130 = 325
	if math.Abs(v.PnL-325) > 1e-9 {
		t.Fatalf("pnl %v, want 325", v.PnL)
	}
	// A hedge BUY on CE 22550 reduces that short and realizes PnL.
	p.ApplyFill(PMSFill{Token: 1, Strike: 22550, OptionType: "CE", Side: "BUY", Qty: 65, Price: 95, Role: "HEDGE"})
	v = p.View(chain)
	if v.BuildCE != 130 { // hedge fills never count as build quantity
		t.Fatalf("hedge changed build count: %d", v.BuildCE)
	}
	for _, l := range v.Legs {
		if l.Token == 1 && (l.Qty != 0 || math.Abs(l.Realized-325) > 1e-9) {
			t.Fatalf("CE 22550 leg %+v, want flat with realized 325", l)
		}
	}
}

func TestSBRoundToLot_MRound(t *testing.T) {
	for _, c := range []struct{ in, lot, want int64 }{{1, 65, 0}, {33, 65, 65}, {6500, 65, 6500}, {6530, 65, 6500}, {6533, 65, 6565}, {100, 30, 90}} {
		if got := sbRoundToLot(c.in, c.lot); got != c.want {
			t.Fatalf("mround(%d,%d)=%d want %d", c.in, c.lot, got, c.want)
		}
	}
}
