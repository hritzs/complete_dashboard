package trading

import (
	"math"
	"testing"
)

func TestMTMExit_InfinityByDefault(t *testing.T) {
	if _, on := mtmExitFloor(MonitorConfig{}, 22600, 130); on {
		t.Fatal("no level set must mean infinity (never fires)")
	}
	lvl := 10.0
	for unit, want := range map[string]float64{"": 10, "rs": 10, "pts": 1300, "bps": 22600 * 10 / 10000.0 * 130} {
		f, on := mtmExitFloor(MonitorConfig{MTMExitLevel: &lvl, MTMExitUnit: unit}, 22600, 130)
		if !on || math.Abs(f-want) > 1e-9 {
			t.Fatalf("%q: %v want %v", unit, f, want)
		}
	}
}

// Cash from fills: sold - bought, exact; wings left out.
func TestMTMExit_LegsFromFills(t *testing.T) {
	execs := []OrderExecution{
		{Leg: "CE", token: 1, Side: "SELL", FilledQty: 130, AvgPrice: 153.05},
		{Leg: "PE", token: 2, Side: "SELL", FilledQty: 130, AvgPrice: 141.15},
		{Leg: "CE", token: 1, Side: "BUY", FilledQty: 65, AvgPrice: 150.00},
		{Leg: "CE", token: 9, Side: "BUY", FilledQty: 65, AvgPrice: 5.00, phase: wingPhase},
	}
	legs := mtmLegsFromExecutions(execs)
	if len(legs) != 2 {
		t.Fatalf("wing must be excluded: %+v", legs)
	}
	for _, l := range legs {
		switch l.Token {
		case 1:
			if l.NetShort != 65 || math.Abs(l.Cash-(130*153.05-65*150)) > 1e-9 {
				t.Fatalf("CE %+v", l)
			}
		case 2:
			if l.NetShort != 130 || math.Abs(l.Cash-130*141.15) > 1e-9 {
				t.Fatalf("PE %+v", l)
			}
		}
	}
}

// Executable MTM walks the depth: a short buys back up the asks.
func TestMTMExit_ExecMTMWalksDepth(t *testing.T) {
	row := &OptionChainRow{CEToken: 1, CEDepth: &DepthBook{Asks: []DepthLevel{{Price: 150, Qty: 65}, {Price: 151, Qty: 65}}}}
	legs := []mtmLeg{{Leg: "CE", Token: 1, NetShort: 130, Cash: 130 * 155}}
	m, ok := mtmExecMTM(legs, func(mtmLeg) *OptionChainRow { return row })
	want := 130*155.0 - (65*150 + 65*151)
	if !ok || math.Abs(m-want) > 1e-9 {
		t.Fatalf("exec MTM %v want %v", m, want)
	}
}

// The lot's IOC limit never lets the trade end below the level: filled
// entirely at the limit, MTM stays >= floor.
func TestMTMExit_LotLimitKeepsFloor(t *testing.T) {
	book := []DepthLevel{{Price: 150, Qty: 65}, {Price: 152, Qty: 65}}
	// MTM 1000 with the lot valued at 150; level 870 -> slack 130 / 65 = 2/unit.
	lim, ok := mtmLotLimit(book, 65, true, 1000, 870)
	if !ok || math.Abs(lim-151.5) > 1e-9 {
		t.Fatalf("limit %v ok %v (vwap 150 + slack 2 = 152, capped at level 150 + band 1.50)", lim, ok)
	}
	if after := 1000 - (lim-150)*65; after < 870-1e-9 {
		t.Fatalf("filled at the limit the MTM %.2f would be below the level", after)
	}
	// Slack 0.6/unit -> 150.60, rounded DOWN to the tick: 150.60.
	lim, ok = mtmLotLimit(book, 65, true, 1000, 1000-0.62*65)
	if !ok || math.Abs(lim-150.60) > 1e-9 {
		t.Fatalf("tick rounding: %v", lim)
	}
	// Below the level: nothing.
	if _, ok := mtmLotLimit(book, 65, true, 860, 870); ok {
		t.Fatal("MTM below the level must not price an order")
	}
	// Selling a long down the bids: limit at or above vwap - slack.
	bids := []DepthLevel{{Price: 100, Qty: 65}}
	lim, ok = mtmLotLimit(bids, 65, false, 500, 500)
	if !ok || math.Abs(lim-100) > 1e-9 { // no slack: exactly the bid
		t.Fatalf("sell limit %v ok %v", lim, ok)
	}
}

func TestMTMExit_WalkThinBook(t *testing.T) {
	vwap, worst, ok := mtmWalk([]DepthLevel{{Price: 10, Qty: 30}}, 65)
	if ok || worst != 10 || vwap != 10 {
		t.Fatalf("thin book: vwap %v worst %v ok %v", vwap, worst, ok)
	}
	if _, _, ok := mtmWalk(nil, 65); ok {
		t.Fatal("empty book")
	}
}
