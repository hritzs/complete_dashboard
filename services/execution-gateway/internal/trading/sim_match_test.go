package trading

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func simMatchTrade() StoredTrade {
	tr := newTestSquareOffTrade("TRD_SIM_MATCH")
	tr.CEQty, tr.PEQty, tr.Lots = 3965, 4355, 64
	return tr
}

// 4 Nov 11:08: 8 lots hedged by hand, the simulation not. Sim 0.0767 per qty = 4.9855 per lot.
func TestSimMatchOf_TodaysGap(t *testing.T) {
	snap := TradeSnapshot{NetDelta: -68.6, NetGamma: -7.263, Underlying: 23769.25, PointsAllowed: 64.4}
	m := simMatchOf(simMatchTrade(), snap, 4.9855, 23778.95)
	if m.Lots != 7 || m.Qty != 455 || m.CESide != "BUY" || m.PESide != "SELL" {
		t.Fatalf("order %+v", m)
	}
	if m.SimDelta < 388 || m.SimDelta > 391 || m.Gap > -457 || m.Gap < -460 {
		t.Fatalf("sim %.1f gap %.1f", m.SimDelta, m.Gap)
	}
	if m.DeltaAfter != -68.6+455 || m.PointsOutAfter < 53 || m.PointsOutAfter > 54 {
		t.Fatalf("after %.1f, %.1f out", m.DeltaAfter, m.PointsOutAfter)
	}
}

// The side follows the gap, not the sign of the live delta.
func TestSimMatchOf_SideFollowsTheGap(t *testing.T) {
	tr := simMatchTrade()
	// Live +100, sim +516 (0.124 per qty): needs MORE delta although live is long.
	m := simMatchOf(tr, TradeSnapshot{NetDelta: 100, NetGamma: -7, Underlying: 23800}, 8.06, 23800)
	if m.CESide != "BUY" || m.PESide != "SELL" || m.Lots != 6 || m.DeltaAfter != 100+390 {
		t.Fatalf("add delta: %+v", m)
	}
	// Live -100, sim -516: needs LESS delta although live is short.
	m = simMatchOf(tr, TradeSnapshot{NetDelta: -100, NetGamma: -7, Underlying: 23800}, -8.06, 23800)
	if m.CESide != "SELL" || m.PESide != "BUY" || m.Lots != 6 || m.DeltaAfter != -100-390 {
		t.Fatalf("take delta off: %+v", m)
	}
	// Under half a lot: nothing to do.
	m = simMatchOf(tr, TradeSnapshot{NetDelta: 20, NetGamma: -7, Underlying: 23800}, 0, 23800)
	if m.Lots != 0 || m.Qty != 0 || m.CESide != "" || m.DeltaAfter != 20 {
		t.Fatalf("nothing to do: %+v", m)
	}
}

func setSimDriver(t *testing.T, d *simDriver) {
	t.Helper()
	simDrive.mu.Lock()
	was := simDrive.d
	simDrive.d = d
	simDrive.mu.Unlock()
	t.Cleanup(func() {
		simDrive.mu.Lock()
		simDrive.d = was
		simDrive.mu.Unlock()
	})
	simMatchLast.mu.Lock()
	simMatchLast.at = map[string]time.Time{}
	simMatchLast.mu.Unlock()
}

func simMatchService(t *testing.T, live float64) (*Service, *tranchedHedgeExecutor, StoredTrade) {
	t.Helper()
	t.Setenv("GS_MAX_ORDER_VALUE", "0")
	tr := simMatchTrade()
	store := NewMemoryStore()
	store.SaveTrade(tr)
	store.SaveSnapshot(TradeSnapshot{TradeUID: tr.TradeUID, Timestamp: time.Now(), NetDelta: live, NetGamma: -7, Underlying: 23800, PointsAllowed: 64})
	registry := NewOrderEventRegistry()
	registry.SetHealthy(true)
	exec := newTranchedHedgeExecutor(registry)
	return &Service{Store: store, BrokerFactory: &fakeBrokerFactory{executor: exec}, OrderEvents: registry,
		Snapshot: fakeHedgeChainSnapshot{}, freezeQtyBySymbol: map[string]int64{"NIFTY": 1755}}, exec, tr
}

