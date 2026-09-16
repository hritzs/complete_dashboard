package contracts

import "time"

// This file defines message shapes reserved for a future C++ trade-worker
// ABI bridge (fixed-width uint64 IDs and typed enums, mirroring
// order_update.hpp/fill_event.hpp). As of 2026-09 nothing in the codebase
// produces or consumes these: services/trade-worker's fill_handler.cpp is
// an empty stub excluded from its own build, and market-state is
// unimplemented. The real, currently-used OrderIntent/OrderUpdate/FillEvent
// contracts -- string-keyed, matching Postgres and the reconciler -- live
// in events.go. Do not add new producers/consumers of the Cpp* types below
// until the C++ bridge they're meant for actually exists.

// --- Common Enums ---

type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

type OrderType string

const (
	OrderTypeMarket         OrderType = "MARKET"
	OrderTypeLimit          OrderType = "LIMIT"
	OrderTypeStopLossMarket OrderType = "SL"
	OrderTypeStopLossLimit  OrderType = "SL-M"
)

type ProductType string

const (
	ProductTypeNRML ProductType = "NRML"
	ProductTypeMIS  ProductType = "MIS"
	ProductTypeCNC  ProductType = "CNC"
)

type OrderStatus string

const (
	StatusSubmitted        OrderStatus = "SUBMITTED"
	StatusAcked            OrderStatus = "ACKED"
	StatusPartialFill      OrderStatus = "PARTIAL_FILL"
	StatusFilled           OrderStatus = "FILLED"
	StatusCancelled        OrderStatus = "CANCELLED"
	StatusRMSRejected      OrderStatus = "RMS_REJECTED"
	StatusExchangeRejected OrderStatus = "EXCHANGE_REJECTED"
	StatusExpired          OrderStatus = "EXPIRED"
)

// --- Reserved C++ ABI message shapes (currently unused) ---

// CppOrderIntent is the fixed-width shape a TradeWorker would emit to
// signal a desire to place an order, if/when a C++ ABI bridge exists.
type CppOrderIntent struct {
	IntentID     uint64      `json:"intent_id"`
	TradeID      uint64      `json:"trade_id"`
	WorkerID     uint64      `json:"worker_id"`
	InstrumentID uint32      `json:"instrument_id"`
	Side         Side        `json:"side"`
	Quantity     uint32      `json:"quantity"`
	OrderType    OrderType   `json:"order_type"`
	LimitPrice   float64     `json:"limit_price,omitempty"`
	TriggerPrice float64     `json:"trigger_price,omitempty"`
	ProductType  ProductType `json:"product_type"`
	Meta         string      `json:"meta,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
}

// CppOrderUpdate is the fixed-width shape of an order's state change, for
// a future C++ ABI bridge. See events.OrderUpdate for the real, currently
// emitted contract.
type CppOrderUpdate struct {
	IntentID        uint64      `json:"intent_id"`
	BrokerOrderID   string      `json:"broker_order_id"`
	ExchangeOrderID string      `json:"exchange_order_id,omitempty"`
	TradeID         uint64      `json:"trade_id"`
	Status          OrderStatus `json:"status"`
	FilledQty       uint32      `json:"filled_qty"`
	PendingQty      uint32      `json:"pending_qty"`
	AvgFillPrice    float64     `json:"avg_fill_price"`
	ReasonCode      string      `json:"reason_code,omitempty"`
	ReasonText      string      `json:"reason_text,omitempty"`
	BrokerTimestamp time.Time   `json:"broker_timestamp"`
}

// CppFillEvent is the fixed-width shape of a single fill, for a future C++
// ABI bridge. See events.FillEvent for the real, currently emitted contract.
type CppFillEvent struct {
	FillID        uint64    `json:"fill_id"`
	IntentID      uint64    `json:"intent_id"`
	BrokerOrderID string    `json:"broker_order_id"`
	TradeID       uint64    `json:"trade_id"`
	InstrumentID  uint32    `json:"instrument_id"`
	Side          Side      `json:"side"`
	FillQty       uint32    `json:"fill_qty"`
	FillPrice     float64   `json:"fill_price"`
	FillTime      time.Time `json:"fill_time"`
}
