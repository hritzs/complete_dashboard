package contracts

import "time"

// TradeCommand is used for operator-facing actions like create, pause, resume.
type TradeCommand struct {
	CommandID string    `json:"command_id"`
	TradeID   string    `json:"trade_id"`
	Action    string    `json:"action"` // e.g., "CREATE", "PAUSE", "RESUME", "FORCE_CLOSE"
	Params    []byte    `json:"params"` // JSON-encoded parameters for the action
	CreatedAt time.Time `json:"created_at"`
}

// OrderIntent is a broker-neutral representation of a desire to place an order.
// This is emitted by a `trade-worker`.
type OrderIntent struct {
	IntentID      string    `json:"intent_id"`
	TradeID       string    `json:"trade_id"`
	WorkerID      string    `json:"worker_id"`
	InstrumentID  int64     `json:"instrument_id"` // Broker-agnostic internal instrument ID
	Side          string    `json:"side"`          // "BUY" or "SELL"
	Quantity      int64     `json:"quantity"`
	OrderType     string    `json:"order_type"` // "LIMIT", "MARKET"
	LimitPrice    float64   `json:"limit_price"`
	TriggerPrice  float64   `json:"trigger_price"`
	ProductType   string    `json:"product_type"` // "MIS", "NRML"
	CorrelationID string    `json:"correlation_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// OrderUpdate is a canonical, normalized representation of an order's state change.
// This is emitted by the `reconciler`.
type OrderUpdate struct {
	IntentID        string    `json:"intent_id"`
	BrokerOrderID   string    `json:"broker_order_id"`
	ExchangeOrderID string    `json:"exchange_order_id"`
	TradeID         string    `json:"trade_id"`
	Status          string    `json:"status"` // e.g., "SUBMITTED", "ACKED", "FILLED", "CANCELLED", "REJECTED"
	FilledQty       int64     `json:"filled_qty"`
	PendingQty      int64     `json:"pending_qty"`
	AvgFillPrice    float64   `json:"avg_fill_price"`
	ReasonCode      string    `json:"reason_code"`
	ReasonText      string    `json:"reason_text"`
	BrokerTimestamp time.Time `json:"broker_timestamp"`
	InternalTime    time.Time `json:"internal_time"`
}

// FillEvent represents a single execution/fill.
// This is emitted by the `reconciler` for every partial or full fill.
type FillEvent struct {
	FillID          string    `json:"fill_id"`
	IntentID        string    `json:"intent_id"`
	BrokerOrderID   string    `json:"broker_order_id"`
	TradeID         string    `json:"trade_id"`
	InstrumentID    int64     `json:"instrument_id"`
	Side            string    `json:"side"`
	FillQty         int64     `json:"fill_qty"`
	FillPrice       float64   `json:"fill_price"`
	FillTime        time.Time `json:"fill_time"`
}

// WorkerHeartbeat is emitted by a C++ trade worker via the ZMQ->NATS bridge.
type WorkerHeartbeat struct {
	WorkerID    string    `json:"worker_id"`
	TradeID     string    `json:"trade_id"`
	Status      string    `json:"status"` // e.g., "RUNNING", "IDLE", "ERROR"
	Strategy    string    `json:"strategy"`
	LastUpdated time.Time `json:"last_updated"`
}

// LivePosition represents a single open leg of a trade.
type LivePosition struct {
	Token       int64   `json:"token"`
	Strike      float64 `json:"strike"`
	OptionType  string  `json:"option_type"`
	Quantity    int64   `json:"quantity"`
	Action      string  `json:"action"` // "BUY" or "SELL"
	EntryPrice  float64 `json:"entry_price"`
	LTP         float64 `json:"ltp"`
	PNL         float64 `json:"pnl"`
	IV          float64 `json:"iv"`
	Delta       float64 `json:"delta"`
	Gamma       float64 `json:"gamma"`
	Theta       float64 `json:"theta"`
	Vega        float64 `json:"vega"`
}

// TradeSnapshot is a full-computation snapshot of a trade's state.
type TradeSnapshot struct {
	TradeUID      string         `json:"trade_uid"`
	Timestamp     time.Time      `json:"timestamp"`
	Status        string         `json:"status"`
	TotalPNL      float64        `json:"total_pnl"`
	RealizedPNL   float64        `json:"realized_pnl"`
	UnrealizedPNL float64        `json:"unrealized_pnl"`
	NetDelta      float64        `json:"net_delta"`
	NetGamma      float64        `json:"net_gamma"`
	LivePositions []LivePosition `json:"live_positions"`
}