// The whole path: real hedge code, a broker fake. Live +100, sim +516: the
// hedge code alone would SELL CE here; this must BUY CE.
func TestSimMatchExecute_SendsTheConfirmedOrder(t *testing.T) {
	s, exec, tr := simMatchService(t, 100)
	setSimDriver(t, &simDriver{TradeUID: tr.TradeUID, LastRun: time.Now(), SimState: "OPEN", SimDelta: 8.06, SimFuture: 23800})
	m, err := s.SimMatchExecute(context.Background(), tr.TradeUID, 6, "BUY")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if m.Lots != 6 || m.Qty != 390 {
		t.Fatalf("result %+v", m)
	}
	var ce, pe OrderIntent
	for _, in := range exec.submitted {
		switch in.LegType {
		case "CE":
			ce = in
		case "PE":
			pe = in
		}
	}
	if !strings.EqualFold(ce.Side, "BUY") || !strings.EqualFold(pe.Side, "SELL") || ce.Quantity != 390 || pe.Quantity != 390 {
		t.Fatalf("orders CE %+v PE %+v", ce, pe)
	}
	got, _ := s.Store.LoadTrade(tr.TradeUID)
	if n := len(got.Config.HedgeEvents); n != 1 || got.Config.HedgeEvents[0].Trigger != "MATCH_SIM" || got.Config.HedgeEvents[0].CESide != "BUY" {
		t.Fatalf("recorded %+v", got.Config.HedgeEvents)
	}
	// A second press straight away sends nothing.
	before := len(exec.submitted)
	s.Store.SaveSnapshot(TradeSnapshot{TradeUID: tr.TradeUID, Timestamp: time.Now(), NetDelta: 100, NetGamma: -7, Underlying: 23800})
	if _, err := s.SimMatchExecute(context.Background(), tr.TradeUID, 6, "BUY"); err == nil || len(exec.submitted) != before {
		t.Fatalf("double press: %v, %d -> %d orders", err, before, len(exec.submitted))
	}
}

func TestSimMatchExecute_RefusesAndSendsNothing(t *testing.T) {
	fresh := func(tr StoredTrade) *simDriver {
		return &simDriver{TradeUID: tr.TradeUID, LastRun: time.Now(), SimState: "OPEN", SimDelta: 8.06, SimFuture: 23800}
	}
	cases := []struct {
		name   string
		driver func(StoredTrade) *simDriver
		lots   int64
		side   string
		want   string
	}{
		{"not sim-driven", func(StoredTrade) *simDriver { return nil }, 6, "BUY", "not driven"},
		{"another trade", func(StoredTrade) *simDriver {
			return &simDriver{TradeUID: "OTHER", LastRun: time.Now(), SimFuture: 23800}
		}, 6, "BUY", "not driven"},
		{"sim stale", func(tr StoredTrade) *simDriver { d := fresh(tr); d.LastRun = time.Now().Add(-time.Minute); return d }, 6, "BUY", "not updating"},
		{"sim exited", func(tr StoredTrade) *simDriver { d := fresh(tr); d.SimState = "EXITED"; return d }, 6, "BUY", "EXITED"},
		{"sim error", func(tr StoredTrade) *simDriver { d := fresh(tr); d.Error = "boom"; return d }, 6, "BUY", "boom"},
		{"side changed", fresh, 6, "SELL", "gap moved"},
		{"size moved", fresh, 9, "BUY", "gap moved"},
		{"no size", fresh, 0, "BUY", "nothing sent"},
		{"gap bigger than the trade", func(tr StoredTrade) *simDriver { d := fresh(tr); d.SimDelta = 7214; return d }, 6, "BUY", "bigger than the trade"},
	}
	for _, c := range cases {
		s, exec, tr := simMatchService(t, 100)
		setSimDriver(t, c.driver(tr))
		_, err := s.SimMatchExecute(context.Background(), tr.TradeUID, c.lots, c.side)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(exec.submitted) != 0 {
			t.Fatalf("%s: %d orders sent", c.name, len(exec.submitted))
		}
	}
	// A stale live snapshot.
	s, exec, tr := simMatchService(t, 100)
	setSimDriver(t, fresh(tr))
	s.Store.SaveSnapshot(TradeSnapshot{TradeUID: tr.TradeUID, Timestamp: time.Now().Add(-time.Minute), NetDelta: 100})
	if _, err := s.SimMatchExecute(context.Background(), tr.TradeUID, 6, "BUY"); err == nil || len(exec.submitted) != 0 {
		t.Fatalf("stale snapshot: %v", err)
	}
}

