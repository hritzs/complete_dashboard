package normalize

import (
	"testing"
	"time"
)

// realOrderResponseFixture is the exact OrderResponse example from
// GreekSoft's official websocket docs (05_Greek RESTAPI_WebSocket.pdf).
var realOrderResponseFixture = GreeksoftOrderResponse{
	Side:           "1",
	Qty:            "50",
	Product:        "0",
	GToken:         "102036187",
	OrderStatus:    "Pending",
	EOrderID:       "1000000000101012",
	GOrderID:       "120140064",
	LuTimeExchange: "1369503632",
	LuTime:         "1685016632",
	Symbol:         "NIFTY 25MAY23",
	PendingQty:     "50",
	QtyFilledToday: "0",
	Price:          "18500.00",
	TriggerPrice:   "0.00",
	Reason:         "",
	CancelledBy:    "",
	TradeSymbol:    "NIFTY",
	Instrument:     "FUTIDX",
	OptionType:     "XX",
	StrikePrice:    "0.00",
}

func TestParse_RealFixture(t *testing.T) {
	update := Parse(&realOrderResponseFixture)

	if update.BrokerOrderID != "120140064" {
		t.Fatalf("BrokerOrderID = %q, want 120140064", update.BrokerOrderID)
	}
	if update.ExchangeOrderID != "1000000000101012" {
		t.Fatalf("ExchangeOrderID = %q, want 1000000000101012", update.ExchangeOrderID)
	}
	if update.Status != StatusAcked {
		t.Fatalf("Status = %q, want %q (Pending -> Acked)", update.Status, StatusAcked)
	}
	if update.FilledQtyToday != 0 {
		t.Fatalf("FilledQtyToday = %d, want 0", update.FilledQtyToday)
	}
	if update.PendingQty != 50 {
		t.Fatalf("PendingQty = %d, want 50", update.PendingQty)
	}
	if update.Price != 18500.00 {
		t.Fatalf("Price = %v, want 18500.00", update.Price)
	}
	wantTime := time.Unix(1685016632, 0)
	if !update.BrokerTimestamp.Equal(wantTime) {
		t.Fatalf("BrokerTimestamp = %v, want %v", update.BrokerTimestamp, wantTime)
	}
}

// realTradeResponseFixture matches the live TradeResponse frame captured
// on 2026-09-16 (account 147, gorderid 147829): order_status "Executed"
// with an exact traded_price distinct from any order-level price field.
var realTradeResponseFixture = GreeksoftTradeResponse{
	Side:           "2",
	OrderStatus:    "Executed",
	EOrderID:       "1000000000771012",
	GOrderID:       "147829",
	LuTime:         "1757999999",
	PendingQty:     "0",
	QtyFilledToday: "65",
	TradeID:        "2932987",
	TradedQty:      "65",
	TradedPrice:    "155.70",
}

func TestParseTradeResponse_RealFixture(t *testing.T) {
	update := ParseTradeResponse(&realTradeResponseFixture)

	if update.BrokerOrderID != "147829" {
		t.Fatalf("BrokerOrderID = %q, want 147829", update.BrokerOrderID)
	}
	if update.Status != StatusFilled {
		t.Fatalf("Status = %q, want %q (Executed -> Filled)", update.Status, StatusFilled)
	}
	if update.FilledQtyToday != 65 {
		t.Fatalf("FilledQtyToday = %d, want 65", update.FilledQtyToday)
	}
	if update.Price != 155.70 {
		t.Fatalf("Price = %v, want 155.70 (exact traded_price, not an approximation)", update.Price)
	}
	if update.Side != "SELL" {
		t.Fatalf("Side = %q, want SELL", update.Side)
	}
	if update.FillID != "2932987" {
		t.Fatalf("FillID = %q, want the broker's own tradeid 2932987", update.FillID)
	}
}

func TestMapStatus(t *testing.T) {
	cases := map[string]CanonicalOrderStatus{
		"Pending":            StatusAcked,
		"pending":            StatusAcked,
		"Traded":             StatusFilled,
		"RMS Rejected":       StatusRejected,
		"Exchange Rejected":  StatusRejected,
		"Cancelled":          StatusCancelled,
		"Partially Filled":   StatusPartial,
		"SomethingUnknown42": StatusSubmitted,
		"":                   StatusSubmitted,
	}
	for input, want := range cases {
		if got := MapStatus(input); got != want {
			t.Errorf("MapStatus(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMapSide(t *testing.T) {
	if got := MapSide("1"); got != "BUY" {
		t.Errorf("MapSide(1) = %q, want BUY", got)
	}
	if got := MapSide("2"); got != "SELL" {
		t.Errorf("MapSide(2) = %q, want SELL", got)
	}
	if got := MapSide("9"); got != "" {
		t.Errorf("MapSide(9) = %q, want empty", got)
	}
}

func TestDeriveFill(t *testing.T) {
	filled := realOrderResponseFixture
	filled.OrderStatus = "Traded"
	filled.QtyFilledToday = "50"
	update := Parse(&filled)

	// No prior fill -> full 50 is a new fill.
	fill, ok := DeriveFill(update, 0)
	if !ok {
		t.Fatalf("expected a fill to be derived")
	}
	if fill.FillQtyDelta != 50 {
		t.Fatalf("FillQtyDelta = %d, want 50", fill.FillQtyDelta)
	}
	if fill.FillPrice != 18500.00 {
		t.Fatalf("FillPrice = %v, want 18500.00", fill.FillPrice)
	}

	// Already fully filled previously -> no new fill.
	if _, ok := DeriveFill(update, 50); ok {
		t.Fatalf("expected no fill when previouslyFilledQty already matches")
	}

	// Partial: previously 20 filled, now 50 -> delta 30.
	partial, ok := DeriveFill(update, 20)
	if !ok || partial.FillQtyDelta != 30 {
		t.Fatalf("expected delta fill of 30, got ok=%v delta=%d", ok, partial.FillQtyDelta)
	}

	// Zero price should never be treated as a real fill even if qty increased.
	zeroPrice := filled
	zeroPrice.Price = "0"
	zpUpdate := Parse(&zeroPrice)
	if _, ok := DeriveFill(zpUpdate, 0); ok {
		t.Fatalf("expected no fill derived when price is zero")
	}
}
