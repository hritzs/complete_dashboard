package trading

import "testing"

// A 192-lot trade squares off the way it was built: ceil(192/100) = 2 lots
// (130 qty) per order, not 1 lot (65) per order. 16 Oct 2026: the 15:37 TIME
// exit sent 384 single-lot orders for CE 10530 / PE 14430.
func TestSquareOff_LotsPerOrderLikeBuild(t *testing.T) {
	store := NewMemoryStore()
	tradeUID := "TRD_TEST_SQF_BUILD_LIKE"
	tr := newTestSquareOffTrade(tradeUID)
	tr.Lots = 192
	tr.CEQty = 11635 // delta-neutral build: legs uneven
	tr.PEQty = 13325
	store.SaveTrade(tr)

	registry := NewOrderEventRegistry()
	registry.SetHealthy(true)
	exec := &delayedLiveExecutor{registry: registry}
	svc := &Service{Store: store, BrokerFactory: &fakeBrokerFactory{executor: exec}, OrderEvents: registry,
		freezeQtyBySymbol: map[string]int64{"NIFTY": 3510}} // live NIFTY max per order (54 lots)

	if err := svc.SquareOff(tradeUID, "TIME"); err != nil {
		t.Fatalf("SquareOff: %v", err)
	}

	exec.mu.Lock()
	defer exec.mu.Unlock()
	var ce, pe int64
	twoLot := 0
	for _, o := range exec.submitted {
		if o.Quantity > 130 {
			t.Fatalf("order qty %d > 130 (2 lots)", o.Quantity)
		}
		if o.Quantity == 130 {
			twoLot++
		}
		switch o.Token {
		case tr.CEToken:
			ce += o.Quantity
		case tr.PEToken:
			pe += o.Quantity
		}
	}
	if ce != 11635 || pe != 13325 {
		t.Fatalf("closed CE %d / PE %d, want 11635 / 13325", ce, pe)
	}
	// At most one odd lot per leg per round (7 rounds x 2 legs).
	if odd := len(exec.submitted) - twoLot; odd > 14 {
		t.Fatalf("%d of %d orders were not 130 qty", odd, len(exec.submitted))
	}
}

func TestExitSizeLots(t *testing.T) {
	if got := exitSizeLots(StoredTrade{Lots: 192}, 222); got != 192 {
		t.Fatalf("got %d, want the trade's own 192 lots", got)
	}
	if got := exitSizeLots(StoredTrade{}, 222); got != 222 {
		t.Fatalf("got %d, want the fallback 222", got)
	}
}