func TestSimMatchHandler(t *testing.T) {
	s, exec, tr := simMatchService(t, 100)
	setSimDriver(t, &simDriver{TradeUID: tr.TradeUID, LastRun: time.Now(), SimState: "OPEN", SimDelta: 8.06, SimFuture: 23800})
	h := &Handlers{Store: s.Store, Service: s}
	url := "/api/trade/" + tr.TradeUID + "/match-sim"
	w := httptest.NewRecorder()
	h.SimMatchHandler(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"lots":6`) || !strings.Contains(w.Body.String(), `"ce_side":"BUY"`) || len(exec.submitted) != 0 {
		t.Fatalf("preview %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.SimMatchHandler(w, httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"lots":6,"ce_side":"BUY"}`)))
	if w.Code != http.StatusBadRequest || len(exec.submitted) != 0 {
		t.Fatalf("no confirm: %d, %d orders", w.Code, len(exec.submitted))
	}
	w = httptest.NewRecorder()
	h.SimMatchHandler(w, httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"lots":6,"ce_side":"BUY","confirm":"MATCH"}`)))
	if w.Code != http.StatusOK || len(exec.submitted) == 0 {
		t.Fatalf("confirmed %d: %s", w.Code, w.Body.String())
	}
}

// An ordinary hedge still takes its side from the live delta.
func TestManualHedgeLots_SideStillFromLiveDeltaWithoutMatch(t *testing.T) {
	s, exec, tr := simMatchService(t, 100)
	if err := s.ManualHedgeLots(context.Background(), tr.TradeUID, 1); err != nil {
		t.Fatalf("hedge: %v", err)
	}
	for _, in := range exec.submitted {
		if in.LegType == "CE" && !strings.EqualFold(in.Side, "SELL") {
			t.Fatalf("long delta must SELL CE: %+v", in)
		}
	}
	got, _ := s.Store.LoadTrade(tr.TradeUID)
	if got.Config.HedgeEvents[0].Trigger != "MANUAL" {
		t.Fatalf("trigger %q", got.Config.HedgeEvents[0].Trigger)
	}
}

func TestSimGapFor_OnlyTheDrivenTrade(t *testing.T) {
	tr := simMatchTrade()
	setSimDriver(t, &simDriver{TradeUID: tr.TradeUID, SimDelta: 8.06, SimFuture: 23800})
	if _, lots, ok := simGapFor(tr, TradeSnapshot{NetDelta: 100, NetGamma: -7, Underlying: 23800}); !ok || lots > -6 || lots < -7 {
		t.Fatalf("driven: %v %.2f", ok, lots)
	}
	other := newTestSquareOffTrade("SB-X")
	if _, _, ok := simGapFor(other, TradeSnapshot{NetDelta: 100}); ok {
		t.Fatal("a straddle build trade has no sim gap")
	}
}
