package trading

import (
	"context"
	"testing"
)

func TestBookHedgeFills(t *testing.T) {
	cases := []struct {
		name             string
		ceQty, peQty     int
		ceSide, peSide   string
		filledCE, filled int64
		wantCE, wantPE   int
		wantErr          bool
	}{
		{"sell CE buy PE", 65, 65, "SELL", "BUY", 65, 65, 130, 0, false},
		{"buy CE sell PE", 65, 65, "BUY", "SELL", 65, 65, 0, 130, false},
		{"partial fill books only what filled", 65, 65, "BUY", "SELL", 65, 30, 0, 95, false},
		{"nothing filled books nothing", 65, 65, "BUY", "SELL", 0, 0, 65, 65, false},
		{"buy exceeding open short is refused", 0, 65, "BUY", "SELL", 65, 65, 0, 65, true},
		{"unknown side is refused", 65, 65, "HOLD", "SELL", 65, 65, 65, 65, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce, pe, err := bookHedgeFills(tc.ceQty, tc.peQty, tc.ceSide, tc.peSide, tc.filledCE, tc.filled)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if ce != tc.wantCE || pe != tc.wantPE {
				t.Fatalf("got CE/PE %d/%d, want %d/%d", ce, pe, tc.wantCE, tc.wantPE)
			}
		})
	}
}

func newHedgeTestService(tr StoredTrade, netDelta float64) (*Service, *MemoryStore, *fakeSLExecutor) {
	store := NewMemoryStore()
	store.SaveTrade(tr)
	store.SaveSnapshot(TradeSnapshot{TradeUID: tr.TradeUID, NetDelta: netDelta})
	exec := &fakeSLExecutor{}
	return &Service{
		Store:         store,
		BrokerFactory: &fakeBrokerFactory{executor: exec},
	}, store, exec
}

func TestManualHedge_BooksVerifiedFillsAndExitClosesEverything(t *testing.T) {
	tr := newTestSquareOffTrade("TRD_HEDGE_ROUNDTRIP")
	svc, store, exec := newHedgeTestService(tr, -5)

	if err := svc.ManualHedge(context.Background(), tr.TradeUID); err != nil {
		t.Fatalf("ManualHedge: %v", err)
	}

	if len(exec.submitted) != 2 {
		t.Fatalf("placed %d orders, want 2", len(exec.submitted))
	}
	for _, in := range exec.submitted {
		if in.OrderType != "MARKET" {
			t.Fatalf("hedge leg %s order type %q, want MARKET (a nil-price LIMIT would be sent at 0.00)", in.LegType, in.OrderType)
		}
		if in.Phase != "HEDGE" || in.HedgeGroupID == "" {
			t.Fatalf("hedge leg %s phase=%q group=%q", in.LegType, in.Phase, in.HedgeGroupID)
		}
		if in.Token != tr.CEToken && in.Token != tr.PEToken {
			t.Fatalf("hedge traded token %d, not one of the trade's own tokens", in.Token)
		}
	}

	got, _ := store.LoadTrade(tr.TradeUID)
	// negative delta: BUY CE (65 -> 0), SELL PE (65 -> 130)
	if got.CEQty != 0 || got.PEQty != 130 {
		t.Fatalf("after hedge CE/PE = %d/%d, want 0/130", got.CEQty, got.PEQty)
	}
	if got.Status != "ACTIVE" {
		t.Fatalf("status = %s, want ACTIVE", got.Status)
	}

	hedgeOrders := len(exec.submitted)
	if err := svc.SquareOff(tr.TradeUID, "manual"); err != nil {
		t.Fatalf("SquareOff after hedge: %v", err)
	}

	closed, _ := store.LoadTrade(tr.TradeUID)
	if closed.CEQty != 0 || closed.PEQty != 0 || closed.Status != "CLOSEDSQF" {
		t.Fatalf("after exit CE/PE=%d/%d status=%s, want 0/0 CLOSEDSQF", closed.CEQty, closed.PEQty, closed.Status)
	}

	var boughtPE int64
	for _, in := range exec.submitted[hedgeOrders:] {
		if in.Token == tr.PEToken && in.Side == "BUY" {
			boughtPE += in.Quantity
		}
	}
	if boughtPE != 130 {
		t.Fatalf("exit bought back %d PE, want 130 (the original 65 plus the 65 the hedge added)", boughtPE)
	}
}

func TestManualHedge_RefusalsPlaceNothing(t *testing.T) {
	t.Run("non-ACTIVE trade", func(t *testing.T) {
		tr := newTestSquareOffTrade("TRD_HEDGE_PARTIAL")
		tr.Status = "PARTIAL"
		svc, _, exec := newHedgeTestService(tr, -5)
		if err := svc.ManualHedge(context.Background(), tr.TradeUID); err == nil {
			t.Fatal("want error for non-ACTIVE trade")
		}
		if len(exec.submitted) != 0 {
			t.Fatalf("placed %d orders on a refused hedge", len(exec.submitted))
		}
	})

	t.Run("delta below one is a no-op", func(t *testing.T) {
		tr := newTestSquareOffTrade("TRD_HEDGE_SMALLDELTA")
		svc, _, exec := newHedgeTestService(tr, 0.4)
		if err := svc.ManualHedge(context.Background(), tr.TradeUID); err != nil {
			t.Fatalf("want nil no-op, got %v", err)
		}
		if len(exec.submitted) != 0 {
			t.Fatalf("placed %d orders for sub-1 delta", len(exec.submitted))
		}
	})

	t.Run("BUY leg larger than open short", func(t *testing.T) {
		tr := newTestSquareOffTrade("TRD_HEDGE_NOSHORT")
		tr.CEQty = 0 // negative delta would BUY CE, but there is no short CE to buy back
		svc, store, exec := newHedgeTestService(tr, -5)
		if err := svc.ManualHedge(context.Background(), tr.TradeUID); err == nil {
			t.Fatal("want refusal")
		}
		if len(exec.submitted) != 0 {
			t.Fatalf("placed %d orders on a refused hedge", len(exec.submitted))
		}
		got, _ := store.LoadTrade(tr.TradeUID)
		if got.CEQty != 0 || got.PEQty != 65 {
			t.Fatalf("refused hedge changed quantities to %d/%d", got.CEQty, got.PEQty)
		}
	})
}
