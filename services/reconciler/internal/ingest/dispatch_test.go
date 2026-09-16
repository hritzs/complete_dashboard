package ingest

import (
	"testing"

	greeksoft "trading-platform/libs/broker-greeksoft"
)

func TestDispatch_OrderResponse(t *testing.T) {
	// Real OrderResponse shape from GreekSoft's official websocket docs.
	raw := []byte(`{
		"response": {
			"svcName": "order",
			"serverTime": "1685016746000",
			"infoID": "0",
			"streaming_type": "OrderResponse",
			"data": {
				"side": "1", "qty": "50", "product": "0", "gtoken": "102036187",
				"order_status": "Pending", "eorderid": "1000000000101012",
				"gorderid": "120140064", "lu_time_exchange": "1369503632",
				"lu_time": "1685016632", "symbol": "NIFTY 25MAY23",
				"pending_qty": "50", "qty_filled_today": "0",
				"price": "18500.00", "trigger_price": "0.00"
			}
		}
	}`)

	frame := greeksoft.IrisFrame{StreamingType: "OrderResponse", ServiceName: "order", Raw: raw}

	kind, payload, tradePayload, err := Dispatch(frame)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if kind != KindOrderResponse {
		t.Fatalf("kind = %v, want KindOrderResponse", kind)
	}
	if payload == nil {
		t.Fatal("payload is nil")
	}
	if tradePayload != nil {
		t.Fatal("expected nil trade payload for an OrderResponse frame")
	}
	if payload.GOrderID != "120140064" {
		t.Errorf("GOrderID = %q, want 120140064", payload.GOrderID)
	}
	if payload.OrderStatus != "Pending" {
		t.Errorf("OrderStatus = %q, want Pending", payload.OrderStatus)
	}
}

func TestDispatch_TradeResponse(t *testing.T) {
	// Real TradeResponse shape confirmed live on 2026-09-16 (account 147):
	// a distinct push frame sent only when a fill actually executes,
	// carrying the exact traded price/qty via tradeid/traded_qty/traded_price.
	raw := []byte(`{
		"response": {
			"svcName": "order",
			"streaming_type": "TradeResponse",
			"data": {
				"side": "2", "gorderid": "147829", "eorderid": "1000000000771012",
				"order_status": "Executed", "qty_filled_today": "65",
				"tradeid": "2932987", "traded_qty": "65", "traded_price": "155.70",
				"pending_qty": "0", "lu_time": "1757999999"
			}
		}
	}`)

	frame := greeksoft.IrisFrame{StreamingType: "TradeResponse", ServiceName: "order", Raw: raw}

	kind, orderPayload, payload, err := Dispatch(frame)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if kind != KindTradeResponse {
		t.Fatalf("kind = %v, want KindTradeResponse", kind)
	}
	if orderPayload != nil {
		t.Fatal("expected nil order payload for a TradeResponse frame")
	}
	if payload == nil {
		t.Fatal("payload is nil")
	}
	if payload.GOrderID != "147829" {
		t.Errorf("GOrderID = %q, want 147829", payload.GOrderID)
	}
	if payload.TradeID != "2932987" {
		t.Errorf("TradeID = %q, want 2932987", payload.TradeID)
	}
	if payload.TradedPrice != "155.70" {
		t.Errorf("TradedPrice = %q, want 155.70", payload.TradedPrice)
	}
}

func TestDispatch_NonOrderFrames(t *testing.T) {
	cases := []struct {
		streamingType string
		wantKind      FrameKind
	}{
		{greeksoft.StreamingTypeLoginResponse, KindLogin},
		{greeksoft.StreamingTypeHeartBeat, KindHeartBeat},
		{greeksoft.StreamingTypeLicense, KindLicense},
		{"SomethingElseEntirely", KindUnknown},
	}
	for _, c := range cases {
		frame := greeksoft.IrisFrame{StreamingType: c.streamingType, Raw: []byte(`{}`)}
		kind, payload, tradePayload, err := Dispatch(frame)
		if err != nil {
			t.Errorf("streamingType=%q: unexpected error %v", c.streamingType, err)
		}
		if kind != c.wantKind {
			t.Errorf("streamingType=%q: kind = %v, want %v", c.streamingType, kind, c.wantKind)
		}
		if payload != nil {
			t.Errorf("streamingType=%q: expected nil payload", c.streamingType)
		}
		if tradePayload != nil {
			t.Errorf("streamingType=%q: expected nil trade payload", c.streamingType)
		}
	}
}

func TestDispatch_MissingGOrderID(t *testing.T) {
	raw := []byte(`{"response":{"streaming_type":"OrderResponse","data":{"side":"1"}}}`)
	frame := greeksoft.IrisFrame{StreamingType: "OrderResponse", Raw: raw}

	_, payload, _, err := Dispatch(frame)
	if err == nil {
		t.Fatal("expected an error for a frame missing gorderid")
	}
	if payload != nil {
		t.Fatal("expected nil payload on error")
	}
}
