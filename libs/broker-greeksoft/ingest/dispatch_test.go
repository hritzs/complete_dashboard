package ingest

import (
	"testing"

	greeksoft "trading-platform/libs/broker-greeksoft"
	"trading-platform/libs/broker-greeksoft/normalize"
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

func TestDispatch_RmsRejectionResponse(t *testing.T) {
	// Real RmsRejectionResponse captured live 2026-09-23: an order that was
	// already accepted (NewOrderRequestResponse ErrorCode=0) got rejected
	// by risk management afterwards ("Scrip Banned by Admin"). Before this
	// streaming_type was recognized, Dispatch fell through to its default
	// case (KindUnknown) and the reconciler silently dropped it, leaving
	// the order stuck at SUBMITTED forever even though it was actually
	// dead -- confirmed live: the trade got stranded in
	// RECONCILIATION_REQUIRED and BUILD-CHASE's later modify/cancel calls
	// failed with "order is not in pending or not found".
	raw := []byte(`{
		"response": {
			"svcName": "order",
			"serverTime": "1790153142000",
			"infoID": "0",
			"streaming_type": "RmsRejectionResponse",
			"data": {
				"gtoken": "102069806", "order_status": "Rms Rejected", "eorderid": "",
				"gorderid": "120000037", "lu_time_exchange": "1790153142",
				"lu_time": "1790153142", "tradeSymbol": "BANKNIFTY",
				"symbol": "BANKNIFTY 29SEP26 CE 56700", "order_state": "0",
				"code": "102", "side": "2", "qty": "30", "pending_qty": "30",
				"order_type": "1", "product": "1",
				"reason": "[RMS Failure] Scrip Banned by Admin| [NSE] [BANKNIFTY 29SEP26 CE 56700] SELL 30 @ 309.8000 Scrip Banned"
			},
			"appID": "bc90bb525bc9739a9595bb9e176dab17"
		}
	}`)

	frame := greeksoft.IrisFrame{StreamingType: "RmsRejectionResponse", ServiceName: "order", Raw: raw}

	kind, payload, tradePayload, err := Dispatch(frame)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if kind != KindOrderResponse {
		t.Fatalf("kind = %v, want KindOrderResponse (converges on the same persistence path as any other rejection)", kind)
	}
	if tradePayload != nil {
		t.Fatal("expected nil trade payload for an RmsRejectionResponse frame")
	}
	if payload == nil {
		t.Fatal("payload is nil")
	}
	if payload.GOrderID != "120000037" {
		t.Errorf("GOrderID = %q, want 120000037", payload.GOrderID)
	}
	if payload.OrderStatus != "Rms Rejected" {
		t.Errorf("OrderStatus = %q, want %q", payload.OrderStatus, "Rms Rejected")
	}
	if got := normalize.MapStatus(payload.OrderStatus); got != normalize.StatusRejected {
		t.Errorf("MapStatus(%q) = %v, want StatusRejected", payload.OrderStatus, got)
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
