package contracts

import "time"

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

// --- Message Contracts ---

// OrderIntent is emitted by a TradeWorker to signal a desire to place an order.
type OrderIntent struct {
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

// OrderUpdate is a canonical representation of an order's state change, published by the Reconciler.
type OrderUpdate struct {
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

// FillEvent is a canonical representation of a single fill, published by the Reconciler.
type FillEvent struct {
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