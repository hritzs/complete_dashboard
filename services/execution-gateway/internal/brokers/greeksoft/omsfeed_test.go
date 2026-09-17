package greeksoft

import (
	"context"
	"testing"

	"trading-platform/libs/broker-greeksoft/normalize"
)

func TestOMSFeed_ApplyOrder_TracksFillAndToken(t *testing.T) {
	f := NewOMSFeed()

	f.applyOrder(normalize.GreeksoftOrderResponse{
		GOrderID:       "ORD1",
		GToken:         "500",
		Side:           "2", // SELL
		OrderStatus:    "Traded",
		QtyFilledToday: "50",
		Price:          "100.00",
		LuTime:         "1700000000",
	})

	fills, err := f.GetVerifiedFills(context.Background())
	if err != nil {
		t.Fatalf("GetVerifiedFills failed: %v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("got %d fills, want 1", len(fills))
	}
	fill := fills[0]
	if fill.BrokerOrderID != "ORD1" {
		t.Errorf("BrokerOrderID = %q, want ORD1", fill.BrokerOrderID)
	}
	if fill.Token != 500 {
		t.Errorf("Token = %d, want 500", fill.Token)
	}
	if fill.Side != "SELL" {
		t.Errorf("Side = %q, want SELL", fill.Side)
	}
	if fill.FilledQty != 50 {
		t.Errorf("FilledQty = %d, want 50", fill.FilledQty)
	}
	if fill.AveragePrice != 100.00 {
		t.Errorf("AveragePrice = %v, want 100.00", fill.AveragePrice)
	}
	if !fill.Verified {
		t.Error("expected Verified = true")
	}
	if fill.Source != "GREEKSOFT_IRIS" {
		t.Errorf("Source = %q, want GREEKSOFT_IRIS", fill.Source)
	}
}

func TestOMSFeed_GetVerifiedFills_ExcludesZeroFill(t *testing.T) {
	f := NewOMSFeed()

	f.applyOrder(normalize.GreeksoftOrderResponse{
		GOrderID:       "ORD2",
		GToken:         "501",
		Side:           "1",
		OrderStatus:    "Pending",
		QtyFilledToday: "0",
		Price:          "100.00",
	})

	fills, err := f.GetVerifiedFills(context.Background())
	if err != nil {
		t.Fatalf("GetVerifiedFills failed: %v", err)
	}
	if len(fills) != 0 {
		t.Fatalf("got %d fills, want 0 (unfilled order must not appear as a verified fill)", len(fills))
	}
}

func TestOMSFeed_ApplyOrder_DoesNotRegressFilledQty(t *testing.T) {
	f := NewOMSFeed()

	f.applyOrder(normalize.GreeksoftOrderResponse{
		GOrderID:       "ORD3",
		GToken:         "502",
		Side:           "2",
		OrderStatus:    "Traded",
		QtyFilledToday: "65",
		Price:          "155.70",
	})

	// A stale/out-of-order redelivery reporting a lower filled qty must
	// not roll back what's already known -- state only moves forward.
	f.applyOrder(normalize.GreeksoftOrderResponse{
		GOrderID:       "ORD3",
		GToken:         "502",
		Side:           "2",
		OrderStatus:    "Pending",
		QtyFilledToday: "0",
		Price:          "0.00",
	})

	fills, err := f.GetVerifiedFills(context.Background())
	if err != nil {
		t.Fatalf("GetVerifiedFills failed: %v", err)
	}
	if len(fills) != 1 || fills[0].FilledQty != 65 || fills[0].AveragePrice != 155.70 {
		t.Fatalf("got %+v, want one fill still at FilledQty=65 AveragePrice=155.70", fills)
	}
}

func TestOMSFeed_ApplyTrade_InheritsTokenFromPriorOrderResponse(t *testing.T) {
	f := NewOMSFeed()

	// GreekSoft always sends an OrderResponse alongside/around a
	// TradeResponse -- confirmed live this session. The OrderResponse
	// carries gtoken; TradeResponse does not.
	f.applyOrder(normalize.GreeksoftOrderResponse{
		GOrderID:       "ORD4",
		GToken:         "503",
		Side:           "2",
		OrderStatus:    "Pending",
		QtyFilledToday: "0",
		Price:          "0.00",
	})

	f.applyTrade(normalize.GreeksoftTradeResponse{
		GOrderID:       "ORD4",
		Side:           "2",
		OrderStatus:    "Executed",
		QtyFilledToday: "65",
		TradeID:        "T1",
		TradedQty:      "65",
		TradedPrice:    "155.70",
	})

	fills, err := f.GetVerifiedFills(context.Background())
	if err != nil {
		t.Fatalf("GetVerifiedFills failed: %v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("got %d fills, want 1", len(fills))
	}
	if fills[0].Token != 503 {
		t.Errorf("Token = %d, want 503 (inherited from the prior OrderResponse)", fills[0].Token)
	}
	if fills[0].AveragePrice != 155.70 {
		t.Errorf("AveragePrice = %v, want 155.70 (exact traded_price from TradeResponse)", fills[0].AveragePrice)
	}
}

func TestOMSFeed_ApplyTrade_UnknownOrderGetsZeroToken(t *testing.T) {
	f := NewOMSFeed()

	// A TradeResponse with no prior OrderResponse: token stays 0 rather
	// than being guessed.
	f.applyTrade(normalize.GreeksoftTradeResponse{
		GOrderID:       "ORD5",
		Side:           "1",
		OrderStatus:    "Executed",
		QtyFilledToday: "10",
		TradedQty:      "10",
		TradedPrice:    "50.00",
	})

	fills, err := f.GetVerifiedFills(context.Background())
	if err != nil {
		t.Fatalf("GetVerifiedFills failed: %v", err)
	}
	if len(fills) != 1 || fills[0].Token != 0 {
		t.Fatalf("got %+v, want one fill with Token=0", fills)
	}
}

func TestOMSFeed_Updates_FiresOnStateChange(t *testing.T) {
	f := NewOMSFeed()

	f.applyOrder(normalize.GreeksoftOrderResponse{
		GOrderID:       "ORD6",
		GToken:         "504",
		Side:           "1",
		OrderStatus:    "Traded",
		QtyFilledToday: "10",
		Price:          "10.00",
	})

	select {
	case id := <-f.Updates():
		if id != "ORD6" {
			t.Errorf("update id = %q, want ORD6", id)
		}
	default:
		t.Fatal("expected an update on the channel after applyOrder")
	}
}

func TestOMSFeed_GetVerifiedFills_MultipleOrdersIndependent(t *testing.T) {
	f := NewOMSFeed()

	f.applyOrder(normalize.GreeksoftOrderResponse{
		GOrderID: "A", GToken: "1", Side: "2", OrderStatus: "Traded",
		QtyFilledToday: "20", Price: "10.00",
	})
	f.applyOrder(normalize.GreeksoftOrderResponse{
		GOrderID: "B", GToken: "2", Side: "1", OrderStatus: "Traded",
		QtyFilledToday: "30", Price: "20.00",
	})

	fills, err := f.GetVerifiedFills(context.Background())
	if err != nil {
		t.Fatalf("GetVerifiedFills failed: %v", err)
	}
	if len(fills) != 2 {
		t.Fatalf("got %d fills, want 2", len(fills))
	}

	byID := map[string]int64{}
	for _, fl := range fills {
		byID[fl.BrokerOrderID] = fl.FilledQty
	}
	if byID["A"] != 20 || byID["B"] != 30 {
		t.Fatalf("got %v, want A=20 B=30", byID)
	}
}
