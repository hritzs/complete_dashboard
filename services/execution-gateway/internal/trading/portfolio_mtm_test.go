package trading

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortfolioMTM_OpenStatus(t *testing.T) {
	for _, st := range []string{"ACTIVE", "SQUARING_OFF", "PARTIAL", sbStatusLive} {
		if !pmOpenStatus(st) {
			t.Fatalf("%s must be acted on", st)
		}
	}
	for _, st := range []string{"CLOSED_MTM", "CLOSED_PORTFOLIO", "CLOSEDSQF", "FAILED", "RECONCILIATION_REQUIRED", sbStatusClosed, ""} {
		if pmOpenStatus(st) {
			t.Fatalf("%s must not be acted on", st)
		}
	}
	if closedStatusForReason("PORTFOLIO") != "CLOSED_PORTFOLIO" || !isTerminalTradeStatus("CLOSED_PORTFOLIO") || closeReasonForStatus("CLOSED_PORTFOLIO") == "" {
		t.Fatal("CLOSED_PORTFOLIO must be a terminal close status with a reason")
	}
}

// Save: both levels off = OFF; any level = ARMED for today; loss must be
// below profit; defaults to the live account.
func TestPortfolioMTM_SaveAndArm(t *testing.T) {
	t.Setenv("PORTFOLIO_MTM_PATH", filepath.Join(t.TempDir(), "pm.json"))
	pm.mu.Lock()
	pm.loaded, pm.running = false, false
	pm.mu.Unlock()
	h := &Handlers{}
	post := func(body string) string {
		w := httptest.NewRecorder()
		h.PortfolioMTMHandler(w, httptest.NewRequest("POST", "/api/portfolio/mtm-exit", strings.NewReader(body)))
		return w.Body.String()
	}
	if out := post(`{"profit_level": 10000, "loss_level": 20000}`); !strings.Contains(out, "below the book level") {
		t.Fatalf("loss above profit must be refused: %s", out)
	}
	post(`{"profit_level": 25000, "loss_level": -40000}`)
	pm.mu.Lock()
	st, cfg := pm.st, pm.cfg
	pm.mu.Unlock()
	if st.Status != pmArmed || st.Day != pmToday() || cfg.BrokerName != sbLiveBroker || cfg.AccountID == "" || *cfg.ProfitLevel != 25000 || *cfg.LossLevel != -40000 {
		t.Fatalf("armed for today on the live account: %+v %+v", st, cfg)
	}
	// Book a smaller loss: at -15000 with the SL at -20000, book at >= -10000.
	post(`{"profit_level": -10000, "loss_level": -20000}`)
	pm.mu.Lock()
	st, cfg = pm.st, pm.cfg
	pm.mu.Unlock()
	if st.Status != pmArmed || *cfg.ProfitLevel != -10000 || *cfg.LossLevel != -20000 {
		t.Fatalf("a negative book level above the SL must be accepted: %+v %+v", st, cfg)
	}
	post(`{"profit_level": null, "loss_level": null}`)
	pm.mu.Lock()
	st = pm.st
	pm.loaded = false // reload from disk
	pmLoadLocked()
	reloaded := pm.st
	pm.mu.Unlock()
	if st.Status != pmOff || reloaded.Status != pmOff {
		t.Fatalf("both off = OFF (and saved): %+v / %+v", st, reloaded)
	}
}

func TestPortfolioSquareOffAll_NeedsConfirm(t *testing.T) {
	w := httptest.NewRecorder()
	(&Handlers{}).PortfolioSquareOffAll(w, httptest.NewRequest("POST", "/api/portfolio/square-off-all", strings.NewReader(`{"confirm":"yes"}`)))
	if !strings.Contains(w.Body.String(), pmSquareOffAllConfirm) || w.Code != 400 {
		t.Fatalf("must refuse without the exact confirmation: %d %s", w.Code, w.Body.String())
	}
}

