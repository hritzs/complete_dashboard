package trading

import "testing"

// 27 Oct 14:36: 48 lots asked, 25 filled (1,625 per leg) was logged FILLED.
func TestRecordHedge_PartialWhenLessThanAsked(t *testing.T) {
	store := NewMemoryStore()
	store.SaveTrade(StoredTrade{TradeUID: "T1", LotSize: 65, Status: "ACTIVE"})
	svc := &Service{Store: store}
	svc.recordHedge("T1", HedgeEvent{Lots: 48, CEFilled: 1625, PEFilled: 1625}, nil)
	svc.recordHedge("T1", HedgeEvent{Lots: 25, CEFilled: 1625, PEFilled: 1625}, nil)
	tr, _ := store.LoadTrade("T1")
	if n := len(tr.Config.HedgeEvents); n != 2 {
		t.Fatalf("events = %d", n)
	}
	if got := tr.Config.HedgeEvents[0].Result; got != "PARTIAL" {
		t.Fatalf("48 asked / 25 filled: result %q, want PARTIAL", got)
	}
	if got := tr.Config.HedgeEvents[1].Result; got != "FILLED" {
		t.Fatalf("25 asked / 25 filled: result %q, want FILLED", got)
	}
}
