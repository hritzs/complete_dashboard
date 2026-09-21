package trading

import "testing"

func ex(leg, side string, qty int64, px float64) OrderExecution {
	return OrderExecution{Leg: leg, Side: side, Quantity: qty, FilledQty: qty, AvgPrice: px, Status: "FILLED"}
}

// The forced-hedge trade of 2026-09-21: two entries, a hedge, then an exit
// bought back in two chunks. Worked by hand: CE sold 130 @74.15, bought 130
// @76.125 = -256.75; PE sold 65 @62.80, bought 65 @62.85 = -3.25; total -260.
func TestComputeLegPnL_HedgedTrade(t *testing.T) {
	legs := computeLegPnL([]OrderExecution{
		ex("CE", "SELL", 65, 74.10), ex("PE", "SELL", 65, 62.80), // entry
		ex("CE", "SELL", 65, 74.20), ex("PE", "BUY", 65, 62.85), // hedge
		ex("CE", "BUY", 65, 76.20), ex("CE", "BUY", 65, 76.05), // exit
	})
	if got := legs["CE"].Realized; got != -256.75 {
		t.Fatalf("CE realized = %v, want -256.75", got)
	}
	if got := legs["PE"].Realized; got != -3.25 {
		t.Fatalf("PE realized = %v, want -3.25", got)
	}
	if got := totalRealizedPnL(legs); got != -260.00 {
		t.Fatalf("total = %v, want -260.00", got)
	}
	if legs["CE"].NetShortQty != 0 || legs["PE"].NetShortQty != 0 {
		t.Fatalf("a fully closed trade must have no open quantity: %+v %+v", legs["CE"], legs["PE"])
	}
	if legs["CE"].SoldAvg != 74.15 || legs["CE"].BoughtAvg != 76.13 {
		t.Fatalf("CE averages = %v/%v", legs["CE"].SoldAvg, legs["CE"].BoughtAvg)
	}
}

func TestComputeLegPnL_OnlyCountsRealFills(t *testing.T) {
	legs := computeLegPnL([]OrderExecution{
		ex("CE", "SELL", 65, 100),
		{Leg: "CE", Side: "BUY", Quantity: 65, FilledQty: 0, AvgPrice: 0, Status: "REJECTED"}, // never filled
		{Leg: "CE", Side: "BUY", Quantity: 65, FilledQty: 65, AvgPrice: 0, Status: "FILLED"},  // fill without a price
	})
	if legs["CE"].BoughtQty != 0 || legs["CE"].Realized != 0 || legs["CE"].NetShortQty != 65 {
		t.Fatalf("unfilled / unpriced orders leaked into the PnL: %+v", legs["CE"])
	}
}

func TestComputeLegPnL_PartialExitRealizesOnlyMatchedQuantity(t *testing.T) {
	legs := computeLegPnL([]OrderExecution{ex("PE", "SELL", 130, 100), ex("PE", "BUY", 65, 90)})
	if got := legs["PE"].Realized; got != 650 { // (100-90) * 65
		t.Fatalf("realized = %v, want 650", got)
	}
	if legs["PE"].NetShortQty != 65 {
		t.Fatalf("open short = %d, want 65", legs["PE"].NetShortQty)
	}
}

func TestClassifyExecutionKind(t *testing.T) {
	cases := []struct{ phase, intent, want string }{
		{"BUILD", "x", "ENTRY"}, {"HEDGE", "x", "HEDGE"}, {"SQF", "x", "EXIT"}, {"PSQF", "x", "EXIT"},
		// older rows: phase is the PRIMARY default, so the intent id decides
		{"PRIMARY", "BUI_TRD_U001_GREEKSOFT_147_NIFTY_1789972990786182", "ENTRY"},
		{"PRIMARY", "HDG20260921121310_CE_1789972993047863857", "HEDGE"},
		{"PRIMARY", "NIF210926122514SQF1C0O0_1", "EXIT"},
		{"", "something-else", "OTHER"},
	}
	for _, c := range cases {
		if got := classifyExecutionKind(c.phase, c.intent); got != c.want {
			t.Fatalf("classify(%q,%q) = %s, want %s", c.phase, c.intent, got, c.want)
		}
	}
}

func TestCloseReasonForStatus(t *testing.T) {
	for st, want := range map[string]string{
		"CLOSED_SL": "STOP LOSS", "CLOSED_TP": "TAKE PROFIT", "CLOSED_TIME": "TIME EXIT",
		"CLOSEDSQF": "MANUAL SQUARE-OFF", "ACTIVE": "", "PARTIAL": "",
	} {
		if got := closeReasonForStatus(st); got != want {
			t.Fatalf("closeReasonForStatus(%s) = %q, want %q", st, got, want)
		}
	}
}
