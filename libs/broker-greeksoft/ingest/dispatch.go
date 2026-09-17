// Package ingest turns a raw Iris websocket frame into a typed payload
// callers can work with. Business logic (what an OrderResponse means, how
// to persist it) deliberately does NOT live here -- this package only
// classifies and decodes. Lives in libs/broker-greeksoft (not a single
// service's internal/) since both services/reconciler and
// services/execution-gateway consume the same Iris wire format.
package ingest

import (
	"encoding/json"
	"fmt"

	greeksoft "trading-platform/libs/broker-greeksoft"
	"trading-platform/libs/broker-greeksoft/normalize"
)

// FrameKind identifies what an Iris frame contained.
type FrameKind int

const (
	KindUnknown FrameKind = iota
	KindOrderResponse
	KindTradeResponse
	KindLogin
	KindHeartBeat
	KindLicense
)

// Dispatch classifies frame and decodes its data payload: for
// KindOrderResponse into a normalize.GreeksoftOrderResponse, for
// KindTradeResponse into a normalize.GreeksoftTradeResponse. Exactly one
// of the two return payloads is non-nil for those two kinds; both are nil
// otherwise.
func Dispatch(frame greeksoft.IrisFrame) (FrameKind, *normalize.GreeksoftOrderResponse, *normalize.GreeksoftTradeResponse, error) {
	switch frame.StreamingType {
	case greeksoft.StreamingTypeOrderResponse:
		payload, err := decodeOrderResponse(frame.Raw)
		if err != nil {
			return KindOrderResponse, nil, nil, err
		}
		return KindOrderResponse, payload, nil, nil
	case greeksoft.StreamingTypeTradeResponse:
		payload, err := decodeTradeResponse(frame.Raw)
		if err != nil {
			return KindTradeResponse, nil, nil, err
		}
		return KindTradeResponse, nil, payload, nil
	case greeksoft.StreamingTypeLoginResponse, greeksoft.StreamingTypeLogin:
		return KindLogin, nil, nil, nil
	case greeksoft.StreamingTypeHeartBeat:
		return KindHeartBeat, nil, nil, nil
	case greeksoft.StreamingTypeLicense:
		return KindLicense, nil, nil, nil
	default:
		return KindUnknown, nil, nil, nil
	}
}

func decodeOrderResponse(raw []byte) (*normalize.GreeksoftOrderResponse, error) {
	var envelope struct {
		Response struct {
			Data json.RawMessage `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode Iris envelope: %w", err)
	}
	if len(envelope.Response.Data) == 0 {
		return nil, fmt.Errorf("Iris OrderResponse frame has no data")
	}

	var payload normalize.GreeksoftOrderResponse
	if err := json.Unmarshal(envelope.Response.Data, &payload); err != nil {
		return nil, fmt.Errorf("decode OrderResponse data: %w", err)
	}
	if payload.GOrderID == "" {
		return nil, fmt.Errorf("OrderResponse frame missing gorderid")
	}
	return &payload, nil
}

func decodeTradeResponse(raw []byte) (*normalize.GreeksoftTradeResponse, error) {
	var envelope struct {
		Response struct {
			Data json.RawMessage `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode Iris envelope: %w", err)
	}
	if len(envelope.Response.Data) == 0 {
		return nil, fmt.Errorf("Iris TradeResponse frame has no data")
	}

	var payload normalize.GreeksoftTradeResponse
	if err := json.Unmarshal(envelope.Response.Data, &payload); err != nil {
		return nil, fmt.Errorf("decode TradeResponse data: %w", err)
	}
	if payload.GOrderID == "" {
		return nil, fmt.Errorf("TradeResponse frame missing gorderid")
	}
	return &payload, nil
}
