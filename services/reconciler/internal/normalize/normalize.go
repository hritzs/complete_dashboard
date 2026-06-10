package normalize

import (
	"strings"
	"time"
)

// CanonicalOrderStatus represents our internal standard states
type CanonicalOrderStatus string

const (
	StatusSubmitted CanonicalOrderStatus = "SUBMITTED"
	StatusAcked     CanonicalOrderStatus = "ACKED"
	StatusPartial   CanonicalOrderStatus = "PARTIAL_FILL"
	StatusFilled    CanonicalOrderStatus = "FILLED"
	StatusCancelled CanonicalOrderStatus = "CANCELLED"
	StatusRejected  CanonicalOrderStatus = "REJECTED"
)

// OrderUpdate is the canonical internal event broadcasted to C++ workers
type OrderUpdate struct {
	IntentID        string               `json:"intent_id"`
	BrokerOrderID   string               `json:"broker_order_id"`
	ExchangeOrderID string               `json:"exchange_order_id"`
	Status          CanonicalOrderStatus `json:"status"`
	FilledQty       int                  `json:"filled_qty"`
	PendingQty      int                  `json:"pending_qty"`
	AvgFillPrice    float64              `json:"avg_fill_price"`
	ReasonText      string               `json:"reason_text"`
	BrokerTimestamp time.Time            `json:"broker_timestamp"`
}

// RawBrokerEvent represents an incoming WebSocket message from Greeksoft/XTS
type RawBrokerEvent struct {
	AppOrderID      string  `json:"AppOrderID"`
	OrderUniqueId   string  `json:"OrderUniqueIdentifier"`
	ExchangeOrderID string  `json:"ExchangeOrderID"`
	OrderStatus     string  `json:"OrderStatus"`
	OrderQuantity   int     `json:"OrderQuantity"`
	LeavesQuantity  int     `json:"LeavesQuantity"`
	CumQuantity     int     `json:"CumulativeQuantity"`
	AvgTradedPrice  float64 `json:"OrderAverageTradedPrice"`
	CancelReject    string  `json:"CancelRejectReason"`
	ExchangeTime    string  `json:"ExchangeTransactTime"`
}

// Parse normalizes a raw broker event into our canonical format
func Parse(raw *RawBrokerEvent) OrderUpdate {
	return OrderUpdate{
		IntentID:        raw.OrderUniqueId,
		BrokerOrderID:   raw.AppOrderID,
		ExchangeOrderID: raw.ExchangeOrderID,
		Status:          MapStatus(raw.OrderStatus),
		FilledQty:       raw.CumQuantity,
		PendingQty:      raw.LeavesQuantity,
		AvgFillPrice:    raw.AvgTradedPrice,
		ReasonText:      raw.CancelReject,
		BrokerTimestamp: time.Now(), // In production, parse raw.ExchangeTime safely
	}
}

// MapStatus translates broker-specific string states into our canonical enum
func MapStatus(rawStatus string) CanonicalOrderStatus {
	s := strings.ToUpper(rawStatus)
	switch s {
	case "NEW", "OPEN":
		return StatusAcked
	case "FILLED", "TRADED", "EXECUTED":
		return StatusFilled
	case "PARTIALLYFILLED":
		return StatusPartial
	case "CANCELLED", "CANCELED":
		return StatusCancelled
	case "REJECTED":
		return StatusRejected
	default:
		return StatusSubmitted
	}
}