// The whole position netted by instrument across trades: shorts close at
// the asks, longs at the bids; greeks = per-unit x signed qty; wings shown
// but outside the MTM and greek totals; flat instruments only realize.
func TestPortfolioMTM_AggregatePosition(t *testing.T) {
	ex := func(leg, side string, tok int64, qty int64, px float64, phase string) OrderExecution {
		e := OrderExecution{Leg: leg, Side: side, FilledQty: qty, AvgPrice: px, Strike: 22300}
		e.token, e.phase = tok, phase
		return e
	}
	row := OptionChainRow{Strike: 22300, CEToken: 1, PEToken: 2, CELtp: 100, CEBid: 99.5, CEAsk: 100.5, PELtp: 90, PEBid: 89.5, PEAsk: 90.5,
		CEDelta: 0.5, PEDelta: -0.5, CETheta: -10, PETheta: -10,
		CEDepth: &DepthBook{Asks: []DepthLevel{{Price: 100.5, Qty: 100}, {Price: 101, Qty: 1000}}},
		PEDepth: &DepthBook{Bids: []DepthLevel{{Price: 89.5, Qty: 1000}}, Asks: []DepthLevel{{Price: 90.5, Qty: 1000}}}}
	chain := &OptionChainSnapshot{Chain: []OptionChainRow{row}}
	a := newPMAgg()
	a.add(TradeSummary{TradeUID: "T1", Symbol: "NIFTY", Executions: []OrderExecution{
		ex("CE", "SELL", 1, 650, 110, "ENTRY"), ex("CE", "BUY", 1, 65, 105, "SQF"), // CE short 585
		ex("PE", "BUY", 2, 130, 80, "HEDGE"), // PE long 130 (hedge)
		ex("PE", "BUY", 3, 65, 5, wingPhase), // a wing
	}}, chain)
	a.add(TradeSummary{TradeUID: "T2", Symbol: "NIFTY", Executions: []OrderExecution{ex("CE", "SELL", 1, 65, 112, "ENTRY")}}, chain) // CE short 650 total
	v := a.view()
	var ce, pe *pmPos
	for i := range v.Positions {
		switch v.Positions[i].Token {
		case 1:
			ce = &v.Positions[i]
		case 2:
			pe = &v.Positions[i]
		}
	}
	if ce == nil || pe == nil || ce.NetQty != -650 || pe.NetQty != 130 || len(ce.Trades) != 2 {
		t.Fatalf("netting: %+v %+v", ce, pe)
	}
	// short 650 buys back 100 @100.5 + 550 @101 (walks the asks)
	wantClose := (100*100.5 + 550*101) / 650
	if d := ce.ClosePx - wantClose; d > 1e-9 || d < -1e-9 || ce.CloseTo != 101 {
		t.Fatalf("CE close %.4f to %.2f, want %.4f to 101", ce.ClosePx, ce.CloseTo, wantClose)
	}
	if pe.ClosePx != 89.5 { // long sells into the bids
		t.Fatalf("PE close %.2f", pe.ClosePx)
	}
	cash := 650*110.0 - 65*105 + 65*112
	if d := ce.MTM - (cash - 650*wantClose); d > 1e-6 || d < -1e-6 {
		t.Fatalf("CE MTM %.4f", ce.MTM)
	}
	if v.OpenQty != 780 || v.Greeks.Delta != 0.5*-650+(-0.5)*130 || v.Greeks.Theta != -10*-650+-10*130 {
		t.Fatalf("totals: open %d greeks %+v", v.OpenQty, v.Greeks)
	}
}

// Even wind-down: the leg most behind (largest share still open) goes
// first, across trades; a lot that would push |net delta| out of the band
// goes after every lot that keeps it in.
func TestPortfolioMTM_PickOrderEvenAndDelta(t *testing.T) {
	c := []pmCand{
		{uid: "A", leg: mtmLeg{Leg: "CE", Token: 1}, qty: 65, frac: 0.50, dDelta: +32}, // A's CE half closed
		{uid: "A", leg: mtmLeg{Leg: "PE", Token: 2}, qty: 65, frac: 0.60, dDelta: -32}, // A's PE 60% open
		{uid: "B", leg: mtmLeg{Leg: "CE", Token: 3}, qty: 65, frac: 1.00, dDelta: +33}, // B untouched
		{uid: "B", leg: mtmLeg{Leg: "PE", Token: 4}, qty: 65, frac: 0.95, dDelta: -31},
	}
	// delta 0, band 39: every lot stays inside -> purely the most-behind first.
	o := pmPickOrder(c, 0, 39)
	if o[0].uid != "B" || o[0].leg.Leg != "CE" || o[1].uid != "B" || o[2].leg.Token != 2 {
		t.Fatalf("most-behind first across trades: %+v", o)
	}
	// net delta already +30: B's CE (+33 -> +63) would leave the band of 39;
	// the PE lots (bringing it toward 0) go first.
	o = pmPickOrder(c, 30, 39)
	if o[0].leg.Leg != "PE" || o[0].uid != "B" || o[len(o)-1].dDelta <= 0 {
		t.Fatalf("delta band must win over evenness: %+v", o)
	}
	for _, x := range o[:2] {
		if x.dDelta > 0 {
			t.Fatalf("a delta-raising lot ahead of the reducing ones: %+v", o)
		}
	}
}
